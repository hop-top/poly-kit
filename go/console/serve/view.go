package serve

import (
	"context"
	"slices"
)

// ServiceState is where one service stands in a run, as the
// supervisor recorded it.
type ServiceState string

// The states a [RunView] reports.
const (
	// StateNotInRun is a name the run did not start: unregistered,
	// not selected, or not enabled. The supervisor has no opinion
	// about it, the same way [StartOrder] ignores a dependency
	// outside the selected set.
	StateNotInRun ServiceState = "not_in_run"
	// StateStarting is a service whose Start was invoked and that has
	// not yet reported ready.
	StateStarting ServiceState = "starting"
	// StateReady is a service that reported ready and has neither
	// failed nor returned since.
	StateReady ServiceState = "ready"
	// StateStopped is a service whose Start returned cleanly on its
	// own while the run went on.
	StateStopped ServiceState = "stopped"
	// StateFailed is a service that failed to start, missed its
	// readiness budget, or returned an error at runtime.
	StateFailed ServiceState = "failed"
)

// RunView is a read-only view of the supervisor's record of one run.
//
// A service reaches it through [RunViewFrom] on the context its Start
// receives. It is how a service that answers for other services —
// the api service's readiness route answering for its DependsOn list
// — reflects what the supervisor saw rather than only what each
// service's own Ready says: a service that crashed under the isolate
// failure policy is failed here even when its Ready was never reset.
//
// The view is live for the whole run and safe for concurrent use.
type RunView interface {
	// State reports name's state in this run.
	State(name string) ServiceState
}

type runViewKey struct{}

// ContextWithRunView returns ctx carrying v. The supervisor attaches
// its own view to the context every Start receives; a test driving a
// service outside a supervisor attaches one of its own.
func ContextWithRunView(ctx context.Context, v RunView) context.Context {
	return context.WithValue(ctx, runViewKey{}, v)
}

// RunViewFrom returns the view ctx carries. ok is false outside a
// supervised run.
func RunViewFrom(ctx context.Context) (RunView, bool) {
	v, ok := ctx.Value(runViewKey{}).(RunView)
	return v, ok && v != nil
}

// State implements [RunView] over the run's own record. Failure wins
// over every other record, because a service that reported ready and
// then crashed is still in the ready list.
func (st *runState) State(name string) ServiceState {
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, failed := st.failed[name]; failed {
		return StateFailed
	}
	if st.stopped[name] {
		return StateStopped
	}
	if slices.Contains(st.ready, name) {
		return StateReady
	}
	if slices.Contains(st.started, name) {
		return StateStarting
	}
	return StateNotInRun
}
