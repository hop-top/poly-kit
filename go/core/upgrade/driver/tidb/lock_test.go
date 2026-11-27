package tidb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

// stubConnector hands out connections that accept nothing; enough for a
// *sql.DB whose pool settings and Close behavior are under test.
type stubConnector struct{ closed bool }

func (c *stubConnector) Connect(context.Context) (driver.Conn, error) { return stubConn{}, nil }
func (c *stubConnector) Driver() driver.Driver                        { return stubDriver{} }
func (c *stubConnector) Close() error                                 { c.closed = true; return nil }

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) { return stubConn{}, nil }

type stubConn struct{}

func (stubConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("stub") }
func (stubConn) Close() error                        { return nil }
func (stubConn) Begin() (driver.Tx, error)           { return nil, errors.New("stub") }

func TestLockName(t *testing.T) {
	if got := lockName("versions", "app"); got != "versions:app" {
		t.Fatalf("short name = %q, want versions:app", got)
	}
	// 64 bytes exactly is kept as is.
	exact := strings.Repeat("a", 64)
	if got := lockName(exact[:40], exact[:23]); got != exact[:40]+":"+exact[:23] {
		t.Fatalf("64-byte name rewritten: %q", got)
	}

	long := lockName("versions", strings.Repeat("s", 200))
	if len(long) > maxLockName {
		t.Fatalf("len = %d, want <= %d", len(long), maxLockName)
	}
	if long != lockName("versions", strings.Repeat("s", 200)) {
		t.Fatal("shortened name is not deterministic")
	}
	other := lockName("versions", strings.Repeat("s", 199)+"t")
	if long == other {
		t.Fatal("schemas differing only past the cut share a lock name")
	}
	// Multi-byte schema names are cut on a rune boundary.
	mb := lockName("versions", strings.Repeat("é", 100))
	if len(mb) > maxLockName || !strings.HasPrefix(mb, "versions:é") {
		t.Fatalf("multi-byte name = %q (%d bytes)", mb, len(mb))
	}
}

func TestNewWithDB_Validation(t *testing.T) {
	if _, err := NewWithDB(nil, "versions"); err == nil {
		t.Fatal("nil handle accepted")
	}
	db := sql.OpenDB(&stubConnector{})
	defer db.Close()
	if _, err := NewWithDB(db, "bad-name"); err == nil ||
		!strings.Contains(err.Error(), "invalid table name") {
		t.Fatalf("err = %v, want invalid table name", err)
	}
}

// Close never closes a handle the caller owns.
func TestDriver_CloseLeavesCallerHandleOpen(t *testing.T) {
	c := &stubConnector{}
	db := sql.OpenDB(c)
	d := &Driver{db: db, name: "tidb", table: "versions"} // as NewWithDB builds it
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("handle closed by Driver.Close: %v", err)
	}
	if c.closed {
		t.Fatal("connector closed by Driver.Close")
	}
	// The DSN constructor's driver owns its handle and closes it.
	own := &Driver{db: db, name: "tidb", table: "versions", owned: true}
	if err := own.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.PingContext(context.Background()); err == nil {
		t.Fatal("owned handle still open after Close")
	}
}

func TestLock_RefusesPoolOfOne(t *testing.T) {
	db := sql.OpenDB(&stubConnector{})
	defer db.Close()
	db.SetMaxOpenConns(1)
	d := &Driver{db: db, name: "tidb", table: "versions"}
	unlock, err := d.Lock(context.Background())
	if err == nil || unlock != nil {
		t.Fatalf("Lock = (%v, %v), want error and no unlock", unlock != nil, err)
	}
	if !strings.Contains(err.Error(), "MaxOpenConns") {
		t.Fatalf("err = %v, want it to name MaxOpenConns", err)
	}
}
