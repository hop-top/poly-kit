package serve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/runtime/bus"
)

// SupervisorConfig is the supervisor-scoped half of the services
// block (contract §"Configuration surface"). The per-service half
// travels in [Request].Configs.
type SupervisorConfig struct {
	// FailurePolicy is services.failure_policy. Empty means
	// DefaultFailurePolicy (fail-fast).
	FailurePolicy FailurePolicy

	// ShutdownTimeout is services.shutdown_timeout, the total budget
	// across every service's Stop. Zero means
	// DefaultShutdownTimeout.
	ShutdownTimeout time.Duration
}

// Option configures a [Supervisor].
type Option func(*Supervisor)

// WithPublisher wires the bus the supervisor publishes lifecycle
// events to. Without one, only the log counterpart runs.
func WithPublisher(p Publisher) Option {
	return func(s *Supervisor) { s.pub = p }
}

// WithLogger wires the structured logger the supervisor writes the
// operator-legible startup and shutdown trace to.
func WithLogger(l Logger) Option {
	return func(s *Supervisor) { s.log = l }
}

// WithTopics overrides the lifecycle topic set. Defaults to
// DefaultTopics(DefaultTopicPrefix); adopters rebrand the prefix the
// same way every other kit emitter's prefix is rebranded.
func WithTopics(t bus.TopicMap) Option {
	return func(s *Supervisor) { s.topics = t }
}

// WithEventSource sets the bus Event.Source field. Defaults to
// DefaultTopicPrefix.
func WithEventSource(src string) Option {
	return func(s *Supervisor) { s.source = src }
}

// WithClock replaces the supervisor's notion of elapsed time. Tests
// use it to keep event payloads deterministic; production never sets
// it.
func WithClock(now func() time.Time) Option {
	return func(s *Supervisor) {
		if now != nil {
			s.now = now
		}
	}
}

// WithEscalation wires the second-signal channel from
// [SignalContext]. A receive on it during shutdown abandons the drain
// and ends the run with the crash code, so an operator can escalate
// without reaching for SIGKILL (contract §"Signals").
func WithEscalation(ch <-chan os.Signal) Option {
	return func(s *Supervisor) { s.escalate = ch }
}

// Supervisor runs a resolved set of services under one lifecycle:
// ordered start, per-service readiness, policy-driven reaction to
// failure, and ordered stop bounded by the configured budgets
// (contract §"Readiness", §"Shutdown", §"One service fails while
// others run").
//
// A Supervisor holds no global state and no package-level registry:
// everything it touches arrives through New and Run, so two of them
// can run concurrently in the same process — which is exactly what a
// test binary does.
type Supervisor struct {
	reg      *Registry
	cfg      SupervisorConfig
	pub      Publisher
	log      Logger
	topics   bus.TopicMap
	source   string
	now      func() time.Time
	escalate <-chan os.Signal
}

// NewSupervisor returns a Supervisor over reg with cfg. Defaults are
// filled in for every zero field, so the zero SupervisorConfig is the
// documented default configuration rather than an unusable one.
func NewSupervisor(reg *Registry, cfg SupervisorConfig, opts ...Option) *Supervisor {
	if cfg.FailurePolicy == "" {
		cfg.FailurePolicy = DefaultFailurePolicy
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = DefaultShutdownTimeout
	}
	s := &Supervisor{
		reg:    reg,
		cfg:    cfg,
		topics: DefaultTopics(DefaultTopicPrefix),
		source: DefaultTopicPrefix,
		now:    time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Result is what one supervised run produced.
type Result struct {
	// Outcome is the worst outcome observed across the whole run,
	// which is what the process exits on (contract §"Exit behavior").
	Outcome LifecycleOutcome

	// Err is the rendered failure carrying Code and ExitCode, nil on
	// a clean stop.
	Err *output.Error

	// Started is the identifiers of services whose Start was invoked,
	// in the order they were invoked. Stop runs in the exact reverse.
	Started []string

	// Ready is the identifiers that reported ready, in report order.
	Ready []string

	// Failed maps a service identifier to the error it returned.
	Failed map[string]error
}

// ExitCode is the process exit code for this run.
func (r Result) ExitCode() int { return ExitCodeFor(r.Outcome) }

// runState is the mutable half of one Run, kept off the Supervisor so
// concurrent runs cannot see each other.
type runState struct {
	mu       sync.Mutex
	started  []string
	ready    []string
	failed   map[string]error
	observed []LifecycleOutcome
	begin    time.Time
	now      func() time.Time

	// stopConfigs is the per-service block the stop sequence reads
	// its budgets from. Held here rather than passed down so the
	// shutdown path needs no argument the caller could forget.
	stopConfigs map[string]Config
}

func (st *runState) elapsedMS() int64 {
	return st.now().Sub(st.begin).Milliseconds()
}

func (st *runState) record(o LifecycleOutcome) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.observed = append(st.observed, o)
}

func (st *runState) markFailed(name string, err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.failed[name] = err
}

func (st *runState) snapshot() ([]string, []string, map[string]error, []LifecycleOutcome) {
	st.mu.Lock()
	defer st.mu.Unlock()
	started := append([]string(nil), st.started...)
	ready := append([]string(nil), st.ready...)
	failed := make(map[string]error, len(st.failed))
	for k, v := range st.failed {
		failed[k] = v
	}
	observed := append([]LifecycleOutcome(nil), st.observed...)
	return started, ready, failed, observed
}

// Run starts every service in selected, waits for the run to end, and
// stops everything in reverse start order.
//
// The run ends when ctx is canceled (the clean path: a signal, or the
// caller's own shutdown), when a service failure trips the failure
// policy, or when every started service has returned. Run always
// performs the ordered stop before returning, so a caller never has to
// clean up after it.
//
// selected is normally Outcome.Selected from [Resolve]; Run does not
// re-resolve and does not consult enablement, because the decision the
// caller already made is the one the contract says to honor.
func (s *Supervisor) Run(ctx context.Context, selected []string, configs map[string]Config) Result {
	st := &runState{
		failed:      make(map[string]error),
		begin:       s.now(),
		now:         s.now,
		stopConfigs: configs,
	}
	em := &emitter{topics: s.topics, pub: s.pub, log: s.log, source: s.source}

	if len(selected) == 0 {
		return s.result(st, OutcomeNoServices, output.UsageError(
			"no services configured and enabled; enable one under services.* or name one explicitly",
		))
	}

	order := StartOrder(s.reg, selected)

	// The run context is the caller's, plus a cancel the supervisor
	// itself trips when the failure policy says to bring everything
	// down. Every service observes cancellation at the same instant;
	// nothing is queued behind another service's drain.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	sup := s.startAll(runCtx, order, configs, st, em)

	// A start failure short-circuits: services already running are
	// stopped in reverse order, and the outcome is start-failed.
	if sup.startErr != nil {
		return s.shutdown(ctx, cancelRun, sup, st, em)
	}

	s.emitAggregateReady(runCtx, st, em)
	s.await(runCtx, sup, st, em, cancelRun)

	return s.shutdown(ctx, cancelRun, sup, st, em)
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
func (s *Supervisor) shutdown(
	ctx context.Context,
	cancelRun context.CancelFunc,
	sup *supervised,
	st *runState,
	em *emitter,
) Result {
	cancelRun()
	s.stopAll(ctx, st, em)

	done := make(chan struct{})
	go func() {
		sup.wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(s.cfg.ShutdownTimeout):
		st.record(OutcomeShutdownTimeout)
	}

	return s.finish(st, em)
}

// supervised tracks the goroutines Start runs in.
type supervised struct {
	wg       sync.WaitGroup
	exits    chan serviceExit
	live     int
	startErr error
}

func (sup *supervised) wait() { sup.wg.Wait() }

// serviceExit is one service's Start returning.
type serviceExit struct {
	name string
	err  error
}

// startAll starts each service in order, waiting for each to report
// ready (or fail, or exhaust its readiness budget) before starting the
// next. Serial start is what makes DependsOn mean anything: a
// dependent must not begin acquiring before its dependency is
// accepting work.
func (s *Supervisor) startAll(
	ctx context.Context,
	order []string,
	configs map[string]Config,
	st *runState,
	em *emitter,
) *supervised {
	sup := &supervised{exits: make(chan serviceExit, len(order))}

	for _, name := range order {
		svc, ok := s.reg.Lookup(name)
		if !ok {
			sup.startErr = fmt.Errorf("service %q disappeared from the registry", name)
			st.record(OutcomeStartFailed)
			st.markFailed(name, sup.startErr)
			em.emit(ctx, ObjectService, ActionFailed, EventPayload{
				Qualifiers: bus.Qualifiers{Reason: "unregistered"},
				Service:    name, Error: sup.startErr.Error(), ElapsedMS: st.elapsedMS(),
			})
			return sup
		}

		readyCh := make(chan struct{})
		var once sync.Once
		report := func() { once.Do(func() { close(readyCh) }) }

		st.mu.Lock()
		st.started = append(st.started, name)
		st.mu.Unlock()
		sup.live++

		em.emit(ctx, ObjectService, ActionStarted, EventPayload{
			Service: name, ElapsedMS: st.elapsedMS(),
		})

		sup.wg.Add(1)
		go func(name string, svc Service) {
			defer sup.wg.Done()
			err := svc.Start(ctx, report)
			sup.exits <- serviceExit{name: name, err: err}
		}(name, svc)

		if err := s.awaitReady(ctx, name, readyCh, sup.exits, &sup.live, configs, st, em); err != nil {
			sup.startErr = err
			return sup
		}
	}
	return sup
}

// awaitReady blocks until name reports ready, fails, or exhausts its
// readiness budget. A service that has not reported ready within the
// budget is a start failure (contract §"Readiness").
func (s *Supervisor) awaitReady(
	ctx context.Context,
	name string,
	readyCh <-chan struct{},
	exits chan serviceExit,
	live *int,
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
			st.mu.Lock()
			st.ready = append(st.ready, name)
			st.mu.Unlock()
			em.emit(ctx, ObjectService, ActionReadyReported, EventPayload{
				Service: name, Address: addrOf(s.reg, name), ElapsedMS: st.elapsedMS(),
			})
			return nil

		case exit := <-exits:
			// Every exit consumed here is one fewer goroutine the
			// wait loop will see, whichever service it belonged to.
			*live--

			// A service returning before readiness is a start
			// failure even when it returns nil: it was asked to
			// serve and it did not.
			if exit.name == name {
				err := exit.err
				if err == nil {
					err = errors.New("returned before reporting ready")
				}
				s.noteFailure(ctx, exit.name, err, OutcomeStartFailed, "start", st, em)
				return err
			}
			// A different service failing during this one's start
			// window is still a failure of the run.
			if exit.err != nil {
				s.noteFailure(ctx, exit.name, exit.err, OutcomeRuntimeCrash, "runtime", st, em)
				if s.cfg.FailurePolicy == FailFast {
					return exit.err
				}
				continue
			}
			// An earlier service returning cleanly mid-start is a
			// stop, not a failure.
			em.emit(ctx, ObjectService, ActionStopped, EventPayload{
				Service: exit.name, ElapsedMS: st.elapsedMS(),
			})

		case <-timer.C:
			err := fmt.Errorf("not ready within %s", budget)
			s.noteFailure(ctx, name, err, OutcomeStartFailed, "ready_timeout", st, em)
			return err

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// emitAggregateReady publishes the supervisor-scoped readiness event
// once every started service is ready (contract §"Readiness").
func (s *Supervisor) emitAggregateReady(ctx context.Context, st *runState, em *emitter) {
	st.mu.Lock()
	allReady := len(st.ready) == len(st.started) && len(st.started) > 0
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
	for sup.live > 0 {
		select {
		case <-ctx.Done():
			return

		case exit := <-sup.exits:
			sup.live--
			if exit.err != nil {
				s.noteFailure(ctx, exit.name, exit.err, OutcomeRuntimeCrash, "runtime", st, em)
				if s.cfg.FailurePolicy == FailFast {
					cancelRun()
					return
				}
				continue
			}
			// A clean return under isolate is not a failure of that
			// service, but the process must not survive as an empty
			// shell: when the last one is gone the run is over.
			em.emit(ctx, ObjectService, ActionStopped, EventPayload{
				Service: exit.name, ElapsedMS: st.elapsedMS(),
			})
		}
	}

	// Every service returned on its own. Under isolate that is the
	// documented "the last running service stopped" exit; under
	// fail-fast it can only happen after a failure already recorded
	// one, or after a clean return, and WorstOutcome sorts it out.
	st.mu.Lock()
	anyFailed := len(st.failed) > 0
	st.mu.Unlock()
	if anyFailed && s.cfg.FailurePolicy == Isolate {
		st.record(OutcomeRuntimeCrash)
	}
}

// noteFailure records a failure and emits its event.
func (s *Supervisor) noteFailure(
	ctx context.Context,
	name string,
	err error,
	outcome LifecycleOutcome,
	reason string,
	st *runState,
	em *emitter,
) {
	st.markFailed(name, err)
	st.record(outcome)
	em.emit(ctx, ObjectService, ActionFailed, EventPayload{
		Qualifiers: bus.Qualifiers{Reason: reason},
		Service:    name, Error: err.Error(), ElapsedMS: st.elapsedMS(),
	})
}

// finish assembles the Result from everything the run observed.
func (s *Supervisor) finish(st *runState, em *emitter) Result {
	started, ready, failed, observed := st.snapshot()
	worst := WorstOutcome(observed)
	res := Result{
		Outcome: worst,
		Started: started,
		Ready:   ready,
		Failed:  failed,
	}
	if IsFailure(worst) {
		res.Err = failureError(worst, failed)
	}
	em.emit(context.Background(), ObjectSupervisor, ActionStopped, EventPayload{
		Qualifiers: bus.Qualifiers{Reason: string(worst)},
		ElapsedMS:  st.elapsedMS(),
	})
	return res
}

// result is finish's short circuit for a run that never started.
func (s *Supervisor) result(st *runState, o LifecycleOutcome, err *output.Error) Result {
	started, ready, failed, _ := st.snapshot()
	return Result{Outcome: o, Err: err, Started: started, Ready: ready, Failed: failed}
}

// addrOf reads the optional [Addressed] declaration.
func addrOf(reg *Registry, name string) string {
	svc, ok := reg.Lookup(name)
	if !ok {
		return ""
	}
	a, ok := svc.(Addressed)
	if !ok {
		return ""
	}
	return a.Addr()
}
