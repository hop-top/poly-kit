package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLock_FileLocation(t *testing.T) {
	t.Run("data dir", func(t *testing.T) {
		dir := t.TempDir()
		d := New("/x/app.db", WithName("appdb"), WithDataDir(dir))
		unlock, err := d.Lock(context.Background())
		require.NoError(t, err)
		defer unlock()
		assert.True(t, exists(join(dir, "appdb", ".lock")), "<dataDir>/<name>/.lock")
	})
	t.Run("xdg", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_DATA_HOME", xdg)
		d := New("/x/app.db", WithName("appdb"), WithTool("mytool"))
		unlock, err := d.Lock(context.Background())
		require.NoError(t, err)
		defer unlock()
		assert.True(t, exists(join(xdg, "hop", "mytool", "appdb", ".lock")))
	})
}

// Same rule in both modes; two drivers (as two processes would) on one
// file exclude each other. The wait is context-bound and the lock is
// reusable after unlock.
func TestLock_ExcludesAndHonoursContext(t *testing.T) {
	for name, handle := range map[string]bool{"path only": false, "with db": true} {
		t.Run(name, func(t *testing.T) {
			e := newRunEnv(t)
			mk := func() *Driver {
				if handle {
					return e.driver(WithDB(e.db))
				}
				return e.driver()
			}
			a, b := mk(), mk()
			unlockA, err := a.Lock(context.Background())
			require.NoError(t, err)

			// Canceled while waiting: context error, nothing held.
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			start := time.Now()
			unlock, err := b.Lock(ctx)
			cancel()
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Nil(t, unlock)
			assert.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond, "it did wait")
			assert.Less(t, time.Since(start), 2*time.Second)

			// Canceled by the caller mid-wait.
			ctx, cancel = context.WithCancel(context.Background())
			go func() { time.Sleep(80 * time.Millisecond); cancel() }()
			_, err = b.Lock(ctx)
			require.ErrorIs(t, err, context.Canceled)

			// Blocks until a releases, then acquires.
			got := make(chan error, 1)
			go func() {
				u, err := b.Lock(context.Background())
				if err == nil {
					err = u()
				}
				got <- err
			}()
			select {
			case err := <-got:
				t.Fatalf("Lock returned while held: %v", err)
			case <-time.After(150 * time.Millisecond):
			}
			require.NoError(t, unlockA())
			select {
			case err := <-got:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("Lock did not acquire after unlock")
			}

			// The canceled waits left nothing held.
			requireLockFree(t, a)
		})
	}
}

func TestLock_AlreadyCanceledHoldsNothing(t *testing.T) {
	d := New("/x/app.db", WithName("appdb"), WithDataDir(t.TempDir()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.Lock(ctx)
	require.ErrorIs(t, err, context.Canceled)
	requireLockFree(t, d)
}
