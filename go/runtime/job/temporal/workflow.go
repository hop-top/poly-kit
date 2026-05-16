// Package temporal provides a job.Service backed by Temporal workflows.
//
// Each job is modeled as a single workflow instance:
//
//   - Enqueue  → ExecuteWorkflow (starts JobWorkflow)
//   - Claim    → SignalWorkflow("claim", workerID)
//   - Complete → SignalWorkflow("complete", result)
//   - Fail     → SignalWorkflow("fail", failOpts)
//   - Timeout  → SignalWorkflow("timeout")
//   - Cancel   → SignalWorkflow("cancel")
//   - Get      → QueryWorkflow("state")
//
// The workflow maintains a JobState struct that mirrors job.Job fields.
// Signals drive state transitions; the state machine is validated
// inside the workflow.
package temporal

import (
	"encoding/json"
	"time"

	"go.temporal.io/sdk/workflow"

	"hop.top/kit/go/runtime/job"
)

// Signal names for workflow communication.
const (
	SignalClaim    = "claim"
	SignalComplete = "complete"
	SignalFail     = "fail"
	SignalTimeout  = "timeout"
	SignalCancel   = "cancel"
	SignalHbeat    = "heartbeat"
)

// QueryState is the query type for reading current job state.
const QueryState = "state"

// defaultClaimTTL is the default duration before an uncompleted claim
// is auto-timed-out by the workflow timer.
const defaultClaimTTL = 5 * time.Minute

// ClaimSignal carries claim information.
type ClaimSignal struct {
	WorkerID string `json:"worker_id"`
}

// CompleteSignal carries completion data.
type CompleteSignal struct {
	Result json.RawMessage `json:"result,omitempty"`
}

// FailSignal carries failure data.
type FailSignal struct {
	Error string `json:"error"`
	Retry bool   `json:"retry"`
}

// JobState is the workflow-internal representation of a job.
type JobState struct {
	ID          string          `json:"id"`
	Queue       string          `json:"queue"`
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
	ClaimedBy   string          `json:"claimed_by,omitempty"`
	Priority    int             `json:"priority"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	ClaimedAt   *time.Time      `json:"claimed_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	ScheduledAt *time.Time      `json:"scheduled_at,omitempty"`
}

// JobWorkflowInput is the input for starting a job workflow.
type JobWorkflowInput struct {
	Queue       string          `json:"queue"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
	Priority    int             `json:"priority"`
	MaxAttempts int             `json:"max_attempts"`
	ClaimTTL    time.Duration   `json:"claim_ttl,omitempty"`
	ScheduledAt *time.Time      `json:"scheduled_at,omitempty"`
}

// JobWorkflow is the Temporal workflow that models a job's lifecycle.
// It blocks on signals to drive state transitions and terminates
// when the job reaches a terminal state.
func JobWorkflow(ctx workflow.Context, input JobWorkflowInput) (JobState, error) {
	info := workflow.GetInfo(ctx)
	now := workflow.Now(ctx)

	maxAttempts := input.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	state := JobState{
		ID:          info.WorkflowExecution.ID,
		Queue:       input.Queue,
		Type:        input.Type,
		Status:      string(job.StatusPending),
		Payload:     input.Payload,
		Metadata:    input.Metadata,
		Priority:    input.Priority,
		MaxAttempts: maxAttempts,
		CreatedAt:   now,
		UpdatedAt:   now,
		ScheduledAt: input.ScheduledAt,
	}

	// Register query handler for state reads.
	if err := workflow.SetQueryHandler(ctx, QueryState,
		func() (JobState, error) { return state, nil },
	); err != nil {
		return state, err
	}

	claimTTL := input.ClaimTTL
	if claimTTL <= 0 {
		claimTTL = defaultClaimTTL
	}

	// Signal channels.
	claimCh := workflow.GetSignalChannel(ctx, SignalClaim)
	completeCh := workflow.GetSignalChannel(ctx, SignalComplete)
	failCh := workflow.GetSignalChannel(ctx, SignalFail)
	timeoutCh := workflow.GetSignalChannel(ctx, SignalTimeout)
	cancelCh := workflow.GetSignalChannel(ctx, SignalCancel)
	hbeatCh := workflow.GetSignalChannel(ctx, SignalHbeat)

	// claimDeadline tracks when the current claim expires.
	// Reset on claim and heartbeat; checked via timer.
	var claimDeadline time.Time

	// Main loop: process signals until terminal state.
	for !isTerminal(state.Status) {
		sel := workflow.NewSelector(ctx)

		sel.AddReceive(claimCh, func(c workflow.ReceiveChannel, _ bool) {
			var sig ClaimSignal
			c.Receive(ctx, &sig)
			if state.Status != string(job.StatusPending) {
				return
			}
			t := workflow.Now(ctx)
			// Reject claim if ScheduledAt is in the future.
			if state.ScheduledAt != nil && state.ScheduledAt.After(t) {
				return
			}
			state.Status = string(job.StatusActive)
			state.ClaimedBy = sig.WorkerID
			state.ClaimedAt = &t
			state.Attempts++
			state.UpdatedAt = t
			claimDeadline = t.Add(claimTTL)
			upsertStatus(ctx, state.Status)
		})

		sel.AddReceive(completeCh, func(c workflow.ReceiveChannel, _ bool) {
			var sig CompleteSignal
			c.Receive(ctx, &sig)
			if state.Status != string(job.StatusActive) {
				return
			}
			t := workflow.Now(ctx)
			state.Status = string(job.StatusSucceeded)
			state.Result = sig.Result
			state.CompletedAt = &t
			state.UpdatedAt = t
			upsertStatus(ctx, state.Status)
		})

		sel.AddReceive(failCh, func(c workflow.ReceiveChannel, _ bool) {
			var sig FailSignal
			c.Receive(ctx, &sig)
			if state.Status != string(job.StatusActive) {
				return
			}
			t := workflow.Now(ctx)
			state.Error = sig.Error

			canRetry := sig.Retry && state.Attempts < state.MaxAttempts
			if canRetry {
				state.Status = string(job.StatusPending)
				state.ClaimedBy = ""
				state.ClaimedAt = nil
			} else {
				state.Status = string(job.StatusFailed)
				state.CompletedAt = &t
			}
			state.UpdatedAt = t
			upsertStatus(ctx, state.Status)
		})

		sel.AddReceive(timeoutCh, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, nil)
			if state.Status != string(job.StatusActive) {
				return
			}
			t := workflow.Now(ctx)
			state.Status = string(job.StatusTimeout)
			state.CompletedAt = &t
			state.UpdatedAt = t
			upsertStatus(ctx, state.Status)
		})

		sel.AddReceive(cancelCh, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, nil)
			if isTerminal(state.Status) {
				return
			}
			t := workflow.Now(ctx)
			state.Status = string(job.StatusCancelled)
			state.CompletedAt = &t
			state.UpdatedAt = t
			upsertStatus(ctx, state.Status)
		})

		sel.AddReceive(hbeatCh, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, nil)
			if state.Status == string(job.StatusActive) {
				t := workflow.Now(ctx)
				state.UpdatedAt = t
				claimDeadline = t.Add(claimTTL)
			}
		})

		// Claim TTL timer: auto-timeout stale claims.
		if state.Status == string(job.StatusActive) &&
			!claimDeadline.IsZero() {
			remaining := claimDeadline.Sub(workflow.Now(ctx))
			if remaining <= 0 {
				remaining = time.Millisecond
			}
			tmr := workflow.NewTimer(ctx, remaining)
			sel.AddFuture(tmr, func(f workflow.Future) {
				_ = f.Get(ctx, nil)
				if state.Status != string(job.StatusActive) {
					return
				}
				t := workflow.Now(ctx)
				// Retry if attempts allow; otherwise terminal.
				if state.Attempts < state.MaxAttempts {
					state.Status = string(job.StatusPending)
					state.ClaimedBy = ""
					state.ClaimedAt = nil
					claimDeadline = time.Time{}
				} else {
					state.Status = string(job.StatusTimeout)
					state.CompletedAt = &t
				}
				state.UpdatedAt = t
				upsertStatus(ctx, state.Status)
			})
		}

		sel.Select(ctx)
	}

	return state, nil
}

// upsertStatus updates the JobStatus search attribute for visibility.
func upsertStatus(ctx workflow.Context, status string) {
	_ = workflow.UpsertTypedSearchAttributes(ctx, SAStatus.ValueSet(status))
}

// isTerminal checks if a status is terminal.
func isTerminal(s string) bool {
	switch job.Status(s) {
	case job.StatusSucceeded, job.StatusFailed,
		job.StatusTimeout, job.StatusCancelled:
		return true
	}
	return false
}
