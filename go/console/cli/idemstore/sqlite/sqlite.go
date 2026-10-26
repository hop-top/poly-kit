// Package sqlite is the persistent idempotency-key store for
// go/console/cli/idemstore: one sqlite database per tool, by default
// $XDG_STATE_HOME/<tool>/idempotency.db, with a TTL after which a
// recorded result is treated as missing and deleted. It lives outside
// idemstore so a CLI that never opens it links no SQLite.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/storage/sqldb"
)

// maxPurgeInterval is the longest a sqlite Store goes between purges
// of expired rows while it is being written to. A shorter TTL purges
// once per TTL.
const maxPurgeInterval = time.Hour

// defaultPurgeBatch bounds how many expired rows one periodic purge
// deletes, so the write that triggers it pays a bounded cost.
const defaultPurgeBatch = 1000

// Open opens (or creates) the sqlite-backed Store at path.
// A zero or negative ttl is replaced with idemstore.DefaultTTL. The parent
// directory of path is created with mode 0750 if it does not exist.
//
// Use ":memory:" as path for an isolated in-memory sqlite Store
// (separate from idemstore.Memory(), which has no SQL layer).
//
// Expired rows are deleted, not only hidden: every expired row when
// the Store opens, and then, at most once per TTL or per hour,
// whichever is shorter, up to a bounded batch on the Record that
// finds the interval elapsed. A batch that comes back full leaves the
// next Record to continue, so a backlog drains without any one write
// paying for all of it. Purging is best-effort: a failed purge leaves
// the rows for the next one and never fails the open or the Record.
func Open(path string, ttl time.Duration) (idemstore.Store, error) {
	if path == "" {
		return nil, errors.New("idemstore: path required")
	}
	if ttl <= 0 {
		ttl = idemstore.DefaultTTL
	}
	db, err := sqldb.Open(sqldb.Options{Path: path})
	if err != nil {
		return nil, fmt.Errorf("idemstore: open sqlite: %w", err)
	}
	if _, err := db.Exec(`
create table if not exists idempotency (
  key        text primary key,
  exit_code  integer not null,
  output     blob not null,
  recorded   text not null
);`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("idemstore: migrate: %w", err)
	}
	s := &store{db: db, ttl: ttl, now: time.Now, purgeBatch: defaultPurgeBatch}
	s.purgeEvery = min(ttl, maxPurgeInterval)
	now := s.now()
	_, _ = s.purge(context.Background(), now, -1)
	s.nextPurge = now.Add(s.purgeEvery)
	return s, nil
}

type store struct {
	db  *sql.DB
	ttl time.Duration
	// now is the store's time source; time.Now outside tests.
	// Injectable so TTL expiry is testable without wall-clock sleeps.
	now func() time.Time

	// purgeEvery is the interval between periodic purges and
	// purgeBatch the most rows one deletes.
	purgeEvery time.Duration
	purgeBatch int

	mu sync.Mutex
	// nextPurge is when the next Record purges; zero means now.
	nextPurge time.Time
}

// purge deletes up to limit rows recorded longer than the TTL before
// now (every such row when limit is negative) and reports how many
// it deleted. The comparison is on the instant, not the text: an
// RFC3339Nano timestamp with no fraction does not sort as text among
// ones with a fraction.
func (s *store) purge(ctx context.Context, now time.Time, limit int) (int64, error) {
	cutoff := now.Add(-s.ttl)
	if cutoff.Year() < 1 {
		// A TTL longer than the calendar: nothing has expired.
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx,
		`delete from idempotency where rowid in (
		   select rowid from idempotency
		   where julianday(recorded) < julianday(?)
		   limit ?)`,
		cutoff.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return 0, fmt.Errorf("idemstore: purge: %w", err)
	}
	return res.RowsAffected()
}

// maybePurge runs a periodic purge when its interval has elapsed. A
// batch that comes back full schedules the next one immediately.
func (s *store) maybePurge(ctx context.Context) {
	now := s.now()
	s.mu.Lock()
	if now.Before(s.nextPurge) {
		s.mu.Unlock()
		return
	}
	s.nextPurge = now.Add(s.purgeEvery)
	s.mu.Unlock()

	n, err := s.purge(ctx, now, s.purgeBatch)
	if err == nil && n >= int64(s.purgeBatch) {
		s.mu.Lock()
		s.nextPurge = time.Time{}
		s.mu.Unlock()
	}
}

func (s *store) Lookup(ctx context.Context, key string) (idemstore.Result, bool, error) {
	var (
		exitCode int
		output   []byte
		recorded string
	)
	err := s.db.QueryRowContext(ctx,
		`select exit_code, output, recorded from idempotency where key = ?`, key,
	).Scan(&exitCode, &output, &recorded)
	if errors.Is(err, sql.ErrNoRows) {
		return idemstore.Result{}, false, nil
	}
	if err != nil {
		return idemstore.Result{}, false, fmt.Errorf("idemstore: lookup: %w", err)
	}
	rec, perr := time.Parse(time.RFC3339Nano, recorded)
	if perr != nil {
		// Best-effort — fall back to RFC3339 (without nanos).
		rec, perr = time.Parse(time.RFC3339, recorded)
		if perr != nil {
			return idemstore.Result{}, false, fmt.Errorf("idemstore: parse recorded: %w", perr)
		}
	}
	if s.ttl > 0 && s.now().Sub(rec) > s.ttl {
		return idemstore.Result{}, false, nil
	}
	return idemstore.Result{
		Key:      key,
		ExitCode: exitCode,
		Output:   output,
		Recorded: rec,
	}, true, nil
}

func (s *store) Record(ctx context.Context, key string, r idemstore.Result) error {
	if r.Recorded.IsZero() {
		r.Recorded = s.now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`insert into idempotency (key, exit_code, output, recorded)
		 values (?, ?, ?, ?)
		 on conflict(key) do update set
		   exit_code=excluded.exit_code,
		   output=excluded.output,
		   recorded=excluded.recorded`,
		key, r.ExitCode, r.Output, r.Recorded.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("idemstore: record: %w", err)
	}
	s.maybePurge(ctx)
	return nil
}

func (s *store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
