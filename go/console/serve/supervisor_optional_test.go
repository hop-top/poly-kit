package serve_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/serve"
)

// errBind stands in for an auxiliary listener whose port is taken.
var errBind = errors.New("bind: address already in use")

// withOptional returns configs with names marked optional.
func withOptional(configs map[string]serve.Config, names ...string) map[string]serve.Config {
	for _, n := range names {
		c := configs[n]
		c.Optional = true
		configs[n] = c
	}
	return configs
}

// failedEvents returns the payloads of every service.failed event for
// name, in publish order.
func failedEvents(rec *recorder, name string) []serve.EventPayload {
	var out []serve.EventPayload
	for _, e := range rec.snapshot() {
		if e.Topic != "kit.serve.service.failed" {
			continue
		}
		if p, ok := e.Payload.(serve.EventPayload); ok && p.Service == name {
			out = append(out, p)
		}
	}
	return out
}

func statusOf(t *testing.T, all []serve.ServiceStatus, name string) serve.ServiceStatus {
	t.Helper()
	for _, s := range all {
		if s.Service == name {
			return s
		}
	}
	t.Fatalf("no status for %q in %+v", name, all)
	return serve.ServiceStatus{}
}

// earlyFake returns nil from Start without ever reporting ready.
type earlyFake struct{ name string }

func (e *earlyFake) Name() string                        { return e.name }
func (e *earlyFake) Start(context.Context, func()) error { return nil }
func (e *earlyFake) Ready() bool                         { return false }
func (e *earlyFake) Stop(context.Context) error          { return nil }

// stuckFake never reports ready, and its Start returns an error only
// once Stop releases it: the shape of a listener stuck in acquisition
// that Stop tears down. It counts its Stop calls.
type stuckFake struct {
	name    string
	release chan struct{}

	mu    sync.Mutex
	stops int
}

func (s *stuckFake) Name() string { return s.name }

func (s *stuckFake) Start(context.Context, func()) error {
	<-s.release
	return errors.New("listener closed")
}

func (s *stuckFake) Ready() bool { return false }

func (s *stuckFake) Stop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stops++
	if s.stops == 1 {
		close(s.release)
	}
	return nil
}

func (s *stuckFake) stopCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stops
}

// delayedReady holds a fake's Start back by delay.
type delayedReady struct {
	*fake
	delay time.Duration
}

func (d delayedReady) Start(ctx context.Context, report func()) error {
	select {
	case <-time.After(d.delay):
	case <-ctx.Done():
		return nil
	}
	return d.fake.Start(ctx, report)
}

// viewFake captures the run view its Start receives.
type viewFake struct {
	*fake
	got chan serve.RunView
}

func (v viewFake) Start(ctx context.Context, report func()) error {
	view, _ := serve.RunViewFrom(ctx)
	v.got <- view
	return v.fake.Start(ctx, report)
}

// runInBackground starts sup.Run and returns the channel its Result
// arrives on.
func runInBackground(ctx context.Context, sup *serve.Supervisor, selected []string, configs map[string]serve.Config) <-chan serve.Result {
	done := make(chan serve.Result, 1)
	go func() { done <- sup.Run(ctx, selected, configs) }()
	return done
}

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

// TestSupervisor_OptionalReadyTimeoutIsStoppedOnce pins that an
// optional service that misses its readiness budget is stopped there
// and then, so it cannot start serving later while reported failed;
// that the error its Start returns once stopped is neither a second
// failure nor a fail-fast trip, whether it arrives while a later
// service starts or while the run goes on; and that the final shutdown
// does not stop it again.
func TestSupervisor_OptionalReadyTimeoutIsStoppedOnce(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		order []string
		// coreDelay holds the core's ready report back, so the
		// stopped service's exit lands inside the core's start
		// window rather than after it.
		coreDelay time.Duration
	}{
		"exit during the next start": {order: []string{"aux", "core"}, coreDelay: 50 * time.Millisecond},
		"exit while running":         {order: []string{"core", "aux"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr := &trace{}
			rec := &recorder{}
			core := &fake{name: "core", trace: tr}
			stuck := &stuckFake{name: "aux", release: make(chan struct{})}
			reg := serve.NewRegistry()
			reg.Register(stuck)
			reg.Register(delayedReady{fake: core, delay: tc.coreDelay})

			configs := enabledSet("aux", "core")
			configs["aux"] = serve.Config{Enabled: true, Optional: true, ReadyTimeout: 30 * time.Millisecond}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sup := serve.NewSupervisor(reg, serve.SupervisorConfig{FailurePolicy: serve.FailFast},
				serve.WithPublisher(rec))
			done := runInBackground(ctx, sup, tc.order, configs)

			waitFor(t, func() bool {
				return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
			})
			assert.Equal(t, 1, stuck.stopCount(), "the timed-out service is stopped when it fails")

			// Its Start has returned an error by now; give the
			// supervisor the chance to (wrongly) treat that as a
			// runtime crash before checking the core is still up.
			time.Sleep(50 * time.Millisecond)
			assert.True(t, core.Ready(), "the stopped service's exit must not trip fail-fast")
			assert.NotContains(t, tr.events(), "stop:core")

			cancel()
			res := <-done
			assert.Equal(t, serve.OutcomeCleanStop, res.Outcome)
			assert.Equal(t, 1, stuck.stopCount(), "a service is stopped once per run")
			aux := statusOf(t, res.Services, "aux")
			assert.Equal(t, serve.StateFailed, aux.State)
			assert.Equal(t, "ready_timeout", aux.Reason)
			assert.Contains(t, aux.Error, "not ready within")
			require.Len(t, failedEvents(rec, "aux"), 1, "one failure, not a second one for the exit")
		})
	}
}

// TestSupervisor_OptionalEarlyReturnIsAnOptionalStartFailure pins the
// third start-failure shape: Start returning nil before ready.
func TestSupervisor_OptionalEarlyReturnIsAnOptionalStartFailure(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	core := &fake{name: "core", trace: tr}
	reg := serve.NewRegistry()
	reg.Register(core)
	reg.Register(&earlyFake{name: "aux"})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{})
	done := runInBackground(ctx, sup, []string{"core", "aux"},
		withOptional(enabledSet("core", "aux"), "aux"))

	waitFor(t, func() bool { return slices.Contains(tr.events(), "ready:core") })
	time.Sleep(20 * time.Millisecond)
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
func TestSupervisor_OptionalRuntimeCrashIsIsolatedUnderEitherPolicy(t *testing.T) {
	t.Parallel()
	for _, policy := range []serve.FailurePolicy{serve.FailFast, serve.Isolate} {
		t.Run(string(policy), func(t *testing.T) {
			t.Parallel()
			tr := &trace{}
			core := &fake{name: "core", trace: tr}
			reg := serve.NewRegistry()
			reg.Register(core)
			reg.Register(&fake{name: "aux", trace: tr, startErr: errors.New("boom"), failAfter: 20 * time.Millisecond})

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sup := serve.NewSupervisor(reg, serve.SupervisorConfig{FailurePolicy: policy})
			done := runInBackground(ctx, sup, []string{"core", "aux"},
				withOptional(enabledSet("core", "aux"), "aux"))

			waitFor(t, func() bool { return slices.Contains(tr.events(), "fail:aux") })
			time.Sleep(20 * time.Millisecond)
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
