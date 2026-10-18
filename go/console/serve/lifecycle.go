package serve

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"hop.top/kit/go/runtime/bus"
)

// supervised tracks the goroutines Start runs in. Everything but the
// exits channel and the releases group is touched by the supervisor's
// own goroutine only.
type supervised struct {
	// exits carries each Start's return, and a write-off notice for a
	// released service whose Stop overran.
	exits chan serviceExit

	// running names the Start goroutines the run still counts: the
	// run is over when none is left. A written-off service leaves it
	// when its notice arrives, not when its goroutine returns.
	running map[string]bool

	// done closes per service when its Start returns, so shutdown can
	// wait for each goroutine on its own terms.
	done map[string]chan struct{}

	// releases counts the optional services being stopped where they
	// failed; shutdown joins them.
	releases sync.WaitGroup

	// releaseCtx parents every release's Stop. It is not the run's
	// context, so a signal does not cut a release short; capReleases
	// cancels it when the shutdown deadline passes, so no release
	// outlives the shutdown budget.
	releaseCtx  context.Context
	capReleases context.CancelFunc

	startErr error
}

// serviceExit is one service's Start returning, or, with abandoned,
// the notice that a released service is written off.
type serviceExit struct {
	name      string
	err       error
	abandoned bool
}

// consume accounts for one message from exits and reports whether it
// is a Start return the caller must act on. A write-off notice, the
// late return of a written-off service, and the return of a service
// whose Stop already ran are all expected and change nothing more.
func (sup *supervised) consume(exit serviceExit, st *runState) bool {
	if exit.abandoned {
		delete(sup.running, exit.name)
		return false
	}
	if !sup.running[exit.name] {
		return false
	}
	delete(sup.running, exit.name)
	return !st.isReleased(exit.name)
}

// startAll starts each service in order, waiting for each to report
// ready (or fail, or exhaust its readiness budget) before starting the
// next. Serial start is what makes DependsOn mean anything: a
// dependent must not begin acquiring before its dependency is
// accepting work.
//
// A canceled run starts nothing further: the check comes before every
// start, including the one after an optional service was released, and
// once more after the last.
func (s *Supervisor) startAll(
	ctx context.Context,
	order []string,
	configs map[string]Config,
	st *runState,
	em *emitter,
) *supervised {
	sup := &supervised{
		// Each service sends at most one return and one write-off
		// notice, so no send ever blocks, read or not.
		exits:   make(chan serviceExit, 2*len(order)),
		running: make(map[string]bool, len(order)),
		done:    make(map[string]chan struct{}, len(order)),
	}
	sup.releaseCtx, sup.capReleases = context.WithCancel(context.WithoutCancel(ctx))

	for _, name := range order {
		if err := ctx.Err(); err != nil {
			sup.startErr = err
			return sup
		}
		svc, ok := s.reg.Lookup(name)
		if !ok {
			sup.startErr = fmt.Errorf("service %q disappeared from the registry", name)
			st.record(OutcomeStartFailed)
			st.markFailed(name, sup.startErr, "unregistered")
			em.emit(ctx, ObjectService, ActionFailed, EventPayload{
				Qualifiers: bus.Qualifiers{Reason: "unregistered"},
				Service:    name, Error: sup.startErr.Error(), ElapsedMS: st.elapsedMS(),
			})
			return sup
		}

		readyCh := make(chan struct{})
		var once sync.Once
		report := func() { once.Do(func() { close(readyCh) }) }

		st.markStarted(name)
		done := make(chan struct{})
		sup.done[name] = done
		sup.running[name] = true

		em.emit(ctx, ObjectService, ActionStarted, EventPayload{
			Service: name, ElapsedMS: st.elapsedMS(),
		})

		go func(name string, svc Service) {
			defer close(done)
			err := svc.Start(ctx, report)
			sup.exits <- serviceExit{name: name, err: err}
		}(name, svc)

		if err := s.awaitReady(ctx, sup, name, readyCh, configs, st, em); err != nil {
			sup.startErr = err
			return sup
		}
	}
	// A signal during the last start, an optional service's release
	// included, ends the run here rather than after a readiness event.
	if err := ctx.Err(); err != nil {
		sup.startErr = err
	}
	return sup
}

// awaitReady blocks until name reports ready, fails, or exhausts its
// readiness budget. A service that has not reported ready within the
// budget is a start failure (serve-lifecycle.md §"Readiness").
//
// A non-nil return ends the start sequence. An optional service's
// start failure is recorded and reported here and returns nil, so the
// next service starts (serve-lifecycle.md §"Optional services").
func (s *Supervisor) awaitReady(
	ctx context.Context,
	sup *supervised,
	name string,
	readyCh <-chan struct{},
	configs map[string]Config,
	st *runState,
	em *emitter,
) error {
	budget := DefaultReadyTimeout
	if c, ok := configs[name]; ok && c.ReadyTimeout > 0 {
		budget = c.ReadyTimeout
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()

	for {
		select {
		case <-readyCh:
			st.markReady(name)
			em.emit(ctx, ObjectService, ActionReadyReported, EventPayload{
				Service: name, Address: addrOf(s.reg, name), ElapsedMS: st.elapsedMS(),
			})
			return nil

		case exit := <-sup.exits:
			if !sup.consume(exit, st) {
				continue
			}

			// A service returning before readiness is a start
			// failure even when it returns nil: it was asked to
			// serve and it did not.
			if exit.name == name {
				err := exit.err
				if err == nil {
					err = errors.New("returned before reporting ready")
				}
				s.noteFailure(ctx, exit.name, err, OutcomeStartFailed, "start", st, em)
				if st.isOptional(name) {
					return nil
				}
				return err
			}
			if err := s.otherExit(ctx, exit, st, em); err != nil {
				return err
			}

		case <-timer.C:
			err := fmt.Errorf("not ready within %s", budget)
			s.noteFailure(ctx, name, err, OutcomeStartFailed, "ready_timeout", st, em)
			if !st.isOptional(name) {
				return err
			}
			// Its Start is still running: stop it, and wait for the
			// release, the end of the run, or a failure that ends it.
			return s.awaitRelease(ctx, sup, s.release(ctx, sup, name, configs, st, em), st, em)

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// awaitRelease waits for an optional service's release to end, the run
// to end, or another service's Start to return in the meantime, which
// it handles as the start window does: a failure under fail-fast ends
// the start sequence there, before anything further starts.
func (s *Supervisor) awaitRelease(
	ctx context.Context,
	sup *supervised,
	released <-chan struct{},
	st *runState,
	em *emitter,
) error {
	for {
		select {
		case <-released:
			return nil
		case <-ctx.Done():
			return nil
		case exit := <-sup.exits:
			if !sup.consume(exit, st) {
				continue
			}
			if err := s.otherExit(ctx, exit, st, em); err != nil {
				return err
			}
		}
	}
}

// otherExit handles the Start of a service other than the one the
// supervisor is waiting on returning while the run goes on. A failure
// is reported, and returned when the failure policy says it ends the
// run: under fail-fast, for a service the run depends on. failure_policy
// governs those services only; an optional one's failure never ends the
// run. A clean return is a stop, reported once.
func (s *Supervisor) otherExit(ctx context.Context, exit serviceExit, st *runState, em *emitter) error {
	if exit.err != nil {
		s.noteFailure(ctx, exit.name, exit.err, OutcomeRuntimeCrash, "runtime", st, em)
		if s.cfg.FailurePolicy == FailFast && !st.isOptional(exit.name) {
			return exit.err
		}
		return nil
	}
	if !st.markStopped(exit.name) {
		em.emit(ctx, ObjectService, ActionStopped, EventPayload{
			Service: exit.name, ElapsedMS: st.elapsedMS(),
		})
	}
	return nil
}

// emitAggregateReady publishes the supervisor-scoped readiness event
// once every started service is ready (serve-lifecycle.md §"Readiness").
// An optional service that failed before reporting ready does not
// participate, the same way a skipped one does not.
func (s *Supervisor) emitAggregateReady(ctx context.Context, st *runState, em *emitter) {
	st.mu.Lock()
	allReady := len(st.ready)+st.optionalNeverReadyLocked() == len(st.started) && len(st.ready) > 0
	st.mu.Unlock()
	if allReady {
		em.emit(ctx, ObjectSupervisor, ActionReadyReported, EventPayload{
			ElapsedMS: st.elapsedMS(),
		})
	}
}

// await blocks while the services run. It returns when ctx is
// canceled, when the failure policy trips, or when the last running
// service has exited.
func (s *Supervisor) await(
	ctx context.Context,
	sup *supervised,
	st *runState,
	em *emitter,
	cancelRun context.CancelFunc,
) {
	for len(sup.running) > 0 {
		select {
		case <-ctx.Done():
			return

		case exit := <-sup.exits:
			if !sup.consume(exit, st) {
				continue
			}
			// A clean return under isolate is not a failure of that
			// service, but the process must not survive as an empty
			// shell: when the last one is gone the run is over.
			if err := s.otherExit(ctx, exit, st, em); err != nil {
				cancelRun()
				return
			}
		}
	}

	// Every service returned on its own. Under isolate that is the
	// documented "the last running service stopped" exit; under
	// fail-fast it can only happen after a failure already recorded
	// one, or after a clean return, and WorstOutcome sorts it out.
	// Optional failures alone do not make it a crash, unless every
	// service in the run failed: then nothing ended cleanly.
	if (st.requiredFailed() && s.cfg.FailurePolicy == Isolate) || st.allFailed() {
		st.record(OutcomeRuntimeCrash)
	}
}

// shutdown ends the run: cancel, ordered stop, then wait for the Start
// goroutines to unwind.
//
// The order matters and is not interchangeable. Stop is what releases
// the resource a service is blocked on — closing a listener, unlinking
// a socket — so waiting for Start to return BEFORE calling Stop
// deadlocks any service whose Start blocks on an accept loop, which is
// every listener-shaped service there is. Cancellation alone does not
// free them.
//
// The wait is bounded for the same reason a Stop is: a service that
// ignores both cancellation and Stop must not hold the process open.
// Its goroutine is abandoned, which is safe because the run is over
// and the process is about to exit.
//
// services.shutdown_timeout bounds all of it once: the ordered stop,
// any release still finishing, and the wait for the Start goroutines
// share one deadline, set when the shutdown begins. A release still
// running at that deadline has its Stop's context canceled there.
func (s *Supervisor) shutdown(
	ctx context.Context,
	cancelRun context.CancelFunc,
	sup *supervised,
	st *runState,
	em *emitter,
) Result {
	cancelRun()
	deadline := time.Now().Add(s.cfg.ShutdownTimeout)
	// join returns at the deadline at the latest; a release still
	// running then has its Stop's context canceled as the run ends.
	defer sup.capReleases()

	s.stopAll(ctx, deadline, st, em)
	s.join(sup, st, deadline)
	return s.finish(st, em)
}

// join waits, until the shutdown deadline, for every release and every
// Start goroutine to return. Only a required service's goroutine still
// running at the deadline is the run's shutdown timeout: an optional
// one — a release cut at the deadline included — is the run's to leave
// behind, and one already written off is not waited for at all.
func (s *Supervisor) join(sup *supervised, st *runState, deadline time.Time) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	expired := false
	// stillRunning waits for ch until the shared budget runs out, then
	// only looks.
	stillRunning := func(ch <-chan struct{}) bool {
		if !expired {
			select {
			case <-ch:
				return false
			case <-timer.C:
				expired = true
			}
		}
		select {
		case <-ch:
			return false
		default:
			return true
		}
	}

	releases := make(chan struct{})
	go func() {
		sup.releases.Wait()
		close(releases)
	}()
	// A release is an optional service's: one still running is left
	// behind, never the run's failure.
	stillRunning(releases)

	started, _, _, _ := st.snapshot()
	stuck := false
	for _, name := range started {
		done, ok := sup.done[name]
		if !ok || st.isAbandoned(name) {
			continue
		}
		if stillRunning(done) && !st.isOptional(name) {
			stuck = true
		}
	}
	if stuck {
		st.record(OutcomeShutdownTimeout)
	}
}
