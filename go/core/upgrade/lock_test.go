package upgrade

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gateDriver is a version-storing driver whose lock is a one-slot channel,
// shared by every Migrator that adds it, like a database advisory lock.
// Lock signals waiting before it blocks, so a test knows a second holder
// is in the queue without sleeping.
type gateDriver struct {
	mu       sync.Mutex
	version  string
	slot     chan struct{}
	waiting  chan struct{}
	versions atomic.Int32 // Version reads
}

func newGateDriver() *gateDriver {
	return &gateDriver{slot: make(chan struct{}, 1), waiting: make(chan struct{}, 4)}
}

func (d *gateDriver) Name() string        { return "gate" }
func (d *gateDriver) StoresVersion() bool { return true }
func (d *gateDriver) Backup(string) error { return nil }
func (d *gateDriver) Restore(string) error {
	return nil
}

func (d *gateDriver) Version() (string, error) {
	d.versions.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.version, nil
}

func (d *gateDriver) SetVersion(v string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.version = v
	return nil
}

func (d *gateDriver) Lock(ctx context.Context) (func() error, error) {
	select {
	case d.slot <- struct{}{}:
	default:
		d.waiting <- struct{}{}
		select {
		case d.slot <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return func() error { <-d.slot; return nil }, nil
}

// gateRun is a Migrator over d with one migration whose Up counts itself
// and, when hold is set, tells entered and waits for release.
type gateRun struct {
	m    *Migrator
	ups  *atomic.Int32
	err  chan error
	done func()
}

func newGateRun(t *testing.T, d *gateDriver, hold bool, entered chan<- struct{}, release <-chan struct{}) *gateRun {
	t.Helper()
	reg := NewRegistry()
	ups := new(atomic.Int32)
	reg.Register(Migration{Version: "1.0.0", Schema: "gate", Up: func(context.Context) error {
		ups.Add(1)
		if hold {
			entered <- struct{}{}
			<-release
		}
		return nil
	}})
	m := NewMigrator("locktool", "1.0.0", WithRegistry(reg), WithDataDir(t.TempDir()))
	m.AddDriver(d)
	return &gateRun{m: m, ups: ups, err: make(chan error, 1)}
}

func (r *gateRun) start(ctx context.Context) { go func() { r.err <- r.m.Run(ctx) }() }

func within(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func waitErr(t *testing.T, r *gateRun) error {
	t.Helper()
	select {
	case err := <-r.err:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a run to finish")
		return nil
	}
}

func TestLockSecondMigratorWaitsThenFindsNothingPending(t *testing.T) {
	d := newGateDriver()
	entered, release := make(chan struct{}), make(chan struct{})
	a := newGateRun(t, d, true, entered, release)
	b := newGateRun(t, d, false, nil, nil)

	a.start(context.Background())
	within(t, "first run inside Up", entered)
	b.start(context.Background())
	within(t, "second run queued on the lock", d.waiting)

	if got := d.versions.Load(); got != 1 {
		t.Fatalf("Version reads while the lock is held = %d, want 1: the second run read before it held the lock", got)
	}
	close(release)
	if err := waitErr(t, a); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := waitErr(t, b); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if a.ups.Load() != 1 || b.ups.Load() != 0 {
		t.Fatalf("ups first=%d second=%d, want 1 and 0", a.ups.Load(), b.ups.Load())
	}
	if v, _ := d.Version(); v != "1.0.0" {
		t.Fatalf("stored version = %q", v)
	}
}

func TestLockCanceledWhileWaitingFailsBeforeAnythingElse(t *testing.T) {
	d := newGateDriver()
	entered, release := make(chan struct{}), make(chan struct{})
	a := newGateRun(t, d, true, entered, release)
	b := newGateRun(t, d, false, nil, nil)

	a.start(context.Background())
	within(t, "first run inside Up", entered)
	ctx, cancel := context.WithCancel(context.Background())
	b.start(ctx)
	within(t, "second run queued on the lock", d.waiting)
	cancel()

	err := waitErr(t, b)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("second run error = %v, want context.Canceled", err)
	}
	if want := "migrate gate: lock: context canceled"; err == nil || err.Error() != want {
		t.Fatalf("second run error = %v, want %q", err, want)
	}
	if b.ups.Load() != 0 || d.versions.Load() != 1 {
		t.Fatalf("second run did work: ups=%d version reads=%d", b.ups.Load(), d.versions.Load())
	}
	close(release)
	if err := waitErr(t, a); err != nil {
		t.Fatalf("first run: %v", err)
	}
}
