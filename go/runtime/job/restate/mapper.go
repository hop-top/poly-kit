package restate

import (
	"encoding/json"
	"fmt"
	"time"

	"hop.top/kit/go/runtime/job"
)

// marshalPayload serializes a job payload to json.RawMessage.
func marshalPayload(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return b, nil
}

// EnqueueInput is the input envelope sent to the Restate virtual
// object "enqueue" handler.
type EnqueueInput struct {
	ID          string         `json:"id"`
	Queue       string         `json:"queue"`
	Type        string         `json:"type"`
	Payload     any            `json:"payload,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Priority    int            `json:"priority"`
	MaxAttempts int            `json:"max_attempts"`
	ScheduledAt *time.Time     `json:"scheduled_at,omitempty"`
}

// CompleteInput is sent to the "complete" handler.
type CompleteInput struct {
	Result any `json:"result,omitempty"`
}

// FailInput is sent to the "fail" handler.
type FailInput struct {
	Error string `json:"error"`
	Retry bool   `json:"retry"`
}

// toRestateInput converts EnqueueOpts + generated ID to an input for
// the Restate virtual object.
func toRestateInput(opts job.EnqueueOpts, id string) *EnqueueInput {
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	return &EnqueueInput{
		ID:          id,
		Queue:       opts.Queue,
		Type:        opts.Type,
		Payload:     opts.Payload,
		Metadata:    opts.Metadata,
		Priority:    opts.Priority,
		MaxAttempts: maxAttempts,
		ScheduledAt: opts.ScheduledAt,
	}
}

// jobStateKeys defines the Restate virtual object state keys used
// by the JobManager handlers.
//
// Virtual object state layout:
//
//	"job"        → json-encoded Job struct
//	"status"     → string status for quick reads
//	"expires_at" → RFC3339 expiry timestamp (when claimed)
//	"queue"      → queue name (for index lookups)
var jobStateKeys = struct {
	Job       string
	Status    string
	ExpiresAt string
	Queue     string
}{
	Job:       "job",
	Status:    "status",
	ExpiresAt: "expires_at",
	Queue:     "queue",
}
