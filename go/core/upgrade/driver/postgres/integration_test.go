package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"hop.top/kit/go/core/upgrade"
)

// One Postgres 16 container serves every integration test of the
// package; each test uses its own table so they stay independent. Under
// -short these tests skip (the default unit target); otherwise a Docker
// that cannot start the container fails the test: make test-go-integration
// must not pass without having run them.
var (
	pgOnce sync.Once
	pgCtr  *tcpg.PostgresContainer
	pgDSN  string
	pgErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if pgCtr != nil {
		_ = testcontainers.TerminateContainer(pgCtr)
	}
	os.Exit(code)
}

func dsn(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	pgOnce.Do(func() {
		ctx := context.Background()
		pgCtr, pgErr = tcpg.Run(ctx, "postgres:16-alpine",
			tcpg.WithDatabase("testdb"),
			tcpg.WithUsername("test"),
			tcpg.WithPassword("test"),
			tcpg.BasicWaitStrategies(),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(120*time.Second)),
		)
		if pgErr != nil {
			return
		}
		pgDSN, pgErr = pgCtr.ConnectionString(ctx, "sslmode=disable")
	})
	if pgErr != nil {
		t.Fatalf("postgres container: %v (docker is required for integration tests)", pgErr)
	}
	return pgDSN
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())
	return db
}

var (
	tableSeq    atomic.Int32
	tableByTest sync.Map // *testing.T -> table name
)

// tableFor derives a valid table name, unique per test and per run (a
// repeated -count run must not see the previous run's rows).
func tableFor(t *testing.T) string {
	t.Helper()
	if n, ok := tableByTest.Load(t); ok {
		return n.(string)
	}
	n := strings.ToLower(strings.NewReplacer("/", "_", "-", "_").Replace(t.Name()))
	n = strings.TrimPrefix(n, "testintegration_")
	if len(n) > 40 {
		n = n[:40]
	}
	n = fmt.Sprintf("v_%s_%d", n, tableSeq.Add(1))
	tableByTest.Store(t, n)
	return n
}

func newDriver(t *testing.T, db *sql.DB, name string) *Driver {
	t.Helper()
	d, err := New(db, tableFor(t), WithName(name))
	require.NoError(t, err)
	return d
}

func lockWithin(t *testing.T, d *Driver, wait time.Duration) func() error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	unlock, err := d.Lock(ctx)
	require.NoError(t, err, "a second session must acquire the lock at once")
	return unlock
}

func TestIntegration_VersionRoundtripAndUpsert(t *testing.T) {
	db := openDB(t)
	d := newDriver(t, db, "myschema")
	other := newDriver(t, db, "other")

	v, err := d.Version()
	require.NoError(t, err)
	assert.Equal(t, "", v)

	require.NoError(t, d.SetVersion("1.0.0"))
	require.NoError(t, other.SetVersion("3.0.0"))
	require.NoError(t, d.SetVersion("2.0.0"))

	v, err = d.Version()
	require.NoError(t, err)
	assert.Equal(t, "2.0.0", v)
	v, err = other.Version()
	require.NoError(t, err)
	assert.Equal(t, "3.0.0", v)

	var rows int
	require.NoError(t, db.QueryRow(
		fmt.Sprintf(`SELECT count(*) FROM %s WHERE schema_name = 'myschema'`, d.table)).Scan(&rows))
	assert.Equal(t, 1, rows, "upsert keeps one row per schema")

	// A second New over the same table keeps the data.
	again := newDriver(t, db, "myschema")
	v, err = again.Version()
	require.NoError(t, err)
	assert.Equal(t, "2.0.0", v)
}

func TestIntegration_ConcurrentNew(t *testing.T) {
	var dbs []*sql.DB
	for i := 0; i < 6; i++ {
		dbs = append(dbs, openDB(t))
	}
	var wg sync.WaitGroup
	errs := make([]error, len(dbs))
	for i, db := range dbs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = New(db, tableFor(t), WithName("app"))
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
}

// Two Migrators, each over its own pool, run the same migrations against
// one database at once: every migration applies exactly once.
func TestIntegration_RacingMigratorsApplyOnce(t *testing.T) {
	const schema = "app"
	var counts [3]atomic.Int32
	reg := upgrade.NewRegistry()
	for i, v := range []string{"1.1.0", "1.2.0", "1.3.0"} {
		reg.Register(upgrade.Migration{Version: v, Schema: schema,
			Up: func(context.Context) error {
				counts[i].Add(1)
				time.Sleep(40 * time.Millisecond) // widen the race window
				return nil
			}})
	}

	const racers = 2
	ms := make([]*upgrade.Migrator, racers)
	for i := range ms {
		db := openDB(t)
		ms[i] = upgrade.NewMigrator("racetest", "1.3.0", upgrade.WithRegistry(reg),
			upgrade.WithDataDir(t.TempDir()))
		ms[i].AddDriver(newDriver(t, db, schema))
	}

	start := make(chan struct{})
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := range ms {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = ms[i].Run(context.Background())
		}()
	}
	close(start)
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	for i := range counts {
		assert.Equal(t, int32(1), counts[i].Load(), "migration %d applied once", i)
	}
	d := newDriver(t, openDB(t), schema)
	v, err := d.Version()
	require.NoError(t, err)
	assert.Equal(t, "1.3.0", v)
}

// Lock blocks while another session holds it and is interruptible
// through the context; once released a waiter acquires it.
func TestIntegration_LockBlocksAndIsInterruptible(t *testing.T) {
	a := newDriver(t, openDB(t), "app")
	b := newDriver(t, openDB(t), "app")

	unlockA, err := a.Lock(context.Background())
	require.NoError(t, err)

	// Canceled while waiting: returns the context error, holds nothing.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, err = b.Lock(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled),
		"error %v should wrap the context error", err)
	assert.GreaterOrEqual(t, time.Since(began), 250*time.Millisecond, "Lock blocked until the deadline")

	// A waiter gets it as soon as A releases.
	got := make(chan error, 1)
	go func() {
		un, err := b.Lock(context.Background())
		if err == nil {
			err = un()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("waiter returned while the lock was held: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, unlockA())
	select {
	case err := <-got:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not acquire after unlock")
	}
}

// A failed run releases the lock: a second session acquires at once and
// the run returned its connection.
func TestIntegration_LockReleasedAfterFailedRun(t *testing.T) {
	db := openDB(t)
	d := newDriver(t, db, "app")
	reg := upgrade.NewRegistry()
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "app",
		Up: func(context.Context) error { return errors.New("boom") }})
	m := upgrade.NewMigrator("failrun", "1.1.0", upgrade.WithRegistry(reg),
		upgrade.WithDataDir(t.TempDir()))
	m.AddDriver(d)
	require.Error(t, m.Run(context.Background()))

	second := newDriver(t, openDB(t), "app")
	require.NoError(t, lockWithin(t, second, 2*time.Second)())
	assert.Equal(t, 0, db.Stats().InUse, "run returned its connections")
}

// A run canceled mid-way releases the lock too: unlock runs detached
// from the canceled context.
func TestIntegration_LockReleasedAfterCanceledRun(t *testing.T) {
	db := openDB(t)
	d := newDriver(t, db, "app")
	ctx, cancel := context.WithCancel(context.Background())
	reg := upgrade.NewRegistry()
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "app",
		Up: func(context.Context) error { cancel(); return nil }})
	reg.Register(upgrade.Migration{Version: "1.2.0", Schema: "app",
		Up: func(context.Context) error { return nil }})
	m := upgrade.NewMigrator("cancelrun", "1.2.0", upgrade.WithRegistry(reg),
		upgrade.WithDataDir(t.TempDir()))
	m.AddDriver(d)
	require.ErrorIs(t, m.Run(ctx), context.Canceled)

	second := newDriver(t, openDB(t), "app")
	require.NoError(t, lockWithin(t, second, 2*time.Second)())
	assert.Equal(t, 0, db.Stats().InUse, "run returned its connections")
}

func TestIntegration_PoolOfOneIsRefused(t *testing.T) {
	db := openDB(t)
	d := newDriver(t, db, "app")
	db.SetMaxOpenConns(1)

	done := make(chan error, 1)
	go func() {
		_, err := d.Lock(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least two connections")
	case <-time.After(5 * time.Second):
		t.Fatal("Lock on a pool of one deadlocked")
	}

	// End to end: the Migrator reports it as a lock error.
	m := upgrade.NewMigrator("poolone", "1.0.0", upgrade.WithRegistry(upgrade.NewRegistry()),
		upgrade.WithDataDir(t.TempDir()))
	m.AddDriver(d)
	err := m.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lock:")
}

// Probe: returning driver.ErrBadConn from conn.Raw really ends the
// server session, and with it the session-level advisory lock.
func TestIntegration_DiscardEndsSession(t *testing.T) {
	db := openDB(t)
	probe := openDB(t)
	ctx := context.Background()

	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	var pid int
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid))
	key := lockKey(tableFor(t), "app")
	_, err = conn.ExecContext(ctx, sqlLock, key)
	require.NoError(t, err)

	var free bool
	require.NoError(t, probe.QueryRow(`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&free))
	require.False(t, free, "lock is held by the session")

	discard(conn)

	require.Eventually(t, func() bool {
		var alive bool
		require.NoError(t, probe.QueryRow(
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1)`, pid).Scan(&alive))
		return !alive
	}, 5*time.Second, 50*time.Millisecond, "session %d still alive after discard", pid)
	assert.Equal(t, 0, db.Stats().OpenConnections, "pool dropped the connection")
	require.Eventually(t, func() bool {
		var got bool
		require.NoError(t, probe.QueryRow(`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&got))
		return got
	}, 5*time.Second, 50*time.Millisecond, "lock freed with the session")
}

// A failing unlock statement (session killed under the lock) returns an
// error, drops the connection and frees the lock for the next session.
func TestIntegration_FailedUnlockDiscardsConnection(t *testing.T) {
	db := openDB(t)
	admin := openDB(t)
	d := newDriver(t, db, "app")

	unlock, err := d.Lock(context.Background())
	require.NoError(t, err)

	var killed int
	require.NoError(t, admin.QueryRow(`SELECT count(pg_terminate_backend(pid)) FROM pg_locks
		WHERE locktype = 'advisory' AND granted
		AND ((classid::bigint << 32) | objid::bigint) = hashtextextended($1, 0)`,
		lockKey(d.table, d.name)).Scan(&killed))
	require.Equal(t, 1, killed, "one session holds the lock")

	first := unlock()
	require.Error(t, first)
	assert.Equal(t, first, unlock(), "unlock runs once and repeats its result")
	require.Eventually(t, func() bool { return db.Stats().OpenConnections == 0 },
		5*time.Second, 50*time.Millisecond, "the dead connection left the pool")
	require.NoError(t, lockWithin(t, newDriver(t, openDB(t), "app"), 2*time.Second)())
}

// Manual rollback on this driver: no backup exists, so RollbackLatest
// cannot restore and says so; the stored version is untouched.
func TestIntegration_ManualRollbackLatestHasNoBackup(t *testing.T) {
	d := newDriver(t, openDB(t), "app")
	require.NoError(t, d.SetVersion("1.0.0"))
	reg := upgrade.NewRegistry()
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "app",
		Up: func(context.Context) error { return nil }})
	reg.Register(upgrade.Migration{Version: "1.2.0", Schema: "app",
		Up: func(context.Context) error { return errors.New("boom") }})
	m := upgrade.NewMigrator("manual", "1.2.0", upgrade.WithRegistry(reg),
		upgrade.WithDataDir(t.TempDir()), upgrade.WithManualRollback())
	m.AddDriver(d)

	require.Error(t, m.Run(context.Background()))
	err := m.RollbackLatest()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no backups found", "nothing to restore from")
	assert.ErrorIs(t, err, upgrade.ErrNotRestored)
	assert.ErrorIs(t, err, upgrade.ErrBackupUnsupported)

	v, verr := d.Version()
	require.NoError(t, verr)
	assert.Equal(t, "1.1.0", v, "nothing was restored")
	// The lock was released by RollbackLatest too.
	require.NoError(t, lockWithin(t, newDriver(t, openDB(t), "app"), 2*time.Second)())
}

// A failed migration with no Down cannot be restored by this driver:
// the run reports ErrNotRestored and the table holds the last step
// still applied.
func TestIntegration_RunWithoutDownIsNotRestored(t *testing.T) {
	d := newDriver(t, openDB(t), "app")
	require.NoError(t, d.SetVersion("1.0.0"))
	reg := upgrade.NewRegistry()
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "app",
		Up: func(context.Context) error { return nil }})
	reg.Register(upgrade.Migration{Version: "1.2.0", Schema: "app",
		Up: func(context.Context) error { return errors.New("boom") }})
	m := upgrade.NewMigrator("pgtest", "1.2.0", upgrade.WithRegistry(reg),
		upgrade.WithDataDir(t.TempDir()))
	m.AddDriver(d)

	err := m.Run(context.Background())
	require.ErrorIs(t, err, upgrade.ErrNotRestored)
	require.ErrorIs(t, err, upgrade.ErrBackupUnsupported)

	v, err := d.Version()
	require.NoError(t, err)
	assert.Equal(t, "1.1.0", v)
}

// The caller owns the handle: the driver has no Close, and a full run
// plus lock cycle leaves the handle open and usable.
func TestIntegration_DriverNeverClosesCallersDB(t *testing.T) {
	_, hasClose := reflect.TypeOf(&Driver{}).MethodByName("Close")
	assert.False(t, hasClose, "Driver must not expose Close")

	db := openDB(t)
	d := newDriver(t, db, "app")
	reg := upgrade.NewRegistry()
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "app",
		Up: func(context.Context) error { return nil }})
	m := upgrade.NewMigrator("keepopen", "1.1.0", upgrade.WithRegistry(reg),
		upgrade.WithDataDir(t.TempDir()))
	m.AddDriver(d)
	require.NoError(t, m.Run(context.Background()))

	require.NoError(t, db.PingContext(context.Background()))
	var one int
	require.NoError(t, db.QueryRow(`SELECT 1`).Scan(&one))
}
