// Package durabletask provides a job.Service backed by SQLite.
// Uses a custom "jobs" table with FIFO claim semantics, priority
// ordering, backoff scheduling, and state machine enforcement.
package durabletask

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/job"
	"hop.top/kit/go/storage/sqldb"
)

const defaultClaimTTL = 5 * time.Minute

// Engine implements job.Service using SQLite for durable storage.
type Engine struct {
	db       *sql.DB
	mu       sync.Mutex // serialize claim to guarantee exactly-one
	sm       *domain.StateMachine
	pub      domain.EventPublisher
	nowFunc  func() time.Time
	claimTTL time.Duration
}

// New creates a new Engine backed by a SQLite database at dbPath.
// Pass "" for an in-memory database (useful for testing).
func New(dbPath string, opts ...job.Option) (*Engine, error) {
	cfg := job.BuildConfig(opts)
	path := ":memory:"
	if dbPath != "" {
		path = dbPath
	}
	db, err := sqldb.Open(sqldb.Options{Path: path})
	if err != nil {
		return nil, fmt.Errorf("durabletask: open db: %w", err)
	}
	db.SetMaxOpenConns(1) // single conn avoids SQLite locking
	e := &Engine{
		db:       db,
		sm:       job.NewStateMachine(cfg.Publisher),
		pub:      cfg.Publisher,
		nowFunc:  cfg.NowFunc,
		claimTTL: defaultClaimTTL,
	}

	if err := e.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("durabletask: migrate: %w", err)
	}

	return e, nil
}

// Close releases the database connection.
func (e *Engine) Close() error { return e.db.Close() }

// migrate creates the jobs table if it doesn't exist.
func (e *Engine) migrate() error {
	_, err := e.db.Exec(schema)
	return err
}

// Enqueue creates a new pending job.
func (e *Engine) Enqueue(
	ctx context.Context, opts job.EnqueueOpts,
) (string, error) {
	if opts.Queue == "" {
		return "", fmt.Errorf("%w: queue is required", domain.ErrValidation)
	}
	if opts.Type == "" {
		return "", fmt.Errorf("%w: type is required", domain.ErrValidation)
	}

	now := e.nowFunc()
	id := generateID()
	var payload []byte
	if opts.Payload != nil {
		b, err := json.Marshal(opts.Payload)
		if err != nil {
			return "", fmt.Errorf("marshal payload: %w", err)
		}
		payload = b
	}
	var meta []byte
	if opts.Metadata != nil {
		b, err := json.Marshal(opts.Metadata)
		if err != nil {
			return "", fmt.Errorf("marshal metadata: %w", err)
		}
		meta = b
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	claimTTL := opts.ClaimTTL
	if claimTTL == 0 {
		claimTTL = e.claimTTL
	}
	var backoffData []byte
	if opts.Backoff != (job.BackoffStrategy{}) {
		b, berr := json.Marshal(opts.Backoff)
		if berr != nil {
			return "", fmt.Errorf("marshal backoff: %w", berr)
		}
		backoffData = b
	}

	_, err := e.db.ExecContext(ctx,
		`INSERT INTO jobs (
			id, queue, type, status, payload, metadata,
			priority, max_attempts, claim_ttl_ns, backoff,
			scheduled_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, opts.Queue, opts.Type, string(job.StatusPending),
		payload, meta,
		opts.Priority, maxAttempts, int64(claimTTL), backoffData,
		nullTime(opts.ScheduledAt), now.UnixNano(), now.UnixNano(),
	)
	if err != nil {
		return "", fmt.Errorf("durabletask: insert job: %w", err)
	}

	return id, nil
}

// Claim atomically claims the next available job from a queue.
// Priority ascending, then created_at ascending (FIFO within priority).
func (e *Engine) Claim(
	ctx context.Context, queue, workerID string,
) (*job.Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.nowFunc()
	nowNano := now.UnixNano()

	row := e.db.QueryRowContext(ctx,
		`SELECT id FROM jobs
		 WHERE queue = ? AND status = ?
		   AND (scheduled_at IS NULL OR scheduled_at <= ?)
		   AND (next_run_at  IS NULL OR next_run_at  <= ?)
		 ORDER BY priority ASC, created_at ASC
		 LIMIT 1`,
		queue, string(job.StatusPending), nowNano, nowNano,
	)

	var id string
	if err := row.Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("durabletask: scan claim: %w", err)
	}

	// Load full job to validate transition.
	j, err := e.getUnlocked(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := e.sm.Transition(ctx,
		domain.State(j.Status),
		domain.State(job.StatusActive),
		false,
	); err != nil {
		return nil, err
	}

	// Use per-job claim TTL from the row; fall back to engine-wide TTL.
	claimTTL := e.jobClaimTTL(ctx, id)
	exp := now.Add(claimTTL)

	_, err = e.db.ExecContext(ctx,
		`UPDATE jobs SET
			status = ?, claimed_by = ?, claimed_at = ?,
			started_at = ?, expires_at = ?,
			attempts = attempts + 1, updated_at = ?
		 WHERE id = ?`,
		string(job.StatusActive), workerID, nowNano,
		nowNano, exp.UnixNano(),
		nowNano, id,
	)
	if err != nil {
		return nil, fmt.Errorf("durabletask: update claim: %w", err)
	}

	return e.getUnlocked(ctx, id)
}

// Complete marks a job as succeeded.
func (e *Engine) Complete(
	ctx context.Context, id string, result any,
) error {
	j, err := e.Get(ctx, id)
	if err != nil {
		return err
	}

	if err := e.sm.Transition(ctx,
		domain.State(j.Status),
		domain.State(job.StatusSucceeded),
		false,
	); err != nil {
		return err
	}

	var res []byte
	if result != nil {
		b, merr := json.Marshal(result)
		if merr != nil {
			return fmt.Errorf("marshal result: %w", merr)
		}
		res = b
	}

	now := e.nowFunc()
	_, err = e.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, result = ?,
			completed_at = ?, updated_at = ?
		 WHERE id = ?`,
		string(job.StatusSucceeded), res,
		now.UnixNano(), now.UnixNano(), id,
	)
	return err
}

// Fail marks a job as failed, optionally scheduling a retry.
func (e *Engine) Fail(
	ctx context.Context, id string, opts job.FailOpts,
) error {
	j, err := e.Get(ctx, id)
	if err != nil {
		return err
	}

	canRetry := opts.Retry && j.Attempts < j.MaxAttempts
	now := e.nowFunc()

	if canRetry {
		if err := e.sm.Transition(ctx,
			domain.State(j.Status),
			domain.State(job.StatusPending),
			false,
		); err != nil {
			return err
		}

		bo := j.Backoff
		if bo == (job.BackoffStrategy{}) {
			bo = job.DefaultBackoff()
		}
		backoff := bo.Compute(j.Attempts - 1)
		nextRun := now.Add(backoff)

		_, err = e.db.ExecContext(ctx,
			`UPDATE jobs SET
				status = ?, error = ?, claimed_by = '',
				claimed_at = NULL, started_at = NULL,
				expires_at = NULL, next_run_at = ?,
				updated_at = ?
			 WHERE id = ?`,
			string(job.StatusPending), opts.Error,
			nextRun.UnixNano(), now.UnixNano(), id,
		)
		return err
	}

	// Terminal failure.
	if err := e.sm.Transition(ctx,
		domain.State(j.Status),
		domain.State(job.StatusFailed),
		false,
	); err != nil {
		return err
	}

	_, err = e.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, error = ?,
			completed_at = ?, updated_at = ?
		 WHERE id = ?`,
		string(job.StatusFailed), opts.Error,
		now.UnixNano(), now.UnixNano(), id,
	)
	return err
}

// Timeout marks a job as timed out.
func (e *Engine) Timeout(ctx context.Context, id string) error {
	j, err := e.Get(ctx, id)
	if err != nil {
		return err
	}

	if err := e.sm.Transition(ctx,
		domain.State(j.Status),
		domain.State(job.StatusTimeout),
		false,
	); err != nil {
		return err
	}

	now := e.nowFunc()
	_, err = e.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, completed_at = ?, updated_at = ?
		 WHERE id = ?`,
		string(job.StatusTimeout), now.UnixNano(), now.UnixNano(), id,
	)
	return err
}

// Cancel marks a job as canceled.
func (e *Engine) Cancel(ctx context.Context, id string) error {
	j, err := e.Get(ctx, id)
	if err != nil {
		return err
	}

	if err := e.sm.Transition(ctx,
		domain.State(j.Status),
		domain.State(job.StatusCancelled),
		false,
	); err != nil {
		return err
	}

	now := e.nowFunc()
	_, err = e.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, completed_at = ?, updated_at = ?
		 WHERE id = ?`,
		string(job.StatusCancelled), now.UnixNano(), now.UnixNano(), id,
	)
	return err
}

// Heartbeat extends the expiry of an active job's claim.
func (e *Engine) Heartbeat(ctx context.Context, id string) error {
	j, err := e.Get(ctx, id)
	if err != nil {
		return err
	}
	if j.Status != job.StatusActive {
		return fmt.Errorf(
			"%w: heartbeat requires active status", domain.ErrValidation,
		)
	}

	now := e.nowFunc()
	// Use per-job claim TTL; fall back to engine-wide TTL.
	claimTTL := e.jobClaimTTL(ctx, id)
	exp := now.Add(claimTTL)
	_, err = e.db.ExecContext(ctx,
		`UPDATE jobs SET expires_at = ?, updated_at = ? WHERE id = ?`,
		exp.UnixNano(), now.UnixNano(), id,
	)
	return err
}

// Get retrieves a job by ID.
func (e *Engine) Get(ctx context.Context, id string) (*job.Job, error) {
	return e.getUnlocked(ctx, id)
}

// List returns jobs matching the query.
func (e *Engine) List(
	ctx context.Context, q job.JobQuery,
) ([]job.Job, error) {
	query := `SELECT
		id, queue, type, status, payload, result, error,
		metadata, claimed_by, priority, attempts, max_attempts,
		backoff, created_at, updated_at, claimed_at, started_at,
		completed_at, expires_at, scheduled_at, next_run_at
	FROM jobs WHERE 1=1`

	var args []any

	if q.Queue != "" {
		query += " AND queue = ?"
		args = append(args, q.Queue)
	}
	if q.Status != "" {
		query += " AND status = ?"
		args = append(args, q.Status)
	}
	if q.Type != "" {
		query += " AND type = ?"
		args = append(args, q.Type)
	}

	query += " ORDER BY created_at ASC"

	if q.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, q.Limit)
	}

	rows, err := e.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("durabletask: list: %w", err)
	}
	defer rows.Close()

	var jobs []job.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *j)
	}
	return jobs, rows.Err()
}

// ReleaseStaleClaims returns expired active jobs to pending.
func (e *Engine) ReleaseStaleClaims(ctx context.Context) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.nowFunc()
	nowNano := now.UnixNano()

	rows, err := e.db.QueryContext(ctx,
		`SELECT id FROM jobs
		 WHERE status = ? AND expires_at IS NOT NULL AND expires_at < ?`,
		string(job.StatusActive), nowNano,
	)
	if err != nil {
		return 0, fmt.Errorf("durabletask: query stale: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	released := 0
	for _, id := range ids {
		if err := e.sm.Transition(ctx,
			domain.State(job.StatusActive),
			domain.State(job.StatusPending),
			false,
		); err != nil {
			continue
		}

		_, err := e.db.ExecContext(ctx,
			`UPDATE jobs SET
				status = ?, claimed_by = '', claimed_at = NULL,
				started_at = NULL, expires_at = NULL, updated_at = ?
			 WHERE id = ?`,
			string(job.StatusPending), nowNano, id,
		)
		if err == nil {
			released++
		}
	}

	return released, nil
}

// jobClaimTTL reads the per-job claim_ttl_ns from the row.
// Falls back to e.claimTTL if 0 or not found.
func (e *Engine) jobClaimTTL(ctx context.Context, id string) time.Duration {
	var ttlNs int64
	row := e.db.QueryRowContext(ctx,
		`SELECT claim_ttl_ns FROM jobs WHERE id = ?`, id,
	)
	if row.Scan(&ttlNs) == nil && ttlNs > 0 {
		return time.Duration(ttlNs)
	}
	return e.claimTTL
}

// getUnlocked loads a single job without acquiring mu.
func (e *Engine) getUnlocked(ctx context.Context, id string) (*job.Job, error) {
	return scanJobRow(e.db.QueryRowContext(ctx,
		`SELECT
			id, queue, type, status, payload, result, error,
			metadata, claimed_by, priority, attempts, max_attempts,
			backoff, created_at, updated_at, claimed_at, started_at,
			completed_at, expires_at, scheduled_at, next_run_at
		 FROM jobs WHERE id = ?`, id,
	))
}
