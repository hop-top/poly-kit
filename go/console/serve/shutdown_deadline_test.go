package serve_test

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/serve"
)

// slowStopStuckStart reports ready; its Stop takes stopFor and returns
// cleanly, but its Start never returns until the test ends.
type slowStopStuckStart struct {
	name    string
	stopFor time.Duration
	release chan struct{}
}

func (s *slowStopStuckStart) Name() string { return s.name }

func (s *slowStopStuckStart) Start(_ context.Context, report func()) error {
	report()
	<-s.release
	return nil
}

func (s *slowStopStuckStart) Ready() bool { return true }

func (s *slowStopStuckStart) Stop(context.Context) error {
	time.Sleep(s.stopFor)
	return nil
}

// TestSupervisor_ShutdownBudgetIsOneDeadline pins that
// services.shutdown_timeout bounds the whole shutdown once: the ordered
// stop and the wait for the Start goroutines share one deadline, so a
// stop that uses part of the budget leaves the wait only the rest.
func TestSupervisor_ShutdownBudgetIsOneDeadline(t *testing.T) {
	t.Parallel()
	core := &slowStopStuckStart{name: "core", stopFor: 300 * time.Millisecond, release: make(chan struct{})}
	t.Cleanup(func() { close(core.release) })
	reg := serve.NewRegistry()
	reg.Register(core)

	rec := &recorder{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{ShutdownTimeout: 500 * time.Millisecond},
		serve.WithPublisher(rec))
	done := runInBackground(ctx, sup, []string{"core"}, enabledSet("core"))
	waitFor(t, func() bool {
		return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
	})

	cancel()
	began := time.Now()
	res := <-done
	took := time.Since(began)
	assert.Less(t, took, 750*time.Millisecond, "one 500ms budget, not one per phase")
	assert.GreaterOrEqual(t, took, 450*time.Millisecond, "the stuck goroutine is waited for until the deadline")
	assert.Equal(t, serve.OutcomeShutdownTimeout, res.Outcome)
}

// cappedStop never reports ready; its Stop waits for its context or a
// long while, and records whether its context was canceled.
type cappedStop struct {
	name     string
	entered  chan struct{}
	once     sync.Once
	canceled atomic.Bool
	release  chan struct{}
}

func (c *cappedStop) Name() string { return c.name }

func (c *cappedStop) Start(context.Context, func()) error {
	<-c.release
	return nil
}

func (c *cappedStop) Ready() bool { return false }

func (c *cappedStop) Stop(ctx context.Context) error {
	c.once.Do(func() { close(c.entered) })
	select {
	case <-ctx.Done():
		c.canceled.Store(true)
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return nil
	}
}

// TestSupervisor_ReleaseIsCappedAtTheShutdownDeadline pins that a
// release whose own stop budget outlasts what remains of the shutdown
// budget is cut at the shutdown deadline: its Stop's context is
// canceled there, the run returns within the budget, and the service,
// optional, is left behind rather than failing the run.
func TestSupervisor_ReleaseIsCappedAtTheShutdownDeadline(t *testing.T) {
	t.Parallel()
	aux := &cappedStop{name: "aux", entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(aux.release) })
	reg := serve.NewRegistry()
	reg.Register(aux)
	reg.Register(&fake{name: "core", trace: &trace{}})

	configs := enabledSet("aux", "core")
	configs["aux"] = serve.Config{Enabled: true, Optional: true,
		ReadyTimeout: 20 * time.Millisecond, StopTimeout: 10 * time.Second}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{ShutdownTimeout: 300 * time.Millisecond})
	done := runInBackground(ctx, sup, []string{"aux", "core"}, configs)
	select {
	case <-aux.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the timed-out service was never stopped")
	}

	cancel()
	began := time.Now()
	res := <-done
	assert.Less(t, time.Since(began), 900*time.Millisecond, "the run returns within the shutdown budget")
	waitFor(t, func() bool { return aux.canceled.Load() })
	assert.Equal(t, 0, res.ExitCode(), "an optional release left behind is not the run failing")
	require.Equal(t, []string{"aux"}, res.Started)
}
