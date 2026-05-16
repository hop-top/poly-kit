package temporal

import (
	"encoding/json"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/temporal"

	"hop.top/kit/go/runtime/job"
)

// stateToJob converts a workflow JobState to a job.Job.
func stateToJob(s JobState) *job.Job {
	return &job.Job{
		ID:          s.ID,
		Queue:       s.Queue,
		Type:        s.Type,
		Status:      job.Status(s.Status),
		Payload:     s.Payload,
		Result:      s.Result,
		Error:       s.Error,
		Metadata:    s.Metadata,
		ClaimedBy:   s.ClaimedBy,
		Priority:    s.Priority,
		Attempts:    s.Attempts,
		MaxAttempts: s.MaxAttempts,
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
		ClaimedAt:   s.ClaimedAt,
		CompletedAt: s.CompletedAt,
		ScheduledAt: s.ScheduledAt,
	}
}

// marshalPayload marshals an arbitrary value into json.RawMessage.
func marshalPayload(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// TaskQueue is the default task queue name for job workflows.
const TaskQueue = "job-queue"

// WorkflowIDPrefix for job workflow IDs.
const WorkflowIDPrefix = "job-"

// Search attribute keys for visibility queries.
const (
	SAKeyQueue  = "JobQueue"
	SAKeyStatus = "JobStatus"
	SAKeyType   = "JobType"
)

// Typed search attribute keys.
var (
	SAQueue  = temporal.NewSearchAttributeKeyKeyword(SAKeyQueue)
	SAStatus = temporal.NewSearchAttributeKeyKeyword(SAKeyStatus)
	SAType   = temporal.NewSearchAttributeKeyKeyword(SAKeyType)
)

// decodeSearchAttr extracts a string value from search attributes.
func decodeSearchAttr(
	sa *commonpb.SearchAttributes, key string,
) string {
	if sa == nil || sa.IndexedFields == nil {
		return ""
	}
	p, ok := sa.IndexedFields[key]
	if !ok || p == nil {
		return ""
	}
	// Temporal encodes search attribute values as JSON payloads.
	var s string
	if err := json.Unmarshal(p.Data, &s); err != nil {
		return ""
	}
	return s
}
