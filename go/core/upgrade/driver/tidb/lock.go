package tidb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// maxLockName is the longest GET_LOCK name the server accepts.
	maxLockName = 64
	// lockPollInterval is the client-side pause between GET_LOCK attempts.
	// The attempts themselves never wait on the server.
	lockPollInterval = 50 * time.Millisecond
)

// lockName is the GET_LOCK name for a table and schema: "<table>:<schema>".
// A name longer than maxLockName bytes is cut on a rune boundary and ends
// in "#" plus the first 16 hex digits of the SHA-256 of the full name, so it
// is deterministic and two long names that differ only past the cut still
// differ.
func lockName(table, schema string) string {
	full := table + ":" + schema
	if len(full) <= maxLockName {
		return full
	}
	sum := sha256.Sum256([]byte(full))
	suffix := "#" + hex.EncodeToString(sum[:8])
	cut := maxLockName - len(suffix)
	for cut > 0 && !utf8.RuneStart(full[cut]) {
		cut--
	}
	return full[:cut] + suffix
}

// Lock takes the server-side named lock for this table and schema on a
// dedicated connection and returns the function that releases it. It
// implements upgrade.Locker.
//
// It polls SELECT GET_LOCK(name, 0), which never waits on the server, and
// pauses lockPollInterval on the client between attempts until the server
// grants the lock or ctx is done. Cancellation is noticed within one pause
// and leaves no server-side wait behind: a blocking GET_LOCK that was
// already sent when the client gave up would still be granted the lock
// when the holder released it, and the dead session would hold it until the
// server noticed the closed connection. The lock belongs to the session, so it sits on
// one *sql.Conn for the whole run. unlock runs RELEASE_LOCK on that same
// connection with a context that ignores the caller's cancellation; when
// the release fails the physical connection is discarded, which ends the
// session and with it the lock. unlock is safe to call twice.
//
// Lock returns an error when db.Stats().MaxOpenConnections is 1: the held
// connection would leave none for Version and SetVersion.
func (d *Driver) Lock(ctx context.Context) (func() error, error) {
	if d.db.Stats().MaxOpenConnections == 1 {
		return nil, errors.New("tidb upgrade: lock: the handle's MaxOpenConns is 1; " +
			"the lock holds a connection for the whole run, so allow at least 2")
	}
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("tidb upgrade: lock: %w", err)
	}
	name := lockName(d.table, d.name)
	for {
		var got sql.NullInt64
		err := conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 0)`, name).Scan(&got)
		switch {
		case err != nil:
			discard(conn)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("tidb upgrade: lock %q: %w", name, err)
		case !got.Valid:
			discard(conn) // NULL: the server could not evaluate the request
			return nil, fmt.Errorf("tidb upgrade: lock %q: GET_LOCK returned NULL", name)
		case got.Int64 == 1:
			var once sync.Once
			var unlockErr error
			return func() error {
				once.Do(func() { unlockErr = release(ctx, conn, name) })
				return unlockErr
			}, nil
		}
		// 0: held elsewhere. Pause on the client, then poll again unless done.
		select {
		case <-ctx.Done():
			discard(conn)
			return nil, ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

// release runs RELEASE_LOCK, detached from ctx's cancellation, on the
// lock's own connection and gives the
// connection back to the pool; on any failure it discards the connection.
func release(ctx context.Context, conn *sql.Conn, name string) error {
	ctx = context.WithoutCancel(ctx)
	var got sql.NullInt64
	err := conn.QueryRowContext(ctx, `SELECT RELEASE_LOCK(?)`, name).Scan(&got)
	if err == nil && (!got.Valid || got.Int64 != 1) {
		err = fmt.Errorf("RELEASE_LOCK returned %v, lock not held by this session", got)
	}
	if err != nil {
		discard(conn)
		return fmt.Errorf("tidb upgrade: unlock %q: %w", name, err)
	}
	return conn.Close()
}

// discard closes the physical connection instead of returning it to the
// pool, so no session keeps a lock it may still hold.
func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}
