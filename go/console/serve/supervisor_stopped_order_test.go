package serve_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/runtime/bus"
)

// stopReleasedFake's Start returns only once its Stop was called, the
// listener shape; Start-returns-on-cancel is the plain fake.
type stopReleasedFake struct {
	name string
	stop chan struct{}
	once sync.Once
}

func (b *stopReleasedFake) Name() string { return b.name }
func (b *stopReleasedFake) Start(_ context.Context, report func()) error {
	report()
	<-b.stop
	return nil
}
func (b *stopReleasedFake) Ready() bool { return true }
func (b *stopReleasedFake) Stop(context.Context) error {
	b.once.Do(func() { close(b.stop) })
	return nil
}

func stoppedOrder(rec *recorder) []string {
	var out []string
	for _, e := range rec.snapshot() {
		if e.Topic != "kit.serve.service.stopped" {
			continue
		}
		if p, ok := e.Payload.(serve.EventPayload); ok {
			out = append(out, p.Service)
		}
	}
	return out
}

// gatedRecorder holds the aggregate-ready publish until released, which
// keeps the supervisor out of await while the run is canceled and the
// Start calls return: ctx.Done and every exit are then ready together
// when await first selects, and Go picks among them at random.
type gatedRecorder struct {
	recorder
	gate chan struct{}
}

func (g *gatedRecorder) Publish(ctx context.Context, e bus.Event) error {
	if e.Topic == "kit.serve.supervisor.ready_reported" {
		<-g.gate
	}
	return g.recorder.Publish(ctx, e)
}

// Whichever select branch await takes, each service reports exactly
// one stopped, in reverse start order: stopped is Stop returning.
func TestSupervisor_ShutdownStoppedEventsFollowReverseStartOrder(t *testing.T) {
	t.Parallel()
	names := []string{"alpha", "beta", "gamma", "delta"}
	for range 50 {
		tr := &trace{}
		rec := &gatedRecorder{gate: make(chan struct{})}
		reg := serve.NewRegistry()
		reg.Register(&fake{name: "alpha", trace: tr})
		reg.Register(&stopReleasedFake{name: "beta", stop: make(chan struct{})})
		reg.Register(&fake{name: "gamma", trace: tr})
		reg.Register(&stopReleasedFake{name: "delta", stop: make(chan struct{})})

		ctx, cancel := context.WithCancel(t.Context())
		sup := serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec))
		done := make(chan serve.Result, 1)
		go func() { done <- sup.Run(ctx, names, enabledSet(names...)) }()

		waitFor(t, func() bool { return slices.Contains(tr.events(), "ready:gamma") })
		cancel()
		time.Sleep(20 * time.Millisecond)
		close(rec.gate)
		res := <-done
		require.Nil(t, res.Err)

		require.Equal(t, []string{"delta", "gamma", "beta", "alpha"}, stoppedOrder(&rec.recorder))
	}
}

// A service that returns on its own before shutdown still reports
// stopped at that moment, once, and Stop does not repeat it.
func TestSupervisor_EarlyExitStillReportsStoppedOnce(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "steady", trace: tr})
	reg.Register(&fake{name: "quitter", trace: tr, exitAfter: 10 * time.Millisecond})

	ctx, cancel := context.WithCancel(t.Context())
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec))
	done := make(chan serve.Result, 1)
	go func() { done <- sup.Run(ctx, []string{"steady", "quitter"}, enabledSet("steady", "quitter")) }()

	waitFor(t, func() bool { return slices.Contains(tr.events(), "exit:quitter") })
	waitFor(t, func() bool { return slices.Contains(stoppedOrder(rec), "quitter") })
	assert.Equal(t, []string{"quitter"}, stoppedOrder(rec), "reported at exit, before shutdown")
	cancel()
	<-done
	assert.Equal(t, []string{"quitter", "steady"}, stoppedOrder(rec))
}
