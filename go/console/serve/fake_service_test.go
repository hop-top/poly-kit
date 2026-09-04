package serve_test

import (
	"context"
	"sync"
	"time"
)

// trace records the order in which lifecycle callbacks fired across
// every fake service in one test.
type trace struct {
	mu   sync.Mutex
	seen []string
}

func (t *trace) add(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen = append(t.seen, s)
}

func (t *trace) events() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.seen...)
}

// fake is a configurable Service for lifecycle tests.
type fake struct {
	name  string
	trace *trace

	// startErr, when non-nil, is returned by Start. It is returned
	// after readiness unless neverReady is set.
	startErr error
	// failAfter delays startErr until the duration elapses, which is
	// how a runtime crash (as opposed to a start failure) is staged.
	failAfter time.Duration
	// neverReady suppresses the ready report, so the service
	// exhausts its readiness budget.
	neverReady bool
	// stopDelay makes Stop overrun its budget.
	stopDelay time.Duration
	// stopErr is returned by Stop.
	stopErr error
	// deps is the DependsOn declaration; nil means no declaration.
	deps []string
	// validateErr, when non-nil, makes the service fail the config
	// gate.
	validateErr error

	mu    sync.Mutex
	ready bool
}

func (f *fake) Name() string { return f.name }

func (f *fake) Start(ctx context.Context, report func()) error {
	f.trace.add("start:" + f.name)

	if f.startErr != nil && f.failAfter == 0 && f.neverReady {
		return f.startErr
	}
	if f.neverReady {
		<-ctx.Done()
		return nil
	}

	f.mu.Lock()
	f.ready = true
	f.mu.Unlock()
	f.trace.add("ready:" + f.name)
	report()

	if f.startErr != nil {
		if f.failAfter > 0 {
			select {
			case <-time.After(f.failAfter):
			case <-ctx.Done():
				return nil
			}
		}
		f.trace.add("fail:" + f.name)
		return f.startErr
	}

	<-ctx.Done()
	return nil
}

func (f *fake) Ready() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

func (f *fake) Stop(ctx context.Context) error {
	f.trace.add("stop:" + f.name)
	if f.stopDelay > 0 {
		select {
		case <-time.After(f.stopDelay):
		case <-ctx.Done():
			f.trace.add("stop-abandoned:" + f.name)
			return ctx.Err()
		}
	}
	f.mu.Lock()
	f.ready = false
	f.mu.Unlock()
	f.trace.add("stopped:" + f.name)
	return f.stopErr
}

// dependentFake adds the optional DependsOn declaration.
type dependentFake struct{ *fake }

func (d dependentFake) DependsOn() []string { return d.deps }

// validatingFake adds the optional Validate gate.
type validatingFake struct{ *fake }

func (v validatingFake) Validate() error { return v.validateErr }
