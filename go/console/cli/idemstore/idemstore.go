// Package idemstore provides the kit-managed idempotency-key store used
// by --idempotency-key replay middleware. The default backend is a
// per-tool sqlite database at $XDG_STATE_HOME/<tool>/idempotency.db
// with a configurable TTL (kit default: 24 hours).
//
// A Memory() backend is provided for tests and adopters that do not
// want disk persistence.
//
// Stored payloads are opaque to idemstore: callers serialize whatever
// envelope they need into Result.Output. The store does not redact
// secrets — that is the adopter's responsibility before
// recording.
//
// Persistence is per-tool: each adopter gets its own database file
// (XDG state dir / <tool> / idempotency.db). Cross-tool replay is
// out of scope by design.
package idemstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"hop.top/kit/go/storage/sqldb"
)

// Result is the recorded outcome of one idempotency-keyed invocation.
// Output is the opaque serialized envelope the RunE middleware
// captured (e.g. JSON output bytes); replay rewrites the same bytes
// back to the requesting stream.
type Result struct {
	// Key is the user-supplied idempotency key.
	Key string `json:"key"`
	// ExitCode is the process exit code recorded for the original
	// invocation (mirrors output.Error.ExitCode for failed runs).
	ExitCode int `json:"exit_code"`
	// Output is the opaque serialized output envelope. Replay writes
	// this to the requesting stream verbatim.
	Output []byte `json:"output"`
	// Recorded is when the original invocation completed and the
	// envelope was persisted.
	Recorded time.Time `json:"recorded"`
}

// Store is the kit-managed idempotency-key persistence interface.
// Lookup is a side-effect-free read; Record upserts the (key, result)
// tuple, replacing any prior record for the same key.
type Store interface {
	// Lookup returns the recorded result for key. Returns
	// (Result{}, false, nil) if the key is unknown or expired,
	// (Result{}, false, err) on backend errors.
	Lookup(ctx context.Context, key string) (Result, bool, error)
	// Record persists r under the given key. r.Recorded is set to
	// time.Now() when it is the zero value. Subsequent Records for
	// the same key overwrite the prior entry — the contract is
	// "last-write-wins" for replay payloads.
	Record(ctx context.Context, key string, r Result) error
	// Close releases backend resources. Calling Close on an
	// already-closed Store is safe.
	Close() error
}

// DefaultTTL is the kit default idempotency TTL (24h). After this
// much wall-clock time the recorded result is treated as missing on
// Lookup, and the sqlite Store deletes it (see [OpenSQLite]).
const DefaultTTL = 24 * time.Hour

// maxPurgeInterval is the longest a sqlite Store goes between purges
// of expired rows while it is being written to. A shorter TTL purges
// once per TTL.
const maxPurgeInterval = time.Hour

// defaultPurgeBatch bounds how many expired rows one periodic purge
// deletes, so the write that triggers it pays a bounded cost.
const defaultPurgeBatch = 1000

// OpenSQLite opens (or creates) the sqlite-backed Store at path.
// A zero or negative ttl is replaced with DefaultTTL. The parent
// directory of path is created with mode 0750 if it does not exist.
//
// Use ":memory:" as path for an isolated in-memory sqlite Store
// (separate from Memory(), which has no SQL layer).
//
// Expired rows are deleted, not only hidden: every expired row when
// the Store opens, and then, at most once per TTL or per hour,
// whichever is shorter, up to a bounded batch on the Record that
// finds the interval elapsed. A batch that comes back full leaves the
// next Record to continue, so a backlog drains without any one write
// paying for all of it. Purging is best-effort: a failed purge leaves
// the rows for the next one and never fails the open or the Record.
func OpenSQLite(path string, ttl time.Duration) (Store, error) {
	if path == "" {
		return nil, errors.New("idemstore: path required")
	}
	if ttl <= 0 {
		ttl = DefaultTTL
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
	s := &sqliteStore{db: db, ttl: ttl, now: time.Now, purgeBatch: defaultPurgeBatch}
	s.purgeEvery = min(ttl, maxPurgeInterval)
	now := s.now()
	_, _ = s.purge(context.Background(), now, -1)
	s.nextPurge = now.Add(s.purgeEvery)
	return s, nil
}

type sqliteStore struct {
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
func (s *sqliteStore) purge(ctx context.Context, now time.Time, limit int) (int64, error) {
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
func (s *sqliteStore) maybePurge(ctx context.Context) {
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

func (s *sqliteStore) Lookup(ctx context.Context, key string) (Result, bool, error) {
	var (
		exitCode int
		output   []byte
		recorded string
	)
	err := s.db.QueryRowContext(ctx,
		`select exit_code, output, recorded from idempotency where key = ?`, key,
	).Scan(&exitCode, &output, &recorded)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("idemstore: lookup: %w", err)
	}
	rec, perr := time.Parse(time.RFC3339Nano, recorded)
	if perr != nil {
		// Best-effort — fall back to RFC3339 (without nanos).
		rec, perr = time.Parse(time.RFC3339, recorded)
		if perr != nil {
			return Result{}, false, fmt.Errorf("idemstore: parse recorded: %w", perr)
		}
	}
	if s.ttl > 0 && s.now().Sub(rec) > s.ttl {
		return Result{}, false, nil
	}
	return Result{
		Key:      key,
		ExitCode: exitCode,
		Output:   output,
		Recorded: rec,
	}, true, nil
}

func (s *sqliteStore) Record(ctx context.Context, key string, r Result) error {
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

func (s *sqliteStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Memory returns an in-process Store with no persistence. Suitable
// for tests and short-lived processes that want lookup-replay within
// a single run. The returned Store is safe for concurrent use.
//
// Memory() does not enforce a TTL — the stored map lives for the
// lifetime of the Store. Tests that exercise expiry should use
// OpenSQLite with an explicit ttl, or call Memory() and Close() per
// scenario.
func Memory() Store {
	return &memoryStore{m: make(map[string]Result), now: time.Now}
}

type memoryStore struct {
	mu sync.Mutex
	m  map[string]Result
	// now is the store's time source; time.Now outside tests.
	now func() time.Time
}

func (s *memoryStore) Lookup(_ context.Context, key string) (Result, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.m[key]
	if !ok {
		return Result{}, false, nil
	}
	return r, true, nil
}

func (s *memoryStore) Record(_ context.Context, key string, r Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Recorded.IsZero() {
		r.Recorded = s.now().UTC()
	}
	r.Key = key
	s.m[key] = r
	return nil
}

func (s *memoryStore) Close() error { return nil }
