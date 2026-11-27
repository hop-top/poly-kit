package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"time"
)

// SQL shared verbatim by every port: the lock key text is hashed by the
// server, so all processes derive the same 64-bit key.
const (
	sqlLock     = `SELECT pg_advisory_lock(hashtextextended($1, 0))`
	sqlUnlock   = `SELECT pg_advisory_unlock(hashtextextended($1, 0))`
	sqlXactLock = `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`
)

// unlockTimeout bounds the unlock statement, which runs detached from the
// run's context so a canceled run still releases its lock.
const unlockTimeout = 30 * time.Second

// lockKey is the text hashed into the advisory lock key for a run.
func lockKey(table, name string) string { return "kit-upgrade:" + table + ":" + name }

// ddlKey is the text hashed into the key that serializes table creation.
func ddlKey(table string) string { return "kit-upgrade:" + table + ":ddl" }

// Lock takes a session-level advisory lock on one dedicated connection
// and holds that connection until the returned unlock runs. It blocks
// until the lock is acquired or ctx is done. The unlock statement runs on
// that same connection; if it fails the physical connection is discarded,
// which ends the session and with it the lock.
//
// The lock connection is taken from the pool, so the pool needs at least
// two connections (one for the lock, one for the run): a pool capped at
// one is refused with an error instead of deadlocking.
func (d *Driver) Lock(ctx context.Context) (func() error, error) {
	if n := d.db.Stats().MaxOpenConnections; n == 1 {
		return nil, errors.New("postgres upgrade: lock needs a pool of at least " +
			"two connections: the lock holds one for the whole run " +
			"(db.SetMaxOpenConns(1) would deadlock)")
	}
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres upgrade: lock: %w", err)
	}
	key := lockKey(d.table, d.name)
	if _, err := conn.ExecContext(ctx, sqlLock, key); err != nil {
		// A canceled wait may still have been granted server side: end
		// the session rather than hand the connection back with a lock.
		discard(conn)
		return nil, fmt.Errorf("postgres upgrade: lock: %w", err)
	}

	var once sync.Once
	var unlockErr error
	return func() error {
		once.Do(func() { unlockErr = unlock(ctx, conn, key) })
		return unlockErr
	}, nil
}

func unlock(ctx context.Context, conn *sql.Conn, key string) error {
	uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
	defer cancel()
	var held bool
	err := conn.QueryRowContext(uctx, sqlUnlock, key).Scan(&held)
	if err == nil && !held {
		err = errors.New("lock was not held by this session")
	}
	if err != nil {
		discard(conn)
		return fmt.Errorf("postgres upgrade: unlock: %w", err)
	}
	// Session-level lock released by statement; now the connection can
	// safely go back to the pool.
	return conn.Close()
}

// discard ends the physical connection behind conn (the server drops the
// session and every advisory lock it holds) and releases the *sql.Conn.
func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}
