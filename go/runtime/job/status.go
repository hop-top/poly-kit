package job

import "hop.top/kit/go/runtime/domain"

// Status represents the lifecycle state of a job.
type Status string

const (
	StatusPending   Status = "pending"
	StatusActive    Status = "active"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusTimeout   Status = "timeout"
	StatusCancelled Status = "canceled"
)

// transitionRules defines allowed state transitions for jobs.
//
//	pending   → active, canceled
//	active    → succeeded, failed, timeout, pending, canceled
//	failed    → pending (retry)
//	timeout   → pending (retry)
var transitionRules = map[domain.State][]domain.State{
	domain.State(StatusPending): {
		domain.State(StatusActive),
		domain.State(StatusCancelled),
	},
	domain.State(StatusActive): {
		domain.State(StatusSucceeded),
		domain.State(StatusFailed),
		domain.State(StatusTimeout),
		domain.State(StatusPending),
		domain.State(StatusCancelled),
	},
	domain.State(StatusFailed): {
		domain.State(StatusPending),
	},
	domain.State(StatusTimeout): {
		domain.State(StatusPending),
	},
}

// NewStateMachine returns a domain.StateMachine configured with job
// transition rules.
func NewStateMachine(pub domain.EventPublisher) *domain.StateMachine {
	return domain.NewStateMachine(transitionRules, pub)
}
