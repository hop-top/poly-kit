package serve_test

import (
	"context"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/runtime/bus"
)

// TestSupervisor_OptionalReadyTimeoutIsStoppedOnce pins that an
// optional service that misses its readiness budget is stopped there
// and then, so it cannot start serving later while reported failed;
// that the error its Start returns once stopped is neither a second
// failure nor a fail-fast trip, whether it arrives while a later
// service starts or while the run goes on; and that the final shutdown
// does not stop it again.
//
// The core leaves on its own cue, after the stopped service's Start
// has returned. The run cannot end before it has accounted for that
// return, so when it ends by itself the outcome below is settled.
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
			core := newCueFake("core", tr, tc.coreDelay)
			stuck := &stuckFake{name: "aux", release: make(chan struct{})}
			reg := serve.NewRegistry()
			reg.Register(stuck)
			reg.Register(core)

			configs := enabledSet("aux", "core")
			configs["aux"] = serve.Config{Enabled: true, Optional: true, ReadyTimeout: 30 * time.Millisecond}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			sup := serve.NewSupervisor(reg, serve.SupervisorConfig{FailurePolicy: serve.FailFast},
				serve.WithPublisher(rec))
			done := runInBackground(ctx, sup, tc.order, configs)

			waitFor(t, func() bool {
				return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
			})
			assert.Equal(t, 1, stuck.stopCount(), "the timed-out service is stopped when it fails")
			close(core.cue)

			res := <-done
			require.NoError(t, ctx.Err(), "the run ends by itself once the core leaves")
			assert.Contains(t, tr.events(), "exit:core", "the core left on its cue")
			assert.NotContains(t, tr.events(), "canceled:core", "the stopped service's exit must not trip fail-fast")
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

// TestSupervisor_SignalDuringOptionalReleaseStartsNothingFurther pins
// that a signal while an optional service is being stopped where it
// missed its readiness budget ends the start sequence: nothing starts
// after it. The Stop itself keeps its budget, so the run returns once
// that Stop is over or written off.
func TestSupervisor_SignalDuringOptionalReleaseStartsNothingFurther(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	aux := newHungFake(t, "aux", true)
	reg := serve.NewRegistry()
	reg.Register(aux)
	reg.Register(&fake{name: "core", trace: tr})

	configs := enabledSet("aux", "core")
	configs["aux"] = serve.Config{Enabled: true, Optional: true,
		ReadyTimeout: 20 * time.Millisecond, StopTimeout: 200 * time.Millisecond}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runInBackground(ctx, serve.NewSupervisor(reg, serve.SupervisorConfig{}), []string{"aux", "core"}, configs)

	select {
	case <-aux.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the timed-out service was never stopped")
	}
	cancel()
	select {
	case res := <-done:
		assert.Equal(t, serve.OutcomeCleanStop, res.Outcome)
		assert.Equal(t, 0, res.ExitCode())
		assert.NotContains(t, tr.events(), "start:core", "nothing starts after the signal")
		assert.Equal(t, []string{"aux"}, res.Started)
	case <-time.After(5 * time.Second):
		t.Fatal("the run outlived the release's stop budget")
	}
}

// listenerFake binds a real listener at Start and never reports
// ready; its Stop takes drain to finish whatever it holds, ignoring
// cancellation, then closes the listener.
type listenerFake struct {
	name    string
	drain   time.Duration
	entered chan struct{}
	once    sync.Once

	mu     sync.Mutex
	ln     net.Listener
	closed bool
}

func (l *listenerFake) Name() string { return l.name }

func (l *listenerFake) Start(context.Context, func()) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.ln = ln
	l.mu.Unlock()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return nil
		}
		_ = conn.Close()
	}
}

func (l *listenerFake) Ready() bool { return false }

func (l *listenerFake) Stop(context.Context) error {
	l.once.Do(func() { close(l.entered) })
	time.Sleep(l.drain)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ln != nil {
		_ = l.ln.Close()
	}
	l.closed = true
	return nil
}

func (l *listenerFake) state() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ln == nil {
		return "", l.closed
	}
	return l.ln.Addr().String(), l.closed
}

// TestSupervisor_ReleaseStillFreesTheListenerWhenASignalArrives pins
// that a signal during the release does not cut the Stop short: by the
// time the run returns, the listener the never-ready service bound is
// closed and its port free.
func TestSupervisor_ReleaseStillFreesTheListenerWhenASignalArrives(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	aux := &listenerFake{name: "aux", drain: 150 * time.Millisecond, entered: make(chan struct{})}
	reg := serve.NewRegistry()
	reg.Register(aux)
	reg.Register(&fake{name: "core", trace: tr})

	configs := enabledSet("aux", "core")
	configs["aux"] = serve.Config{Enabled: true, Optional: true,
		ReadyTimeout: 30 * time.Millisecond, StopTimeout: 3 * time.Second}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runInBackground(ctx, serve.NewSupervisor(reg, serve.SupervisorConfig{}), []string{"aux", "core"}, configs)
	select {
	case <-aux.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the timed-out service was never stopped")
	}
	cancel()
	res := <-done

	addr, closed := aux.state()
	require.NotEmpty(t, addr, "the service bound its listener")
	assert.True(t, closed, "the release's Stop finished before the run returned")
	ln, err := net.Listen("tcp", addr)
	require.NoError(t, err, "the port is free again")
	_ = ln.Close()
	assert.Equal(t, 0, res.ExitCode())
	assert.NotContains(t, tr.events(), "start:core")
}

// hookRecorder is a recorder that also calls on for each event.
type hookRecorder struct {
	*recorder
	on func(bus.Event)
}

func (h hookRecorder) Publish(ctx context.Context, e bus.Event) error {
	_ = h.recorder.Publish(ctx, e)
	h.on(e)
	return nil
}

// gatedFake never reports ready; its Stop returns once gate closes or
// its budget runs out, and only then does its Start return.
type gatedFake struct {
	name    string
	gate    chan struct{}
	entered chan struct{}
	freed   chan struct{}
	once    sync.Once
}

func (g *gatedFake) Name() string { return g.name }

func (g *gatedFake) Start(context.Context, func()) error {
	<-g.freed
	return nil
}

func (g *gatedFake) Ready() bool { return false }

func (g *gatedFake) Stop(ctx context.Context) error {
	g.once.Do(func() { close(g.entered) })
	defer close(g.freed)
	select {
	case <-g.gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestSupervisor_FailFastDuringReleaseStartsNothingFurther pins that a
// required service failing while an optional one is being released is
// handled there and then: under fail-fast the start sequence ends, and
// no later service starts.
//
// The optional service's Stop waits for the required failure to be
// reported, so the failure lands inside the release window, and a
// supervisor that did not look at it until the release was over would
// start the last service first.
func TestSupervisor_FailFastDuringReleaseStartsNothingFurther(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	core := newCueFake("core", tr, 0)
	core.fail = true
	aux := &gatedFake{name: "aux", gate: make(chan struct{}), entered: make(chan struct{}), freed: make(chan struct{})}
	reg := serve.NewRegistry()
	reg.Register(core)
	reg.Register(aux)
	reg.Register(&fake{name: "last", trace: tr})

	var once sync.Once
	rec := hookRecorder{recorder: &recorder{}, on: func(e bus.Event) {
		if p, ok := e.Payload.(serve.EventPayload); ok && e.Topic == "kit.serve.service.failed" && p.Service == "core" {
			once.Do(func() { close(aux.gate) })
		}
	}}
	configs := enabledSet("core", "aux", "last")
	configs["aux"] = serve.Config{Enabled: true, Optional: true,
		ReadyTimeout: 20 * time.Millisecond, StopTimeout: time.Second}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{FailurePolicy: serve.FailFast}, serve.WithPublisher(rec))
	done := runInBackground(ctx, sup, []string{"core", "aux", "last"}, configs)

	select {
	case <-aux.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the timed-out service was never stopped")
	}
	close(core.cue)
	res := <-done
	require.NoError(t, ctx.Err(), "fail-fast ends the run by itself")
	assert.NotContains(t, tr.events(), "start:last", "nothing starts after the required failure")
	assert.Equal(t, serve.OutcomeRuntimeCrash, res.Outcome)
	assert.Equal(t, 1, res.ExitCode())
}

// TestSupervisor_OverrunReleaseDoesNotKeepTheRunAlive pins that an
// optional service written off after its Stop overran no longer counts
// as running: when the rest of the run returns on its own, the run is
// over, with no signal needed.
func TestSupervisor_OverrunReleaseDoesNotKeepTheRunAlive(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(newHungFake(t, "aux", true))
	reg.Register(&fake{name: "core", trace: tr, exitAfter: 50 * time.Millisecond})

	configs := enabledSet("aux", "core")
	configs["aux"] = serve.Config{Enabled: true, Optional: true,
		ReadyTimeout: 20 * time.Millisecond, StopTimeout: 20 * time.Millisecond}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	res := serve.NewSupervisor(reg, serve.SupervisorConfig{}).Run(ctx, []string{"aux", "core"}, configs)
	require.NoError(t, ctx.Err(), "the run ends when the last counted service returns")
	assert.Equal(t, serve.OutcomeCleanStop, res.Outcome)
	assert.Contains(t, tr.events(), "exit:core")
	aux := statusOf(t, res.Services, "aux")
	assert.Equal(t, "ready_timeout", aux.Reason, "the overrun does not replace the failure that put it there")
}

// TestSupervisor_OptionalReleaseOverrunIsOneFailure pins that writing
// off a stuck release emits no second failed event.
func TestSupervisor_OptionalReleaseOverrunIsOneFailure(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	reg := serve.NewRegistry()
	reg.Register(newHungFake(t, "aux", true))
	reg.Register(&fake{name: "core", trace: &trace{}})

	configs := enabledSet("aux", "core")
	configs["aux"] = serve.Config{Enabled: true, Optional: true,
		ReadyTimeout: 20 * time.Millisecond, StopTimeout: 20 * time.Millisecond}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runInBackground(ctx, serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec)),
		[]string{"aux", "core"}, configs)
	waitFor(t, func() bool {
		return slices.Contains(rec.topics(), "kit.serve.supervisor.ready_reported")
	})
	cancel()
	<-done
	failed := failedEvents(rec, "aux")
	require.Len(t, failed, 1)
	assert.Equal(t, "ready_timeout", failed[0].Reason)
}

// observingFake records, when its Stop is called, whether the
// listener of the service it watches was already closed.
type observingFake struct {
	*fake
	watch      *listenerFake
	mu         sync.Mutex
	sawClosed  bool
	stopCalled bool
}

func (o *observingFake) Stop(ctx context.Context) error {
	_, closed := o.watch.state()
	o.mu.Lock()
	o.sawClosed, o.stopCalled = closed, true
	o.mu.Unlock()
	return o.fake.Stop(ctx)
}

// TestSupervisor_SignalDuringReleaseStopsTheOthersAtOnce pins that a
// signal during a release starts the shutdown at once: the services
// already running are stopped while the release is still finishing,
// not after it.
func TestSupervisor_SignalDuringReleaseStopsTheOthersAtOnce(t *testing.T) {
	t.Parallel()
	aux := &listenerFake{name: "aux", drain: 300 * time.Millisecond, entered: make(chan struct{})}
	core := &observingFake{fake: &fake{name: "core", trace: &trace{}}, watch: aux}
	reg := serve.NewRegistry()
	reg.Register(core)
	reg.Register(aux)

	configs := enabledSet("core", "aux")
	configs["aux"] = serve.Config{Enabled: true, Optional: true,
		ReadyTimeout: 20 * time.Millisecond, StopTimeout: 3 * time.Second}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runInBackground(ctx, serve.NewSupervisor(reg, serve.SupervisorConfig{}), []string{"core", "aux"}, configs)
	select {
	case <-aux.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the timed-out service was never stopped")
	}
	cancel()
	res := <-done

	core.mu.Lock()
	defer core.mu.Unlock()
	require.True(t, core.stopCalled)
	assert.False(t, core.sawClosed, "core was stopped while the release was still draining")
	_, closed := aux.state()
	assert.True(t, closed, "and the release still finished before the run returned")
	assert.Equal(t, 0, res.ExitCode())
}
