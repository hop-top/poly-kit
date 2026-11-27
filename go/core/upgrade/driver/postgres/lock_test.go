package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

// stubConnector never connects: Stats().MaxOpenConnections and the
// pool-size refusal need no server.
type stubConnector struct{}

func (stubConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("stub: no server")
}
func (stubConnector) Driver() driver.Driver { return nil }

// An unlimited pool reports 0, not 1: only an explicit cap of one is
// refused.
func TestUnlimitedPoolReportsZero(t *testing.T) {
	db := sql.OpenDB(stubConnector{})
	defer db.Close()
	if got := db.Stats().MaxOpenConnections; got != 0 {
		t.Fatalf("unlimited pool MaxOpenConnections = %d, want 0", got)
	}
	db.SetMaxOpenConns(1)
	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("capped pool MaxOpenConnections = %d, want 1", got)
	}
}

// A pool of one connection is refused before any connection is taken:
// the lock would hold the only connection and starve the run.
func TestLockRefusesPoolOfOne(t *testing.T) {
	db := sql.OpenDB(stubConnector{})
	defer db.Close()
	db.SetMaxOpenConns(1)
	d := &Driver{db: db, name: "app", table: "versions"}

	done := make(chan error, 1)
	go func() {
		_, err := d.Lock(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Lock on a pool of one = nil error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Lock on a pool of one blocked instead of failing")
	}
}
