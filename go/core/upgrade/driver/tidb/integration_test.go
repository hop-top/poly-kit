package tidb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"hop.top/kit/go/core/upgrade"
)

// One MySQL container serves the whole package run; each test takes its
// own table names. Under the integration target (no -short) a container
// that cannot start fails the test: a missing Docker is not a skip.
var (
	mysqlOnce      sync.Once
	mysqlContainer testcontainers.Container
	mysqlDSN       string
	mysqlErr       error
	tableSeq       atomic.Int64
)

func TestMain(m *testing.M) {
	code := m.Run()
	if mysqlContainer != nil {
		_ = mysqlContainer.Terminate(context.Background())
	}
	os.Exit(code)
}

func startMySQL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	mysqlOnce.Do(func() {
		ctx := context.Background()
		req := testcontainers.ContainerRequest{
			Image:        "mysql:8",
			ExposedPorts: []string{"3306/tcp"},
			Env: map[string]string{
				"MYSQL_ROOT_PASSWORD": "test",
				"MYSQL_DATABASE":      "testdb",
			},
			WaitingFor: wait.ForAll(
				wait.ForLog("ready for connections").WithOccurrence(2),
				wait.ForListeningPort("3306/tcp"),
			).WithDeadline(120 * time.Second),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			mysqlErr = fmt.Errorf("start mysql container: %w", err)
			return
		}
		mysqlContainer = c
		host, err := c.Host(ctx)
		if err != nil {
			mysqlErr = err
			return
		}
		port, err := c.MappedPort(ctx, "3306")
		if err != nil {
			mysqlErr = err
			return
		}
		mysqlDSN = fmt.Sprintf("root:test@tcp(%s:%s)/testdb", host, port.Port())
	})
	require.NoError(t, mysqlErr, "integration tests need Docker")
	return mysqlDSN
}

var nonIdent = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// uniqueTable returns a valid table name unique within the container.
func uniqueTable(t *testing.T) string {
	t.Helper()
	n := nonIdent.ReplaceAllString(strings.ToLower(t.Name()), "_")
	if len(n) > 40 {
		n = n[:40]
	}
	return fmt.Sprintf("%s_%d", n, tableSeq.Add(1))
}

// openDB opens a caller-owned handle, as an application would.
func openDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())
	return db
}

func TestIntegration_VersionRoundtrip(t *testing.T) {
	dsn := startMySQL(t)
	d, err := New(dsn, "test_schema_versions", WithName("myschema"))
	require.NoError(t, err)
	defer d.Close()

	v, err := d.Version()
	require.NoError(t, err)
	assert.Equal(t, "", v)

	require.NoError(t, d.SetVersion("1.0.0"))
	v, err = d.Version()
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", v)

	require.NoError(t, d.SetVersion("2.0.0"))
	v, err = d.Version()
	require.NoError(t, err)
	assert.Equal(t, "2.0.0", v)

	d.db.Exec("DROP TABLE test_schema_versions")
}

func TestIntegration_MultipleSchemas(t *testing.T) {
	dsn := startMySQL(t)
	d1, err := New(dsn, "test_multi_versions", WithName("schema_a"))
	require.NoError(t, err)
	defer d1.Close()

	d2, err := New(dsn, "test_multi_versions", WithName("schema_b"))
	require.NoError(t, err)
	defer d2.Close()

	require.NoError(t, d1.SetVersion("1.0.0"))
	require.NoError(t, d2.SetVersion("3.0.0"))

	v1, err := d1.Version()
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", v1)

	v2, err := d2.Version()
	require.NoError(t, err)
	assert.Equal(t, "3.0.0", v2)

	d1.db.Exec("DROP TABLE test_multi_versions")
}

// A failed migration with no Down cannot be restored by this driver:
// the run reports ErrNotRestored and the table holds the last step
// still applied, not the starting version.
func TestIntegration_RunWithoutDownIsNotRestored(t *testing.T) {
	dsn := startMySQL(t)
	d, err := New(dsn, "test_notrestored_versions", WithName("app"))
	require.NoError(t, err)
	defer d.Close()
	require.NoError(t, d.SetVersion("1.0.0"))

	reg := upgrade.NewRegistry()
	ok := func(context.Context) error { return nil }
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "app", Up: ok})
	reg.Register(upgrade.Migration{Version: "1.2.0", Schema: "app",
		Up: func(context.Context) error { return errors.New("boom") }})
	m := upgrade.NewMigrator("tidbtest", "1.2.0", upgrade.WithRegistry(reg),
		upgrade.WithDataDir(t.TempDir()))
	m.AddDriver(d)

	err = m.Run(context.Background())
	require.ErrorIs(t, err, upgrade.ErrNotRestored)
	require.ErrorIs(t, err, upgrade.ErrBackupUnsupported)

	v, err := d.Version()
	require.NoError(t, err)
	assert.Equal(t, "1.1.0", v)
	d.db.Exec("DROP TABLE test_notrestored_versions")
}

func lockFree(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var free sql.NullInt64
	require.NoError(t, db.QueryRow(`SELECT IS_FREE_LOCK(?)`, name).Scan(&free))
	return free.Valid && free.Int64 == 1
}

// Racing Migrators, each with its own pool as separate processes would
// have, apply every migration exactly once. The Up sleeps so that, with
// no lock, the other runners read the old version while one applies.
func TestIntegration_RacingMigratorsApplyOnce(t *testing.T) {
	dsn := startMySQL(t)
	table := uniqueTable(t)
	logTable := table + "_log"
	admin := openDB(t, dsn)
	_, err := admin.Exec(fmt.Sprintf(`CREATE TABLE %s (version VARCHAR(32), at TIMESTAMP(6) DEFAULT CURRENT_TIMESTAMP(6))`, logTable))
	require.NoError(t, err)

	const runners = 6
	start := make(chan struct{})
	errs := make(chan error, runners)
	var wg sync.WaitGroup
	for i := 0; i < runners; i++ {
		d, err := NewWithDB(openDB(t, dsn), table, WithName("app"))
		require.NoError(t, err)
		reg := upgrade.NewRegistry()
		for _, v := range []string{"1.1.0", "1.2.0"} {
			v := v
			reg.Register(upgrade.Migration{Version: v, Schema: "app",
				Up: func(ctx context.Context) error {
					time.Sleep(150 * time.Millisecond)
					_, err := admin.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (version) VALUES (?)`, logTable), v)
					return err
				}})
		}
		m := upgrade.NewMigrator("tidbrace", "1.2.0", upgrade.WithRegistry(reg),
			upgrade.WithDataDir(t.TempDir()))
		m.AddDriver(d)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- m.Run(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	rows, err := admin.Query(fmt.Sprintf(`SELECT version, COUNT(*) FROM %s GROUP BY version ORDER BY version`, logTable))
	require.NoError(t, err)
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var v string
		var n int
		require.NoError(t, rows.Scan(&v, &n))
		got[v] = n
	}
	assert.Equal(t, map[string]int{"1.1.0": 1, "1.2.0": 1}, got, "each migration applied once")

	d, err := NewWithDB(admin, table, WithName("app"))
	require.NoError(t, err)
	v, err := d.Version()
	require.NoError(t, err)
	assert.Equal(t, "1.2.0", v)
}

// A run that fails leaves no lock behind.
func TestIntegration_LockReleasedAfterFailedRun(t *testing.T) {
	dsn := startMySQL(t)
	table := uniqueTable(t)
	db := openDB(t, dsn)
	d, err := NewWithDB(db, table, WithName("app"))
	require.NoError(t, err)

	reg := upgrade.NewRegistry()
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "app",
		Up: func(context.Context) error { return errors.New("boom") }})
	m := upgrade.NewMigrator("tidbfail", "1.1.0", upgrade.WithRegistry(reg),
		upgrade.WithDataDir(t.TempDir()))
	m.AddDriver(d)
	require.Error(t, m.Run(context.Background()))

	assert.True(t, lockFree(t, db, lockName(table, "app")), "lock still held after a failed run")
	unlock, err := d.Lock(context.Background())
	require.NoError(t, err)
	require.NoError(t, unlock())
}

// A Lock that waits and is canceled returns the context error and
// never ends up holding the lock; unlock still works after the
// caller's context is done.
func TestIntegration_LockReleasedAfterCancelledContext(t *testing.T) {
	dsn := startMySQL(t)
	table := uniqueTable(t)
	db := openDB(t, dsn)
	holder, err := NewWithDB(db, table, WithName("app"))
	require.NoError(t, err)
	waiter, err := NewWithDB(openDB(t, dsn), table, WithName("app"))
	require.NoError(t, err)
	name := lockName(table, "app")

	unlockHolder, err := holder.Lock(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	began := time.Now()
	unlock, err := waiter.Lock(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, unlock)
	assert.Less(t, time.Since(began), 3*time.Second, "cancellation must not wait out the poll")

	require.NoError(t, unlockHolder())
	assert.True(t, lockFree(t, db, name), "canceled waiter took the lock")

	// Unlock after the lock's own context was canceled.
	lctx, lcancel := context.WithCancel(context.Background())
	unlock, err = waiter.Lock(lctx)
	require.NoError(t, err)
	lcancel()
	require.NoError(t, unlock())
	assert.True(t, lockFree(t, db, name))
	require.NoError(t, unlock(), "second unlock is a no-op")
}

// delayProxy forwards TCP to the server and, once armed, holds each
// COM_STMT_EXECUTE back for delay and tells the server the client
// has gone only closeLag after the last chunk. A client that gives up
// while a statement is held back is then gone before the server has seen
// the statement, and the server learns of it late: the window in which a
// canceled statement can still be granted a lock.
type delayProxy struct {
	addr     string
	delay    atomic.Int64
	closeLag atomic.Int64
}

func startDelayProxy(t *testing.T, target string) *delayProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &delayProxy{addr: ln.Addr().String()}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(c, target)
		}
	}()
	return p
}

func (p *delayProxy) serve(c net.Conn, target string) {
	s, err := net.Dial("tcp", target)
	if err != nil {
		_ = c.Close()
		return
	}
	go func() {
		_, _ = io.Copy(c, s)
		_ = c.Close()
	}()
	chunks := make(chan []byte, 64)
	go func() {
		defer close(chunks)
		buf := make([]byte, 32<<10)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				chunks <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	var lag time.Duration
	for b := range chunks {
		// Only COM_STMT_EXECUTE (0x17 after the 4-byte packet header) is held.
		if d := time.Duration(p.delay.Load()); d > 0 && len(b) > 4 && b[4] == 0x17 {
			time.Sleep(d)
			lag = time.Duration(p.closeLag.Load())
		}
		if _, err := s.Write(b); err != nil {
			break
		}
	}
	time.Sleep(lag)
	_ = s.Close()
}

// A statement canceled in flight can reach the server after the client
// has closed its socket. Lock must never leave a server-side wait behind
// for that statement: when the holder releases afterwards, the lock must
// stay free instead of going to a session whose client is gone.
func TestIntegration_LockCanceledStatementArrivesAfterClientGone(t *testing.T) {
	dsn := startMySQL(t)
	table := uniqueTable(t)
	db := openDB(t, dsn)
	holder, err := NewWithDB(db, table, WithName("app"))
	require.NoError(t, err)
	name := lockName(table, "app")

	m := regexp.MustCompile(`@tcp\(([^)]*)\)`).FindStringSubmatch(dsn)
	require.Len(t, m, 2)
	proxy := startDelayProxy(t, m[1])
	pdb := openDB(t, strings.Replace(dsn, m[1], proxy.addr, 1)) // pings: one idle conn
	waiter, err := NewWithDB(pdb, table, WithName("app"))
	require.NoError(t, err)

	unlockHolder, err := holder.Lock(context.Background())
	require.NoError(t, err)

	// The waiter's statement is held back 300ms; its context ends at 100ms,
	// so the client is gone before the server sees the statement.
	proxy.delay.Store(int64(300 * time.Millisecond))
	proxy.closeLag.Store(int64(2 * time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	unlock, err := waiter.Lock(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, unlock)
	proxy.delay.Store(0)

	// Let the statement reach the server (the server does not yet know the
	// client is gone), then release the holder.
	time.Sleep(500 * time.Millisecond)
	require.NoError(t, unlockHolder())

	deadline := time.Now().Add(1200 * time.Millisecond)
	for time.Now().Before(deadline) {
		require.True(t, lockFree(t, db, name), "canceled waiter's session holds the lock")
		time.Sleep(10 * time.Millisecond)
	}
}

// Two drivers on the same table and schema exclude each other; other
// schemas do not.
func TestIntegration_LockExcludesSameSchemaOnly(t *testing.T) {
	dsn := startMySQL(t)
	table := uniqueTable(t)
	db := openDB(t, dsn)
	a, err := NewWithDB(db, table, WithName("a"))
	require.NoError(t, err)
	a2, err := NewWithDB(db, table, WithName("a"))
	require.NoError(t, err)
	b, err := NewWithDB(db, table, WithName("b"))
	require.NoError(t, err)

	unlockA, err := a.Lock(context.Background())
	require.NoError(t, err)
	unlockB, err := b.Lock(context.Background())
	require.NoError(t, err)
	require.NoError(t, unlockB())

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = a2.Lock(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, unlockA())
}

func TestIntegration_LockRefusesPoolOfOne(t *testing.T) {
	dsn := startMySQL(t)
	db := openDB(t, dsn)
	d, err := NewWithDB(db, uniqueTable(t))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	_, err = d.Lock(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MaxOpenConns")
	// Not deadlocked: the pool still serves queries.
	_, err = d.Version()
	require.NoError(t, err)
}

// NewWithDB neither opens nor closes the caller's handle.
func TestIntegration_NewWithDBLeavesHandleOpen(t *testing.T) {
	dsn := startMySQL(t)
	db := openDB(t, dsn)
	d, err := NewWithDB(db, uniqueTable(t), WithName("app"))
	require.NoError(t, err)
	require.NoError(t, d.SetVersion("1.0.0"))
	require.NoError(t, d.Close())
	require.NoError(t, db.Ping(), "Driver.Close closed the caller's handle")
	d2, err := NewWithDB(db, d.table, WithName("app"))
	require.NoError(t, err)
	v, err := d2.Version()
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", v)
}
