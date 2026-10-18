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

// ServiceStatus is one service's entry in the supervisor's record of a
// run, with the detail behind its state. It is the shape a status
// surface renders, readably or as JSON; its keys are the lifecycle
// event payload's, so a status reader and an event subscriber read the
// same words.
type ServiceStatus struct {
	// Service is the service identifier.
	Service string `json:"service"`

	// State is where the service stands, as [RunView.State] reports
	// it.
	State ServiceState `json:"state"`

	// Optional is true when the run does not depend on the service
	// ([Config].Optional).
	Optional bool `json:"optional,omitempty"`

	// Reason is the kind of failure, for a failed service: the value
	// its failed event carries under reason (start, ready_timeout,
	// runtime, stop, stop_timeout, escalated, unregistered). Empty
	// otherwise.
	Reason string `json:"reason,omitempty"`

	// Error is the failure text, for a failed service.
	Error string `json:"error,omitempty"`

	// ElapsedMS is milliseconds from the start of the run to the
	// service's latest recorded transition: started, ready, failed or
	// stopped.
	ElapsedMS int64 `json:"elapsed_ms"`
}

// RunStatus is the supervisor's [RunView] with the detail behind each
// state. The view the supervisor attaches to every Start's context
// implements it, so a service that answers status questions for the
// whole process — an adopter's own status endpoint — reads the same
// record [Result].Services carries when the run ends.
type RunStatus interface {
	RunView

	// Services returns every service the run started, in the order
	// it started them.
	Services() []ServiceStatus
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
	return st.stateLocked(name)
}

// stateLocked is State for a caller already holding st.mu.
func (st *runState) stateLocked(name string) ServiceState {
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
