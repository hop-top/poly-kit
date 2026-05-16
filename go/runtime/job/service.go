package job

import (
	"context"
	"time"
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
