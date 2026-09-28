package serve_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/serve"
)

// viewProbe is a service that captures the RunView its Start context
// carries, so a test can ask the supervisor's record questions while
// the run is live.
type viewProbe struct {
	*fake

	mu   sync.Mutex
	view serve.RunView
	ok   bool
	got  chan struct{}
}

func newViewProbe(name string) *viewProbe {
	return &viewProbe{fake: &fake{name: name, trace: &trace{}}, got: make(chan struct{})}
}

func (p *viewProbe) Start(ctx context.Context, report func()) error {
	v, ok := serve.RunViewFrom(ctx)
	p.mu.Lock()
	p.view, p.ok = v, ok
	p.mu.Unlock()
	close(p.got)
	return p.fake.Start(ctx, report)
}

func (p *viewProbe) runView(t *testing.T) serve.RunView {
	t.Helper()
	select {
	case <-p.got:
	case <-time.After(5 * time.Second):
		t.Fatal("probe never started")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	require.True(t, p.ok, "Start's context must carry the run view")
	return p.view
}

func TestRunView_ReportsSupervisorRecord(t *testing.T) {
	t.Parallel()
	crash := &fake{
		name: "crash", trace: &trace{},
		startErr: errors.New("boom"), failAfter: 20 * time.Millisecond,
	}
	probe := newViewProbe("probe")
	reg := serve.NewRegistry()
	reg.Register(crash)
	reg.Register(probe)
	reg.Register(&fake{name: "idle", trace: &trace{}})

	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{FailurePolicy: serve.Isolate})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan serve.Result, 1)
	go func() { done <- sup.Run(ctx, []string{"crash", "probe"}, enabledSet("crash", "probe")) }()

	view := probe.runView(t)
	assert.Equal(t, serve.StateNotInRun, view.State("idle"),
		"a registered service outside the run is not in it")
	assert.Equal(t, serve.StateNotInRun, view.State("nope"))

	require.Eventually(t, func() bool { return view.State("probe") == serve.StateReady },
		5*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool { return view.State("crash") == serve.StateFailed },
		5*time.Second, 5*time.Millisecond,
		"a service that crashed under isolate is failed even though its own Ready still says true")
	assert.True(t, crash.Ready())

	cancel()
	<-done
}

func TestRunView_StartingUntilReadyReported(t *testing.T) {
	t.Parallel()
	probe := newViewProbe("slow")
	probe.neverReady = true
	reg := serve.NewRegistry()
	reg.Register(probe)

	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan serve.Result, 1)
	go func() { done <- sup.Run(ctx, []string{"slow"}, enabledSet("slow")) }()

	assert.Equal(t, serve.StateStarting, probe.runView(t).State("slow"))
	cancel()
	<-done
}

func TestRunView_AbsentOutsideASupervisor(t *testing.T) {
	t.Parallel()
	_, ok := serve.RunViewFrom(context.Background())
	assert.False(t, ok)
}
