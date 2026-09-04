package serve_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/runtime/bus"
)

// enabledSet is the per-service config every supervisor test starts from.
func enabledSet(names ...string) map[string]serve.Config {
	out := make(map[string]serve.Config, len(names))
	for _, n := range names {
		out[n] = serve.Config{Enabled: true}
	}
	return out
}

// recorder captures published events for topic assertions.
type recorder struct {
	mu     sync.Mutex
	events []bus.Event
}

func (r *recorder) Publish(_ context.Context, e bus.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recorder) topics() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, string(e.Topic))
	}
	return out
}

func TestSupervisor_StartsInRegistrationOrderAndStopsInReverse(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "alpha", trace: tr})
	reg.Register(&fake{name: "beta", trace: tr})
	reg.Register(&fake{name: "gamma", trace: tr})

	ctx, cancel := context.WithCancel(t.Context())
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{})

	done := make(chan serve.Result, 1)
	go func() {
		done <- sup.Run(ctx, []string{"alpha", "beta", "gamma"}, enabledSet("alpha", "beta", "gamma"))
	}()

	waitFor(t, func() bool { return len(tr.events()) >= 6 })
	cancel()
	res := <-done

	assert.Equal(t, serve.OutcomeCleanStop, res.Outcome)
	assert.Equal(t, 0, res.ExitCode())
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, res.Started)
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, res.Ready)

	ev := tr.events()
	assert.Equal(t,
		[]string{"start:alpha", "ready:alpha", "start:beta", "ready:beta", "start:gamma", "ready:gamma"},
		ev[:6], "start is serial and in registration order")

	// Stop runs in the exact reverse of the order services started.
	assert.Equal(t,
		[]string{"stop:gamma", "stop:beta", "stop:alpha"},
		onlyPrefixed(ev, "stop:"))
}

func TestSupervisor_StartOrderFollowsDependsOn(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	// Registration order is api, db — but api depends on db, so db
	// must start first.
	reg.Register(dependentFake{&fake{name: "api", trace: tr, deps: []string{"db"}}})
	reg.Register(&fake{name: "db", trace: tr})

	ctx, cancel := context.WithCancel(t.Context())
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{})
	done := make(chan serve.Result, 1)
	go func() { done <- sup.Run(ctx, []string{"api", "db"}, enabledSet("api", "db")) }()

	waitFor(t, func() bool { return len(tr.events()) >= 4 })
	cancel()
	res := <-done

	assert.Equal(t, []string{"db", "api"}, res.Started)
	assert.Equal(t, []string{"stop:api", "stop:db"}, onlyPrefixed(tr.events(), "stop:"))
}

func TestSupervisor_DependencyCyclePanics(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(dependentFake{&fake{name: "a", trace: tr, deps: []string{"b"}}})
	reg.Register(dependentFake{&fake{name: "b", trace: tr, deps: []string{"a"}}})

	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{})
	assert.Panics(t, func() {
		sup.Run(t.Context(), []string{"a", "b"}, enabledSet("a", "b"))
	}, "a cycle is a wiring bug, discoverable on the first run")
}

func TestSupervisor_ReadinessAggregatesAcrossServices(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "one", trace: tr})
	reg.Register(&fake{name: "two", trace: tr})

	ctx, cancel := context.WithCancel(t.Context())
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec))
	done := make(chan serve.Result, 1)
	go func() { done <- sup.Run(ctx, []string{"one", "two"}, enabledSet("one", "two")) }()

	waitFor(t, func() bool {
		return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
	})
	cancel()
	res := <-done

	assert.Equal(t, []string{"one", "two"}, res.Ready)
	got := rec.topics()
	assert.Equal(t, 2, countOf(got, "kit.serve.service.ready_reported"))
	assert.Equal(t, 1, countOf(got, "kit.serve.supervisor.ready_reported"),
		"aggregate readiness is reported once, after every started service is ready")
	assert.Contains(t, got, "kit.serve.supervisor.stopped")
}

func TestSupervisor_ReadyTimeoutIsAStartFailure(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "slow", trace: tr, neverReady: true})

	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{})
	res := sup.Run(t.Context(), []string{"slow"}, map[string]serve.Config{
		"slow": {Enabled: true, ReadyTimeout: 40 * time.Millisecond},
	})

	assert.Equal(t, serve.OutcomeStartFailed, res.Outcome)
	assert.Equal(t, 1, res.ExitCode())
	require.NotNil(t, res.Err)
	assert.Equal(t, output.CodeGeneric, res.Err.Code)
	assert.Contains(t, res.Err.Message, "not ready within")
	assert.Contains(t, tr.events(), "stop:slow", "a service that failed to start is still stopped")
}

func TestSupervisor_FailFastStopsSiblings(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	boom := errors.New("boom")
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "healthy", trace: tr})
	reg.Register(&fake{name: "doomed", trace: tr, startErr: boom, failAfter: 20 * time.Millisecond})

	// Bounded so a fail-fast policy that does NOT stop its siblings
	// fails this test loudly instead of hanging until the package
	// timeout. The budget is far longer than the staged failure.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{FailurePolicy: serve.FailFast})
	res := sup.Run(ctx, []string{"healthy", "doomed"}, enabledSet("healthy", "doomed"))
	require.NoError(t, ctx.Err(),
		"fail-fast must end the run itself, not wait for the deadline")

	assert.Equal(t, serve.OutcomeRuntimeCrash, res.Outcome)
	assert.Equal(t, 1, res.ExitCode())
	assert.ErrorIs(t, res.Failed["doomed"], boom)

	ev := tr.events()
	assert.Contains(t, ev, "stop:healthy",
		"fail-fast must bring the healthy sibling down too")
	assert.Equal(t, []string{"stop:doomed", "stop:healthy"}, onlyPrefixed(ev, "stop:"))
}

func TestSupervisor_IsolateKeepsSiblingsRunning(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	boom := errors.New("boom")
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "healthy", trace: tr})
	reg.Register(&fake{name: "doomed", trace: tr, startErr: boom, failAfter: 20 * time.Millisecond})

	ctx, cancel := context.WithCancel(t.Context())
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{FailurePolicy: serve.Isolate})
	done := make(chan serve.Result, 1)
	go func() { done <- sup.Run(ctx, []string{"healthy", "doomed"}, enabledSet("healthy", "doomed")) }()

	// The failure happens; the healthy sibling must still be running
	// some time later.
	waitFor(t, func() bool { return slices.Contains(tr.events(), "fail:doomed") })
	time.Sleep(30 * time.Millisecond)
	assert.NotContains(t, tr.events(), "stop:healthy",
		"isolate must not stop the healthy sibling")

	cancel()
	res := <-done
	assert.Equal(t, serve.OutcomeRuntimeCrash, res.Outcome,
		"the process still exits non-zero on the worst outcome observed")
	assert.Equal(t, 1, res.ExitCode())
	assert.Contains(t, tr.events(), "stop:healthy", "shutdown still stops it")
}

func TestSupervisor_StopTimeoutAbandonsStraggler(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "straggler", trace: tr, stopDelay: 2 * time.Second})
	reg.Register(&fake{name: "prompt", trace: tr})

	ctx, cancel := context.WithCancel(t.Context())
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{})
	done := make(chan serve.Result, 1)
	go func() {
		done <- sup.Run(ctx, []string{"straggler", "prompt"}, map[string]serve.Config{
			"straggler": {Enabled: true, StopTimeout: 30 * time.Millisecond},
			"prompt":    {Enabled: true},
		})
	}()

	waitFor(t, func() bool { return len(tr.events()) >= 4 })
	cancel()

	started := time.Now()
	res := <-done
	assert.Less(t, time.Since(started), time.Second,
		"the straggler must not hold the whole shutdown")

	ev := tr.events()
	assert.Contains(t, ev, "stop-abandoned:straggler")
	assert.Contains(t, ev, "stopped:prompt",
		"the next service is stopped rather than blocked behind the straggler")
	assert.Equal(t, serve.OutcomeRuntimeCrash, res.Outcome)
	require.Contains(t, res.Failed, "straggler",
		"an abandoned stop is recorded as a failure")
	assert.Contains(t, res.Failed["straggler"].Error(), "stop exceeded")
}

func TestSupervisor_ShutdownBudgetExceeded(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "a", trace: tr, stopDelay: 500 * time.Millisecond})
	reg.Register(&fake{name: "b", trace: tr, stopDelay: 500 * time.Millisecond})

	ctx, cancel := context.WithCancel(t.Context())
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{
		ShutdownTimeout: 40 * time.Millisecond,
	})
	done := make(chan serve.Result, 1)
	go func() { done <- sup.Run(ctx, []string{"a", "b"}, enabledSet("a", "b")) }()

	waitFor(t, func() bool { return len(tr.events()) >= 4 })
	cancel()
	res := <-done

	assert.Equal(t, serve.OutcomeShutdownTimeout, res.Outcome)
	assert.Equal(t, 1, res.ExitCode())
	assert.Contains(t, res.Err.Message, "shutdown budget exceeded")
}

func TestSupervisor_ContextCancellationIsACleanStop(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "only", trace: tr})

	ctx, cancel := context.WithCancel(t.Context())
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec))
	done := make(chan serve.Result, 1)
	go func() { done <- sup.Run(ctx, []string{"only"}, enabledSet("only")) }()

	waitFor(t, func() bool { return slices.Contains(tr.events(), "ready:only") })
	cancel()
	res := <-done

	assert.Equal(t, serve.OutcomeCleanStop, res.Outcome)
	assert.Equal(t, 0, res.ExitCode(),
		"SIGTERM is how a supervisor asks for an orderly exit")
	assert.Nil(t, res.Err)
	assert.Contains(t, rec.topics(), "kit.serve.service.stopped")
}

func TestSupervisor_ZeroServicesIsAUsageError(t *testing.T) {
	t.Parallel()
	sup := serve.NewSupervisor(serve.NewRegistry(), serve.SupervisorConfig{})
	res := sup.Run(t.Context(), nil, nil)

	assert.Equal(t, serve.OutcomeNoServices, res.Outcome)
	assert.Equal(t, 2, res.ExitCode())
	require.NotNil(t, res.Err)
	assert.Equal(t, output.CodeUsage, res.Err.Code)
}

func TestSupervisor_TransientFailurePropagatesExitSix(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(&fake{
		name:      "flaky",
		trace:     tr,
		startErr:  output.TransientError("upstream unavailable"),
		failAfter: 10 * time.Millisecond,
	})

	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{})
	res := sup.Run(t.Context(), []string{"flaky"}, enabledSet("flaky"))

	require.NotNil(t, res.Err)
	assert.Equal(t, output.CodeTransient, res.Err.Code,
		"a kit transient error keeps its branch for agents and retry wrappers")
	assert.Equal(t, output.ExitTransient, res.Err.ExitCode)
}

func TestExitCodeFor_EveryOutcome(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		outcome serve.LifecycleOutcome
		code    string
		exit    int
	}{
		{serve.OutcomeCleanStop, output.CodeOK, 0},
		{serve.OutcomeInvalidSelection, output.CodeUsage, 2},
		{serve.OutcomeConfigInvalid, output.CodeUsage, 2},
		{serve.OutcomeNoServices, output.CodeUsage, 2},
		{serve.OutcomeUnknownService, output.CodeNotFound, 3},
		{serve.OutcomePolicyDenied, output.CodeUnauthorized, 5},
		{serve.OutcomeStartFailed, output.CodeGeneric, 1},
		{serve.OutcomeRuntimeCrash, output.CodeGeneric, 1},
		{serve.OutcomeShutdownTimeout, output.CodeGeneric, 1},
	} {
		assert.Equal(t, tc.exit, serve.ExitCodeFor(tc.outcome), string(tc.outcome))
		assert.Equal(t, tc.code, serve.CodeFor(tc.outcome), string(tc.outcome))
	}
}

// waitFor polls cond until it holds or the test's patience runs out.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within 3s")
}

func onlyPrefixed(events []string, prefix string) []string {
	var out []string
	for _, e := range events {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			out = append(out, e)
		}
	}
	return out
}

func countOf(all []string, want string) int {
	n := 0
	for _, s := range all {
		if s == want {
			n++
		}
	}
	return n
}
