package job

import (
	"encoding/json"
	"time"
)

// Job represents a unit of asynchronous work.
type Job struct {
	ID       string          `json:"id"`
	Queue    string          `json:"queue"`
	Type     string          `json:"type"`
	Status   Status          `json:"status"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	Metadata map[string]any  `json:"metadata,omitempty"`

	ClaimedBy string `json:"claimed_by,omitempty"`
	Priority  int    `json:"priority"`

	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	Backoff     BackoffStrategy `json:"backoff,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	ClaimedAt   *time.Time `json:"claimed_at,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	NextRunAt   *time.Time `json:"next_run_at,omitempty"`
}

// GetID implements domain.Entity.
func (j Job) GetID() string { return j.ID }
