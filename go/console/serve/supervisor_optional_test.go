package serve_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/serve"
)

// TestSupervisor_OptionalStartFailureLeavesTheRestRunning pins the
// adopter case: an auxiliary service that cannot bind must not take
// the core service down, and must not stop the core from starting
// when it comes first.
func TestSupervisor_OptionalStartFailureLeavesTheRestRunning(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	core := &fake{name: "core", trace: tr}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "aux", trace: tr, startErr: errBind, neverReady: true})
	reg.Register(core)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec))
	done := runInBackground(ctx, sup, []string{"aux", "core"},
		withOptional(enabledSet("aux", "core"), "aux"))

	waitFor(t, func() bool {
		return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
	})
	assert.True(t, core.Ready(), "the core service started after the optional one failed")
	assert.NotContains(t, tr.events(), "stop:core", "nothing stops the core before the run ends")

	failed := failedEvents(rec, "aux")
	require.Len(t, failed, 1)
	assert.True(t, failed[0].Optional, "the failed event marks the service optional")
	assert.Equal(t, "start", failed[0].Reason)
	assert.Equal(t, errBind.Error(), failed[0].Error)

	cancel()
	res := <-done
	assert.Equal(t, serve.OutcomeCleanStop, res.Outcome,
		"an optional failure is reported, not exited on")
	assert.Equal(t, 0, res.ExitCode())
	assert.Nil(t, res.Err)
	assert.ErrorIs(t, res.Failed["aux"], errBind)
	assert.Equal(t, []string{"aux", "core"}, res.Started)
	assert.Equal(t, []string{"core"}, res.Ready)

	aux := statusOf(t, res.Services, "aux")
	assert.Equal(t, serve.StateFailed, aux.State)
	assert.True(t, aux.Optional)
	assert.Equal(t, "start", aux.Reason)
	assert.Equal(t, errBind.Error(), aux.Error)
	assert.False(t, statusOf(t, res.Services, "core").Optional)
}

// TestSupervisor_RequiredStartFailureStillEndsTheRun pins today's
// behavior for a service that is not optional: its start failure
// stops what already started and ends the run with exit 1.
func TestSupervisor_RequiredStartFailureStillEndsTheRun(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "core", trace: tr})
	reg.Register(&fake{name: "aux", trace: tr, startErr: errBind, neverReady: true})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec))
	res := sup.Run(ctx, []string{"core", "aux"}, enabledSet("core", "aux"))
	require.NoError(t, ctx.Err(), "the start failure ends the run by itself")

	assert.Equal(t, serve.OutcomeStartFailed, res.Outcome)
	assert.Equal(t, 1, res.ExitCode())
	require.NotNil(t, res.Err)
	assert.Contains(t, res.Err.Message, "aux: "+errBind.Error())
	assert.NotContains(t, res.Err.Message, "optional")
	assert.Equal(t, []string{"stop:aux", "stop:core"}, onlyPrefixed(tr.events(), "stop:"))
	assert.NotContains(t, rec.topics(), "kit.serve.supervisor.ready_reported")
	failed := failedEvents(rec, "aux")
	require.Len(t, failed, 1)
	assert.False(t, failed[0].Optional)
}

// TestSupervisor_OptionalEarlyReturnIsAnOptionalStartFailure pins the
// third start-failure shape: Start returning nil before ready.
func TestSupervisor_OptionalEarlyReturnIsAnOptionalStartFailure(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	core := &fake{name: "core", trace: tr}
	reg := serve.NewRegistry()
	reg.Register(core)
	reg.Register(&earlyFake{name: "aux"})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec))
	done := runInBackground(ctx, sup, []string{"core", "aux"},
		withOptional(enabledSet("core", "aux"), "aux"))

	// The aggregate readiness event comes only once the start sequence
	// is over, the optional failure handled.
	waitFor(t, func() bool {
		return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
	})
	assert.True(t, core.Ready())
	cancel()
	res := <-done

	assert.Equal(t, 0, res.ExitCode())
	aux := statusOf(t, res.Services, "aux")
	assert.Equal(t, serve.StateFailed, aux.State)
	assert.Equal(t, "start", aux.Reason)
	assert.Contains(t, aux.Error, "returned before reporting ready")
}

// TestSupervisor_OptionalRuntimeCrashIsIsolatedUnderEitherPolicy pins
// that an optional service crashing after ready never brings the run
// down and never raises the exit code, whatever failure_policy says;
// the policy governs the required services.
//
// A witness, another optional service, fails only once the crash has
// been reported. The supervisor handles one exit at a time, so the
// witness's own failed event proves it carried on past the crash
// rather than shutting down.
func TestSupervisor_OptionalRuntimeCrashIsIsolatedUnderEitherPolicy(t *testing.T) {
	t.Parallel()
	for _, policy := range []serve.FailurePolicy{serve.FailFast, serve.Isolate} {
		t.Run(string(policy), func(t *testing.T) {
			t.Parallel()
			tr := &trace{}
			rec := &recorder{}
			core := &fake{name: "core", trace: tr}
			witness := newCueFake("witness", tr, 0)
			witness.fail = true
			reg := serve.NewRegistry()
			reg.Register(core)
			reg.Register(&fake{name: "aux", trace: tr, startErr: errors.New("boom"), failAfter: 20 * time.Millisecond})
			reg.Register(witness)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sup := serve.NewSupervisor(reg, serve.SupervisorConfig{FailurePolicy: policy}, serve.WithPublisher(rec))
			done := runInBackground(ctx, sup, []string{"core", "aux", "witness"},
				withOptional(enabledSet("core", "aux", "witness"), "aux", "witness"))

			waitFor(t, func() bool { return len(failedEvents(rec, "aux")) == 1 })
			close(witness.cue)
			waitFor(t, func() bool { return len(failedEvents(rec, "witness")) == 1 })
			assert.True(t, core.Ready(), "the core keeps serving")
			assert.NotContains(t, tr.events(), "stop:core")

			cancel()
			res := <-done
			assert.Equal(t, serve.OutcomeCleanStop, res.Outcome)
			assert.Equal(t, 0, res.ExitCode())
			aux := statusOf(t, res.Services, "aux")
			assert.Equal(t, serve.StateFailed, aux.State)
			assert.Equal(t, "runtime", aux.Reason)
		})
	}
}

// TestSupervisor_RequiredRuntimeCrashKeepsItsPolicy pins that a
// required service's crash is still governed by failure_policy when an
// optional sibling is in the run.
func TestSupervisor_RequiredRuntimeCrashKeepsItsPolicy(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "aux", trace: tr})
	reg.Register(&fake{name: "core", trace: tr, startErr: errors.New("boom"), failAfter: 20 * time.Millisecond})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{FailurePolicy: serve.FailFast})
	res := sup.Run(ctx, []string{"aux", "core"}, withOptional(enabledSet("aux", "core"), "aux"))
	require.NoError(t, ctx.Err(), "fail-fast still ends the run on a required crash")

	assert.Equal(t, serve.OutcomeRuntimeCrash, res.Outcome)
	assert.Equal(t, 1, res.ExitCode())
	assert.Contains(t, tr.events(), "stop:aux", "fail-fast stops the optional sibling too")
}

// TestSupervisor_EveryServiceFailingIsAStartFailure pins that a run
// with nothing serving is down, not degraded: when every started
// service fails to start, optional or not, the run is a start failure.
// The selector form naming one optional service is this case.
func TestSupervisor_EveryServiceFailingIsAStartFailure(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "aux", trace: tr, startErr: errBind, neverReady: true})
	reg.Register(&earlyFake{name: "extra"})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec))
	res := sup.Run(ctx, []string{"aux", "extra"},
		withOptional(enabledSet("aux", "extra"), "aux", "extra"))
	require.NoError(t, ctx.Err(), "a run with nothing serving ends by itself")

	assert.Equal(t, serve.OutcomeStartFailed, res.Outcome)
	assert.Equal(t, 1, res.ExitCode())
	require.NotNil(t, res.Err)
	assert.Contains(t, res.Err.Message, "aux (optional): "+errBind.Error())
	assert.NotContains(t, rec.topics(), "kit.serve.supervisor.ready_reported")
}

// TestSupervisor_RequiredFailureMessageMarksOptionalSiblings pins the
// refusal text when a required failure ends a run in which an optional
// service had already failed: both are named, and the optional one is
// marked so an operator reads which failure the exit is about.
func TestSupervisor_RequiredFailureMessageMarksOptionalSiblings(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "aux", trace: tr, startErr: errBind, neverReady: true})
	reg.Register(&fake{name: "core", trace: tr, startErr: errors.New("boom"), neverReady: true})

	res := serve.NewSupervisor(reg, serve.SupervisorConfig{}).
		Run(t.Context(), []string{"aux", "core"}, withOptional(enabledSet("aux", "core"), "aux"))

	assert.Equal(t, serve.OutcomeStartFailed, res.Outcome)
	require.NotNil(t, res.Err)
	assert.Contains(t, res.Err.Message, "aux (optional): "+errBind.Error())
	assert.Contains(t, res.Err.Message, "core: boom")
}

// TestSupervisor_RunViewReportsOptionalFailureLive pins the live half
// of the status surface: a service holding the run view reads the
// optional sibling's failure, with its reason, while the run goes on.
func TestSupervisor_RunViewReportsOptionalFailureLive(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	watcher := viewFake{fake: &fake{name: "core", trace: tr}, got: make(chan serve.RunView, 1)}
	reg := serve.NewRegistry()
	reg.Register(watcher)
	reg.Register(&fake{name: "aux", trace: tr, startErr: errBind, neverReady: true})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{})
	done := runInBackground(ctx, sup, []string{"core", "aux"},
		withOptional(enabledSet("core", "aux"), "aux"))

	view := <-watcher.got
	status, ok := view.(serve.RunStatus)
	require.True(t, ok, "the supervisor's run view reports detail, not only state")
	waitFor(t, func() bool { return view.State("aux") == serve.StateFailed })

	aux := statusOf(t, status.Services(), "aux")
	assert.True(t, aux.Optional)
	assert.Equal(t, "start", aux.Reason)
	assert.Equal(t, serve.StateReady, statusOf(t, status.Services(), "core").State)

	raw, err := json.Marshal(aux)
	require.NoError(t, err)
	var round map[string]any
	require.NoError(t, json.Unmarshal(raw, &round))
	assert.Equal(t, "aux", round["service"])
	assert.Equal(t, "failed", round["state"])
	assert.Equal(t, true, round["optional"])
	assert.Equal(t, "start", round["reason"])
	assert.Equal(t, errBind.Error(), round["error"])
	assert.Contains(t, round, "elapsed_ms")

	cancel()
	<-done
}

// TestSupervisor_OptionalMarksEveryEventAboutTheService pins that the
// optional flag rides every event about the service, not only the
// failure, and never an event about a required one.
func TestSupervisor_OptionalMarksEveryEventAboutTheService(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "core", trace: tr})
	reg.Register(&fake{name: "aux", trace: tr})

	ctx, cancel := context.WithCancel(t.Context())
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec))
	done := runInBackground(ctx, sup, []string{"core", "aux"},
		withOptional(enabledSet("core", "aux"), "aux"))
	waitFor(t, func() bool {
		return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
	})
	cancel()
	<-done

	seen := 0
	for _, e := range rec.snapshot() {
		p, ok := e.Payload.(serve.EventPayload)
		require.True(t, ok)
		switch p.Service {
		case "aux":
			seen++
			assert.True(t, p.Optional, "%s about aux", e.Topic)
		default:
			assert.False(t, p.Optional, "%s about %q", e.Topic, p.Service)
		}
	}
	assert.Equal(t, 3, seen, "started, ready_reported, stopped")
}
