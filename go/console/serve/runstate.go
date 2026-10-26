package serve

import (
	"slices"
	"sync"
	"time"
)

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

	// stopped names the services whose stopped event has already been
	// emitted, because their Start returned cleanly on its own before
	// the ordered stop reached them. Stop still runs for them — it is
	// what releases what Start acquired — but a service reports
	// stopped once per run, whichever path noticed it first.
	stopped map[string]bool

	// stopConfigs is the per-service block the stop sequence reads
	// its budgets from. Held here rather than passed down so the
	// shutdown path needs no argument the caller could forget.
	stopConfigs map[string]Config

	// optional names the services the run does not depend on. It is
	// fixed before any service starts and only read afterwards.
	optional map[string]bool

	// reasons is each failed service's failure kind, the value its
	// failed event carried under reason.
	reasons map[string]string

	// changed is each service's elapsed time at its latest recorded
	// transition.
	changed map[string]int64

	// released names the services whose Stop already ran mid-run: an
	// optional service stopped where it failed. The ordered stop
	// skips them, and the exit their Start makes once stopped is
	// expected and changes nothing.
	released map[string]bool

	// abandoned names the optional services written off because
	// their Stop overran its budget: their Start goroutine may never
	// return, so the run neither counts it as running nor waits for
	// it at shutdown.
	abandoned map[string]bool
}

func newRunState(now func() time.Time, selected []string, configs map[string]Config) *runState {
	return &runState{
		failed:      make(map[string]error),
		begin:       now(),
		now:         now,
		stopConfigs: configs,
		optional:    optionalSet(selected, configs),
		reasons:     make(map[string]string),
		changed:     make(map[string]int64),
		released:    make(map[string]bool),
		abandoned:   make(map[string]bool),
	}
}

func (st *runState) elapsedMS() int64 {
	return st.now().Sub(st.begin).Milliseconds()
}

func (st *runState) record(o LifecycleOutcome) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.observed = append(st.observed, o)
}

// isOptional reports whether the run does not depend on name. The set
// is fixed before any service starts, so it is read without the lock.
func (st *runState) isOptional(name string) bool { return st.optional[name] }

func (st *runState) markStarted(name string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.started = append(st.started, name)
	st.changed[name] = st.elapsedMS()
}

func (st *runState) markReady(name string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.ready = append(st.ready, name)
	st.changed[name] = st.elapsedMS()
}

// markFailed records name's failure and its kind.
func (st *runState) markFailed(name string, err error, reason string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.failed[name] = err
	st.reasons[name] = reason
	st.changed[name] = st.elapsedMS()
}

// markStopped records that name's stopped event has been emitted and
// reports whether it had been already.
func (st *runState) markStopped(name string) (already bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.stopped == nil {
		st.stopped = map[string]bool{}
	}
	already = st.stopped[name]
	st.stopped[name] = true
	if !already {
		st.changed[name] = st.elapsedMS()
	}
	return already
}

func (st *runState) markReleased(name string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.released[name] = true
}

// isReleased reports whether name's Stop already ran mid-run.
func (st *runState) isReleased(name string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.released[name]
}

func (st *runState) markAbandoned(name string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.abandoned[name] = true
}

// isAbandoned reports whether name was written off after its Stop
// overran.
func (st *runState) isAbandoned(name string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.abandoned[name]
}

// allFailed reports whether every service the run started has failed,
// which leaves nothing serving whatever each one's optionality.
func (st *runState) allFailed() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.started) == 0 {
		return false
	}
	for _, name := range st.started {
		if _, failed := st.failed[name]; !failed {
			return false
		}
	}
	return true
}

// requiredFailed reports whether a service the run depends on failed.
func (st *runState) requiredFailed() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for name := range st.failed {
		if !st.optional[name] {
			return true
		}
	}
	return false
}

// optionalNeverReadyLocked counts the optional services that failed
// without ever reporting ready. The caller holds st.mu.
func (st *runState) optionalNeverReadyLocked() int {
	n := 0
	for name := range st.failed {
		if st.optional[name] && !slices.Contains(st.ready, name) {
			n++
		}
	}
	return n
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

// Services implements [RunStatus] over the run's own record.
func (st *runState) Services() []ServiceStatus {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]ServiceStatus, 0, len(st.started))
	for _, name := range st.started {
		ss := ServiceStatus{
			Service:   name,
			State:     st.stateLocked(name),
			Optional:  st.optional[name],
			ElapsedMS: st.changed[name],
		}
		if err, failed := st.failed[name]; failed {
			ss.Reason = st.reasons[name]
			if err != nil {
				ss.Error = err.Error()
			}
		}
		out = append(out, ss)
	}
	return out
}

// The supervisor's run view reports detail, not only state.
var _ RunStatus = (*runState)(nil)
