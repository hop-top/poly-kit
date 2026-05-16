package job

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
