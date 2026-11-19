package job

import (
	"context"
	"fmt"
	"time"

	"hop.top/kit/go/runtime/domain"
)

// Service defines operations for managing asynchronous jobs.
type Service interface {
	// Enqueue creates a new pending job.
	Enqueue(ctx context.Context, opts EnqueueOpts) (string, error)

	// Claim atomically claims the next available job from a queue.
	Claim(ctx context.Context, queue, workerID string) (*Job, error)

	// Complete marks a job as succeeded with an optional result.
	Complete(ctx context.Context, id string, result any) error

	// Fail marks a job as failed, optionally scheduling a retry.
	Fail(ctx context.Context, id string, opts FailOpts) error

	// Timeout marks a job as timed out.
	Timeout(ctx context.Context, id string) error

	// Cancel marks a pending or active job as canceled.
	Cancel(ctx context.Context, id string) error

	// Heartbeat extends the expiry of an active job's claim.
	Heartbeat(ctx context.Context, id string) error

	// Get retrieves a job by ID.
	Get(ctx context.Context, id string) (*Job, error)

	// List returns jobs matching the query.
	List(ctx context.Context, q JobQuery) ([]Job, error)

	// ReleaseStaleClaims returns stale active jobs to pending.
	ReleaseStaleClaims(ctx context.Context) (int, error)
}

// Pruner is implemented by Services that can delete finished jobs.
// It is separate from Service: server-backed engines own retention
// themselves. Kit supplies the call only; scheduling and policy
// belong to the caller.
type Pruner interface {
	// Prune deletes finished jobs last updated before olderThan and
	// returns the number deleted. With no statuses it considers every
	// status in FinishedStatuses; otherwise only the listed ones. A
	// status that is not finished (pending, active) is rejected with
	// domain.ErrValidation and nothing is deleted. Safe to repeat.
	Prune(ctx context.Context, olderThan time.Time, statuses ...Status) (int, error)
}

// EnqueueOpts are the parameters for creating a new job.
type EnqueueOpts struct {
	Queue       string
	Type        string
	Payload     any
	Metadata    map[string]any
	Priority    int
	MaxAttempts int
	ClaimTTL    time.Duration
	ScheduledAt *time.Time
	Backoff     BackoffStrategy
}

// FailOpts controls failure behavior.
type FailOpts struct {
	Error string
	Retry bool // false = force terminal failure
}

// JobQuery filters for listing jobs.
type JobQuery struct {
	Queue  string
	Status string
	Type   string
	Limit  int
}

// PruneStatuses resolves the status filter of [Pruner.Prune]: empty
// means all finished statuses; a non-finished status is an error.
// Engines share it so the safety rule lives in one place.
func PruneStatuses(in []Status) ([]Status, error) {
	if len(in) == 0 {
		return FinishedStatuses(), nil
	}
	for _, s := range in {
		if !s.IsFinished() {
			return nil, fmt.Errorf(
				"%w: cannot prune %q jobs", domain.ErrValidation, s,
			)
		}
	}
	return in, nil
}
