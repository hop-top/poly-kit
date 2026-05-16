package temporal

import (
	"context"
	"encoding/json"
	"fmt"

	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/job"
)

// Engine implements job.Service by mapping each job to a Temporal workflow.
//
// Limitations vs a native queue-based backend:
//   - Claim is modeled as a signal, not a competing consumer poll.
//     The caller must know the workflow/job ID to claim it. For FIFO
//     claim-from-queue semantics, use List + signal.
//   - List uses workflow visibility (search attributes). Basic
//     filtering is supported; complex queries need the advanced
//     visibility store.
//   - ReleaseStaleClaims is a no-op: Temporal handles timeouts via
//     workflow timers and activity heartbeat.
//   - Heartbeat extends the workflow's notion of liveness but does
//     not interact with Temporal's activity heartbeat mechanism.
type Engine struct {
	client  client.Client
	pub     domain.EventPublisher
	nowFunc func() interface{ UnixNano() int64 }
}

// New creates a Temporal-backed job engine.
func New(c client.Client, opts ...job.Option) *Engine {
	cfg := job.BuildConfig(opts)
	_ = cfg.NowFunc // available but Temporal controls time inside workflows
	return &Engine{
		client: c,
		pub:    cfg.Publisher,
	}
}

// Enqueue starts a new job workflow.
func (e *Engine) Enqueue(
	ctx context.Context, opts job.EnqueueOpts,
) (string, error) {
	if opts.Queue == "" {
		return "", fmt.Errorf("%w: queue is required", domain.ErrValidation)
	}
	if opts.Type == "" {
		return "", fmt.Errorf("%w: type is required", domain.ErrValidation)
	}

	payload, err := marshalPayload(opts.Payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	input := JobWorkflowInput{
		Queue:       opts.Queue,
		Type:        opts.Type,
		Payload:     payload,
		Metadata:    opts.Metadata,
		Priority:    opts.Priority,
		MaxAttempts: maxAttempts,
		ClaimTTL:    opts.ClaimTTL,
		ScheduledAt: opts.ScheduledAt,
	}

	run, err := e.client.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue: opts.Queue,
			TypedSearchAttributes: temporal.NewSearchAttributes(
				SAQueue.ValueSet(opts.Queue),
				SAStatus.ValueSet(string(job.StatusPending)),
				SAType.ValueSet(opts.Type),
			),
		},
		JobWorkflow,
		input,
	)
	if err != nil {
		return "", fmt.Errorf("temporal: start workflow: %w", err)
	}

	return run.GetID(), nil
}

// Claim finds the next pending job in the queue via List, then claims it.
// Returns nil if no pending jobs are available.
func (e *Engine) Claim(
	ctx context.Context, queue, workerID string,
) (*job.Job, error) {
	pending, err := e.List(ctx, job.JobQuery{
		Queue:  queue,
		Status: string(job.StatusPending),
		Limit:  1,
	})
	if err != nil {
		return nil, fmt.Errorf("temporal: claim list: %w", err)
	}
	if len(pending) == 0 {
		return nil, nil
	}

	return e.ClaimByID(ctx, pending[0].ID, workerID)
}

// ClaimByID signals a specific job workflow to be claimed.
func (e *Engine) ClaimByID(
	ctx context.Context, id, workerID string,
) (*job.Job, error) {
	err := e.client.SignalWorkflow(ctx,
		id, "", SignalClaim,
		ClaimSignal{WorkerID: workerID},
	)
	if err != nil {
		return nil, fmt.Errorf("temporal: signal claim: %w", err)
	}

	return e.Get(ctx, id)
}

// Complete signals the job workflow with a result.
func (e *Engine) Complete(
	ctx context.Context, id string, result any,
) error {
	var res json.RawMessage
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal result: %w", err)
		}
		res = b
	}

	return e.client.SignalWorkflow(ctx,
		id, "", SignalComplete,
		CompleteSignal{Result: res},
	)
}

// Fail signals the job workflow with a failure.
func (e *Engine) Fail(
	ctx context.Context, id string, opts job.FailOpts,
) error {
	return e.client.SignalWorkflow(ctx,
		id, "", SignalFail,
		FailSignal{Error: opts.Error, Retry: opts.Retry},
	)
}

// Timeout signals the job workflow as timed out.
func (e *Engine) Timeout(ctx context.Context, id string) error {
	return e.client.SignalWorkflow(ctx, id, "", SignalTimeout, nil)
}

// Cancel requests cancellation of the job workflow.
func (e *Engine) Cancel(ctx context.Context, id string) error {
	return e.client.SignalWorkflow(ctx, id, "", SignalCancel, nil)
}

// Heartbeat signals the workflow that the worker is still alive.
func (e *Engine) Heartbeat(ctx context.Context, id string) error {
	return e.client.SignalWorkflow(ctx, id, "", SignalHbeat, nil)
}

// Get queries the workflow for current job state.
func (e *Engine) Get(ctx context.Context, id string) (*job.Job, error) {
	val, err := e.client.QueryWorkflow(ctx, id, "", QueryState)
	if err != nil {
		return nil, fmt.Errorf("temporal: query state: %w", err)
	}

	var state JobState
	if err := val.Get(&state); err != nil {
		return nil, fmt.Errorf("temporal: decode state: %w", err)
	}

	return stateToJob(state), nil
}

// List queries Temporal visibility for matching workflows.
// Uses search attributes (JobQueue, JobStatus, JobType) for
// server-side filtering, avoiding per-workflow queries (N+1).
// Only Get fetches full state via QueryWorkflow.
func (e *Engine) List(
	ctx context.Context, q job.JobQuery,
) ([]job.Job, error) {
	// Build visibility query from search attributes.
	visQuery := buildVisibilityQuery(q)

	pageSize := int32(100)
	if q.Limit > 0 && q.Limit < 100 {
		pageSize = int32(q.Limit)
	}

	resp, err := e.client.ListWorkflow(ctx,
		&workflowservice.ListWorkflowExecutionsRequest{
			PageSize: pageSize,
			Query:    visQuery,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("temporal: list: %w", err)
	}

	var jobs []job.Job
	for _, exec := range resp.Executions {
		// Construct minimal Job from visibility data.
		j := job.Job{
			ID:        exec.Execution.WorkflowId,
			CreatedAt: exec.StartTime.AsTime(),
		}

		// Read search attributes for queue/status/type.
		if sa := exec.SearchAttributes; sa != nil {
			j.Queue = decodeSearchAttr(sa, SAKeyQueue)
			j.Status = job.Status(decodeSearchAttr(sa, SAKeyStatus))
			j.Type = decodeSearchAttr(sa, SAKeyType)
		}

		jobs = append(jobs, j)
		if q.Limit > 0 && len(jobs) >= q.Limit {
			break
		}
	}

	return jobs, nil
}

// buildVisibilityQuery constructs a Temporal visibility query string
// from JobQuery filters using search attributes.
func buildVisibilityQuery(q job.JobQuery) string {
	var parts []string
	if q.Queue != "" {
		parts = append(parts,
			fmt.Sprintf("%s = '%s'", SAKeyQueue, q.Queue))
	}
	if q.Status != "" {
		parts = append(parts,
			fmt.Sprintf("%s = '%s'", SAKeyStatus, q.Status))
	}
	if q.Type != "" {
		parts = append(parts,
			fmt.Sprintf("%s = '%s'", SAKeyType, q.Type))
	}
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += " AND " + p
	}
	return result
}

// ReleaseStaleClaims is a no-op for the Temporal adapter.
// Temporal handles timeouts via workflow timers and activity
// heartbeat mechanisms natively.
func (e *Engine) ReleaseStaleClaims(_ context.Context) (int, error) {
	return 0, nil
}
