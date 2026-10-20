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

// observedRuns collects the records WithRunObserver hands over.
type observedRuns struct {
	mu   sync.Mutex
	runs []serve.RunStatus
	// started is how many services each record listed when handed
	// over.
	started []int
}

func (o *observedRuns) observe(st serve.RunStatus) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.runs = append(o.runs, st)
	o.started = append(o.started, len(st.Services()))
}

func (o *observedRuns) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.runs)
}

// awaitRun waits for the observer's nth call and returns its record.
func (o *observedRuns) awaitRun(t *testing.T, n int) serve.RunStatus {
	t.Helper()
	waitFor(t, func() bool { return o.count() >= n })
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.runs[n-1]
}

// TestRunObserver_SeesTheLiveRecord pins the seam a status surface
// outside the services reads: the observer is handed the run's record
// before any service starts, and that record answers, while the run is
// live, with an optional service's failure and its detail beside the
// core service's readiness. A second Run hands over a record of its
// own.
func TestRunObserver_SeesTheLiveRecord(t *testing.T) {
	t.Parallel()
	core := &fake{name: "core", trace: &trace{}}
	reg := serve.NewRegistry()
	reg.Register(core)
	reg.Register(&fake{name: "aux", trace: &trace{}, startErr: errBind, neverReady: true})

	obs := &observedRuns{}
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithRunObserver(obs.observe))
	ctx, cancel := context.WithCancel(t.Context())
	done := runInBackground(ctx, sup, []string{"core", "aux"},
		withOptional(enabledSet("core", "aux"), "aux"))

	rec := obs.awaitRun(t, 1)
	waitFor(t, func() bool { return rec.State("aux") == serve.StateFailed })
	assert.Equal(t, serve.StateReady, rec.State("core"))
	live := rec.Services()
	assert.Equal(t, serve.ServiceStatus{Service: "core", State: serve.StateReady},
		withoutElapsed(statusOf(t, live, "core")))
	assert.Equal(t, serve.ServiceStatus{Service: "aux", State: serve.StateFailed, Optional: true,
		Reason: "start", Error: errBind.Error()}, withoutElapsed(statusOf(t, live, "aux")))
	cancel()
	res := <-done
	assert.Equal(t, res.Services, rec.Services(), "the live record is the one the Result carries")

	ctx2, cancel2 := context.WithCancel(t.Context())
	done2 := runInBackground(ctx2, sup, []string{"core"}, enabledSet("core"))
	obs.awaitRun(t, 2)
	cancel2()
	<-done2
	obs.mu.Lock()
	defer obs.mu.Unlock()
	assert.Equal(t, []int{0, 0}, obs.started, "each record is handed over before any service starts")
	assert.Len(t, obs.runs[1].Services(), 1, "the second run's record is its own")
	assert.Len(t, obs.runs[0].Services(), 2, "and the first run's is left as it ended")
}

func withoutElapsed(s serve.ServiceStatus) serve.ServiceStatus {
	s.ElapsedMS = 0
	return s
}
