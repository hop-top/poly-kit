package serve_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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

// cueFake reports ready after delay, then leaves when its cue closes:
// cleanly, or with an error when fail is set. Canceled first, it
// records that instead, so a test can tell the two departures apart.
type cueFake struct {
	name  string
	trace *trace
	delay time.Duration
	fail  bool
	cue   chan struct{}

	mu    sync.Mutex
	ready bool
}

func newCueFake(name string, tr *trace, delay time.Duration) *cueFake {
	return &cueFake{name: name, trace: tr, delay: delay, cue: make(chan struct{})}
}

func (c *cueFake) Name() string { return c.name }

func (c *cueFake) Start(ctx context.Context, report func()) error {
	select {
	case <-time.After(c.delay):
	case <-ctx.Done():
		c.trace.add("canceled:" + c.name)
		return nil
	}
	c.mu.Lock()
	c.ready = true
	c.mu.Unlock()
	report()
	select {
	case <-c.cue:
		c.trace.add("exit:" + c.name)
		if c.fail {
			return errors.New(c.name + " left")
		}
		return nil
	case <-ctx.Done():
		c.trace.add("canceled:" + c.name)
		return nil
	}
}

func (c *cueFake) Ready() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ready
}

func (c *cueFake) Stop(context.Context) error {
	c.trace.add("stop:" + c.name)
	return nil
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

// hungFake is a service that ignores both cancellation and Stop: its
// Start blocks, and its Stop blocks, until the test ends. It reports
// ready unless neverReady, and closes stopEntered when Stop begins.
type hungFake struct {
	name       string
	neverReady bool
	release    chan struct{} // closed by the test's cleanup
	once       sync.Once
	entered    chan struct{}
}

func newHungFake(t *testing.T, name string, neverReady bool) *hungFake {
	h := &hungFake{name: name, neverReady: neverReady,
		release: make(chan struct{}), entered: make(chan struct{})}
	t.Cleanup(func() { close(h.release) })
	return h
}

func (h *hungFake) Name() string { return h.name }

func (h *hungFake) Start(_ context.Context, report func()) error {
	if !h.neverReady {
		report()
	}
	<-h.release
	return nil
}

func (h *hungFake) Ready() bool { return !h.neverReady }

func (h *hungFake) Stop(context.Context) error {
	h.once.Do(func() { close(h.entered) })
	<-h.release
	return nil
}

// lingeringFake reports ready and stops cleanly, but its Start never
// returns until the test ends: a goroutine the run can only leave
// behind.
type lingeringFake struct {
	name    string
	release chan struct{}
}

func (l *lingeringFake) Name() string { return l.name }

func (l *lingeringFake) Start(_ context.Context, report func()) error {
	report()
	<-l.release
	return nil
}

func (l *lingeringFake) Ready() bool                { return true }
func (l *lingeringFake) Stop(context.Context) error { return nil }
