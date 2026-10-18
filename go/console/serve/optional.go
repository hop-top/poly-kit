package serve

import (
	"context"
	"slices"
	"time"
)

// optionalSet returns the selected services whose resolved block marks
// them optional (serve-lifecycle.md §"Optional services").
func optionalSet(selected []string, configs map[string]Config) map[string]bool {
	out := make(map[string]bool)
	for _, name := range selected {
		if configs[name].Optional {
			out[name] = true
		}
	}
	return out
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

// release stops an optional service at the point it failed rather
// than at the end of the run, bounded by its own stop budget alone:
// the supervisor's total budget belongs to the shutdown sequence. The
// ordered stop then skips it, so it is stopped once per run.
func (s *Supervisor) release(ctx context.Context, name string, configs map[string]Config, st *runState, em *emitter) {
	svc, ok := s.reg.Lookup(name)
	if !ok {
		return
	}
	st.markReleased(name)
	s.stopOne(context.WithoutCancel(ctx), svc, name, stopBudget(configs, name), st, em)
}

// stopBudget is name's stop timeout, or the default.
func stopBudget(configs map[string]Config, name string) time.Duration {
	if c, ok := configs[name]; ok && c.StopTimeout > 0 {
		return c.StopTimeout
	}
	return DefaultStopTimeout
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
