package hatchet

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

// statusToHatchet maps a job.Status to a Hatchet workflow run status
// string. Values match Hatchet's stable API contract: PENDING,
// RUNNING, SUCCEEDED, FAILED, CANCELED, TIMED_OUT. Unmapped
// statuses fall back to "UNKNOWN" (not part of the contract).
func statusToHatchet(s job.Status) string {
	switch s {
	case job.StatusPending:
		return "PENDING"
	case job.StatusActive:
		return "RUNNING"
	case job.StatusSucceeded:
		return "SUCCEEDED"
	case job.StatusFailed:
		return "FAILED"
	case job.StatusTimeout:
		return "TIMED_OUT"
	case job.StatusCancelled:
		return "CANCELED"
	default:
		return "UNKNOWN"
	}
}

// statusFromHatchet maps a Hatchet workflow run status string back to
// job.Status.
func statusFromHatchet(s string) job.Status {
	switch s {
	case "PENDING", "QUEUED":
		return job.StatusPending
	case "RUNNING":
		return job.StatusActive
	case "SUCCEEDED":
		return job.StatusSucceeded
	case "FAILED":
		return job.StatusFailed
	case "TIMED_OUT":
		return job.StatusTimeout
	case "CANCELED":
		return job.StatusCancelled
	default:
		return job.StatusPending
	}
}

// toJobInput builds the Hatchet workflow input envelope from
// EnqueueOpts.
func toJobInput(opts job.EnqueueOpts) map[string]any {
	m := map[string]any{
		"queue":    opts.Queue,
		"type":     opts.Type,
		"payload":  opts.Payload,
		"metadata": opts.Metadata,
		"priority": opts.Priority,
	}
	if opts.ScheduledAt != nil {
		m["scheduled_at"] = opts.ScheduledAt.Format(time.RFC3339)
	}
	if opts.MaxAttempts > 0 {
		m["max_attempts"] = opts.MaxAttempts
	}
	return m
}

// fromHatchetRun builds a job.Job from Hatchet run metadata fields.
// CreatedAt/UpdatedAt default to now; callers may overwrite with
// values from the API response when available.
func fromHatchetRun(runID, queue, jobType, status string) *job.Job {
	now := time.Now()
	return &job.Job{
		ID:        runID,
		Queue:     queue,
		Type:      jobType,
		Status:    statusFromHatchet(status),
		CreatedAt: now,
		UpdatedAt: now,
	}
}
