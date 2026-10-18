package serve_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
)

// TestSupervisor_OptionalStopOverrunDoesNotHoldTheExit pins that an
// optional service whose Stop overruns its budget at shutdown is
// written off: its Start goroutine, still running, does not make the
// shutdown wait out the total budget, nor exit non-zero.
func TestSupervisor_OptionalStopOverrunDoesNotHoldTheExit(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "core", trace: tr})
	reg.Register(newHungFake(t, "aux", false))

	configs := enabledSet("core", "aux")
	configs["aux"] = serve.Config{Enabled: true, Optional: true, StopTimeout: 20 * time.Millisecond}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{ShutdownTimeout: 2 * time.Second},
		serve.WithPublisher(rec))
	done := runInBackground(ctx, sup, []string{"core", "aux"}, configs)
	waitFor(t, func() bool {
		return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
	})

	cancel()
	began := time.Now()
	res := <-done
	assert.Less(t, time.Since(began), time.Second, "the written-off goroutine is not waited for")
	assert.Equal(t, serve.OutcomeCleanStop, res.Outcome)
	assert.Equal(t, 0, res.ExitCode())
	aux := statusOf(t, res.Services, "aux")
	assert.Equal(t, serve.StateFailed, aux.State)
	assert.Equal(t, "stop_timeout", aux.Reason)
}

// TestSupervisor_RequiredGoroutineStillHoldsTheShutdownBudget pins
// today's rule for a required service: one whose Start never returns
// exhausts the shutdown budget, and the run says so.
func TestSupervisor_RequiredGoroutineStillHoldsTheShutdownBudget(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	reg := serve.NewRegistry()
	reg.Register(newHungFake(t, "core", false))

	configs := enabledSet("core")
	configs["core"] = serve.Config{Enabled: true, StopTimeout: 20 * time.Millisecond}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{ShutdownTimeout: 50 * time.Millisecond},
		serve.WithPublisher(rec))
	done := runInBackground(ctx, sup, []string{"core"}, configs)
	waitFor(t, func() bool {
		return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
	})
	cancel()
	res := <-done
	assert.Equal(t, 1, res.ExitCode())
	assert.Equal(t, serve.OutcomeRuntimeCrash, res.Outcome, "the stop overrun is the first failure")
}

// TestSupervisor_OptionalStopErrorDoesNotRaiseTheExit pins that an
// optional service's Stop returning an error is reported and recorded
// but is not an outcome of the run.
func TestSupervisor_OptionalStopErrorDoesNotRaiseTheExit(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "core", trace: tr})
	reg.Register(&fake{name: "aux", trace: tr, stopErr: errors.New("unlink: busy")})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runInBackground(ctx, serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec)),
		[]string{"core", "aux"}, withOptional(enabledSet("core", "aux"), "aux"))
	waitFor(t, func() bool {
		return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
	})
	cancel()
	res := <-done
	assert.Equal(t, 0, res.ExitCode())
	aux := statusOf(t, res.Services, "aux")
	assert.Equal(t, serve.StateFailed, aux.State)
	assert.Equal(t, "stop", aux.Reason)
	require.Len(t, failedEvents(rec, "aux"), 1)
}

// TestSupervisor_OptionalStopTimeoutExitsZero pins that an optional
// service's Stop overrunning its own budget, with its Start returning
// on cancellation, is reported and exits 0.
func TestSupervisor_OptionalStopTimeoutExitsZero(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	abandoned := make(chan struct{})
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "core", trace: tr})
	reg.Register(&fake{name: "aux", trace: tr, stopDelay: 30 * time.Second, abandoned: abandoned})

	configs := enabledSet("core", "aux")
	configs["aux"] = serve.Config{Enabled: true, Optional: true, StopTimeout: 20 * time.Millisecond}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runInBackground(ctx, serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec)),
		[]string{"core", "aux"}, configs)
	waitFor(t, func() bool {
		return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
	})
	cancel()
	res := <-done
	<-abandoned
	assert.Equal(t, serve.OutcomeCleanStop, res.Outcome)
	assert.Equal(t, 0, res.ExitCode())
	assert.Equal(t, "stop_timeout", statusOf(t, res.Services, "aux").Reason)
}

// TestSupervisor_OptionalTransientErrorDoesNotMakeARequiredFailureTransient
// pins that exit 6 is read only from the failures the exit is about: a
// required service's permanent failure exits 1 even when an optional
// sibling failed transiently.
func TestSupervisor_OptionalTransientErrorDoesNotMakeARequiredFailureTransient(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "aux", trace: tr, startErr: output.TransientError("upstream unavailable"), neverReady: true})
	reg.Register(&fake{name: "core", trace: tr, startErr: errors.New("config broken"), neverReady: true})

	res := serve.NewSupervisor(reg, serve.SupervisorConfig{}).
		Run(t.Context(), []string{"aux", "core"}, withOptional(enabledSet("aux", "core"), "aux"))
	require.NotNil(t, res.Err)
	assert.Equal(t, 1, res.Err.ExitCode)
	assert.Equal(t, output.CodeGeneric, res.Err.Code)

	// When every failure is optional, they are what the exit is about.
	reg2 := serve.NewRegistry()
	reg2.Register(&fake{name: "aux", trace: tr, startErr: output.TransientError("upstream unavailable"), neverReady: true})
	res = serve.NewSupervisor(reg2, serve.SupervisorConfig{}).
		Run(t.Context(), []string{"aux"}, withOptional(enabledSet("aux"), "aux"))
	require.NotNil(t, res.Err)
	assert.Equal(t, output.ExitTransient, res.Err.ExitCode)
}

// TestSupervisor_OptionalGoroutineLeftBehindIsNotAShutdownTimeout pins
// that the shutdown budget running out on an optional service's Start
// goroutine leaves it behind without failing the run; only a required
// one's is the run's shutdown timeout.
func TestSupervisor_OptionalGoroutineLeftBehindIsNotAShutdownTimeout(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	lingering := &lingeringFake{name: "aux", release: make(chan struct{})}
	t.Cleanup(func() { close(lingering.release) })
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "core", trace: &trace{}})
	reg.Register(lingering)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{ShutdownTimeout: 50 * time.Millisecond},
		serve.WithPublisher(rec))
	done := runInBackground(ctx, sup, []string{"core", "aux"}, withOptional(enabledSet("core", "aux"), "aux"))
	waitFor(t, func() bool {
		return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
	})
	cancel()
	res := <-done
	assert.Equal(t, serve.OutcomeCleanStop, res.Outcome)
	assert.Equal(t, 0, res.ExitCode())
}
