package job

import (
	"context"

	"hop.top/kit/go/runtime/domain"
)

// JobEvent is the payload published for job lifecycle events.
type JobEvent struct {
	JobID    string `json:"job_id"`
	Queue    string `json:"queue"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	WorkerID string `json:"worker_id,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Event topic constants.
const (
	TopicCreated       = "job.created"
	TopicClaimed       = "job.claimed"
	TopicSucceeded     = "job.succeeded"
	TopicFailed        = "job.failed"
	TopicTimeout       = "job.timeout"
	TopicRetried       = "job.retried"
	TopicCancelled     = "job.cancelled"
	TopicStaleReleased = "job.stale_released"
	TopicDeadLetter    = "job.dead_letter"
	TopicHeartbeat     = "job.heartbeat"
)

// EventSource is the source string engines pass to
// domain.EventPublisher.Publish for job lifecycle events.
const EventSource = "kit.runtime.job"

// PublishEvent publishes a JobEvent for j on topic. It is a no-op when
// pub is nil. Lifecycle events are fire-and-forget: a publisher error
// never fails or rolls back the operation that raised the event. errMsg
// is carried in JobEvent.Error (empty for non-failure events).
func PublishEvent(
	ctx context.Context, pub domain.EventPublisher, topic string,
	j Job, errMsg string,
) {
	if pub == nil {
		return
	}
	_ = pub.Publish(ctx, topic, EventSource, JobEvent{
		JobID:    j.ID,
		Queue:    j.Queue,
		Type:     j.Type,
		Status:   string(j.Status),
		WorkerID: j.ClaimedBy,
		Error:    errMsg,
	})
}
