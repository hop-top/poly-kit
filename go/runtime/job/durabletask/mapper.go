package durabletask

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/job"

	"github.com/google/uuid"
)

// schema defines the jobs table DDL.
const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id           TEXT PRIMARY KEY,
	queue        TEXT    NOT NULL,
	type         TEXT    NOT NULL,
	status       TEXT    NOT NULL DEFAULT 'pending',
	payload      BLOB,
	result       BLOB,
	error        TEXT    NOT NULL DEFAULT '',
	metadata     BLOB,
	claimed_by   TEXT    NOT NULL DEFAULT '',
	priority     INTEGER NOT NULL DEFAULT 0,
	attempts     INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 3,
	claim_ttl_ns INTEGER NOT NULL DEFAULT 0,
	backoff      BLOB,
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL,
	claimed_at   INTEGER,
	started_at   INTEGER,
	completed_at INTEGER,
	expires_at   INTEGER,
	scheduled_at INTEGER,
	next_run_at  INTEGER
);

CREATE INDEX IF NOT EXISTS idx_jobs_claim
	ON jobs (queue, status, scheduled_at, next_run_at, priority, created_at);
`

// generateID returns a new unique job ID.
func generateID() string {
	return "dtj_" + uuid.NewString()[:8]
}

// nullTime converts *time.Time to nullable int64 (nanoseconds).
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixNano()
}

// scanner abstracts sql.Row and sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanJob scans a full job row from sql.Rows.
func scanJob(rows *sql.Rows) (*job.Job, error) {
	return scanFrom(rows)
}

// scanJobRow scans a full job row from sql.Row.
func scanJobRow(row *sql.Row) (*job.Job, error) {
	return scanFrom(row)
}

func scanFrom(s scanner) (*job.Job, error) {
	var (
		j         job.Job
		status    string
		payload   []byte
		result    []byte
		errStr    string
		meta      []byte
		backoff   []byte
		createdAt int64
		updatedAt int64
		claimedAt sql.NullInt64
		startedAt sql.NullInt64
		completAt sql.NullInt64
		expiresAt sql.NullInt64
		schedAt   sql.NullInt64
		nextRunAt sql.NullInt64
	)

	err := s.Scan(
		&j.ID, &j.Queue, &j.Type, &status,
		&payload, &result, &errStr,
		&meta, &j.ClaimedBy, &j.Priority,
		&j.Attempts, &j.MaxAttempts,
		&backoff,
		&createdAt, &updatedAt,
		&claimedAt, &startedAt, &completAt,
		&expiresAt, &schedAt, &nextRunAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("durabletask: scan: %w", err)
	}

	j.Status = job.Status(status)
	j.Payload = json.RawMessage(payload)
	j.Result = json.RawMessage(result)
	j.Error = errStr

	if meta != nil {
		if uerr := json.Unmarshal(meta, &j.Metadata); uerr != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", uerr)
		}
	}

	if backoff != nil {
		if uerr := json.Unmarshal(backoff, &j.Backoff); uerr != nil {
			return nil, fmt.Errorf("unmarshal backoff: %w", uerr)
		}
	}

	j.CreatedAt = time.Unix(0, createdAt).UTC()
	j.UpdatedAt = time.Unix(0, updatedAt).UTC()
	j.ClaimedAt = nanoToTime(claimedAt)
	j.StartedAt = nanoToTime(startedAt)
	j.CompletedAt = nanoToTime(completAt)
	j.ExpiresAt = nanoToTime(expiresAt)
	j.ScheduledAt = nanoToTime(schedAt)
	j.NextRunAt = nanoToTime(nextRunAt)

	return &j, nil
}

// nanoToTime converts a nullable int64 (nanoseconds) to *time.Time.
func nanoToTime(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.Unix(0, n.Int64).UTC()
	return &t
}
