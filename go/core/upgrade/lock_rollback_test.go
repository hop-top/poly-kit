package upgrade

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// mutexDriver is a Locker over a real mutex, so a second holder blocks.
type mutexDriver struct {
	mu       sync.Mutex
	state    sync.Mutex // guards the fields below
	version  string
	restores int
	attempt  chan struct{} // signaled when Lock is entered
}

func (d *mutexDriver) Name() string { return "db" }
func (d *mutexDriver) Version() (string, error) {
	d.state.Lock()
	defer d.state.Unlock()
	return d.version, nil
}
func (d *mutexDriver) SetVersion(v string) error {
	d.state.Lock()
	defer d.state.Unlock()
	d.version = v
	return nil
}
func (d *mutexDriver) Backup(string) error { return nil }
func (d *mutexDriver) Restore(string) error {
	d.state.Lock()
	defer d.state.Unlock()
	d.restores++
	return nil
}
func (d *mutexDriver) restored() int {
	d.state.Lock()
	defer d.state.Unlock()
	return d.restores
}
func (d *mutexDriver) Lock(context.Context) (func() error, error) {
	d.attempt <- struct{}{}
	d.mu.Lock()
	return func() error { d.mu.Unlock(); return nil }, nil
}

// A RollbackLatest started while a run holds the driver's lock waits for
// it: nothing is restored until the run has unlocked.
func TestRollbackLatestWaitsForARunHoldingTheLock(t *testing.T) {
	dir := t.TempDir()
	d := &mutexDriver{version: "1.0.0", attempt: make(chan struct{}, 4)}
	ok := func(context.Context) error { return nil }

	// A manual run that fails leaves a backup to roll back to.
	seed := NewRegistry()
	seed.Register(Migration{Version: "1.1.0", Schema: "db", Up: ok})
	seed.Register(Migration{Version: "1.2.0", Schema: "db",
		Up: func(context.Context) error { return errors.New("boom") }})
	m0 := NewMigrator("lockrb", "1.2.0", WithRegistry(seed), WithDataDir(dir), WithManualRollback())
	m0.AddDriver(d)
	if err := m0.Run(context.Background()); err == nil {
		t.Fatal("seed run should fail")
	}
	<-d.attempt

	// A run that holds the lock inside an Up until released.
	held, release := make(chan struct{}), make(chan struct{})
	reg := NewRegistry()
	reg.Register(Migration{Version: "1.2.0", Schema: "db", Up: func(context.Context) error {
		close(held)
		<-release
		return nil
	}})
	runner := NewMigrator("lockrb", "1.2.0", WithRegistry(reg), WithDataDir(dir), WithManualRollback())
	runner.AddDriver(d)
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(context.Background()) }()
	<-held
	<-d.attempt // the run's Lock

	rollbacker := NewMigrator("lockrb", "1.2.0", WithRegistry(NewRegistry()), WithDataDir(dir), WithManualRollback())
	rollbacker.AddDriver(d)
	rbDone := make(chan error, 1)
	go func() { rbDone <- rollbacker.RollbackLatest() }()
	select {
	case <-d.attempt: // RollbackLatest is in Lock, blocked on the mutex
	case <-time.After(5 * time.Second):
		t.Fatal("RollbackLatest never asked for the lock")
	}
	if n := d.restored(); n != 0 {
		t.Fatalf("restored %d times while the run held the lock", n)
	}
	select {
	case err := <-rbDone:
		t.Fatalf("RollbackLatest returned while the run held the lock: %v", err)
	default:
	}

	close(release)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if err := <-rbDone; err != nil {
		t.Fatal(err)
	}
	if n := d.restored(); n != 1 {
		t.Fatalf("restored %d times, want 1", n)
	}
	if v, _ := d.Version(); v != "1.1.0" {
		t.Fatalf("version = %q, want the pre-run version 1.1.0 recorded after the run finished", v)
	}
}
