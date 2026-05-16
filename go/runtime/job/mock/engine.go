// Package mock provides an in-memory job.Service implementation for testing.
package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/job"
)

// Engine is an in-memory Service backed by a map and sync.Mutex.
// It enforces the full job state machine via domain.StateMachine.
type Engine struct {
	mu       sync.Mutex
	jobs     map[string]*job.Job
	sm       *domain.StateMachine
	pub      domain.EventPublisher
	nowFunc  func() time.Time
	claimTTL time.Duration
	seq      int
}

// New creates a new in-memory Engine.
func New(opts ...job.Option) *Engine {
	cfg := job.BuildConfig(opts)
	return &Engine{
		jobs:     make(map[string]*job.Job),
		sm:       job.NewStateMachine(cfg.Publisher),
		pub:      cfg.Publisher,
		nowFunc:  cfg.NowFunc,
		claimTTL: 5 * time.Minute,
	}
}

// Enqueue creates a new pending job.
func (e *Engine) Enqueue(_ context.Context, opts job.EnqueueOpts) (string, error) {
	if opts.Queue == "" {
		return "", fmt.Errorf("%w: queue is required", domain.ErrValidation)
	}
	if opts.Type == "" {
		return "", fmt.Errorf("%w: type is required", domain.ErrValidation)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.seq++
	id := fmt.Sprintf("job_%d", e.seq)
	now := e.nowFunc()

	var payload json.RawMessage
	if opts.Payload != nil {
		b, err := json.Marshal(opts.Payload)
		if err != nil {
			return "", fmt.Errorf("marshal payload: %w", err)
		}
		payload = b
	}

	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	j := &job.Job{
		ID:          id,
		Queue:       opts.Queue,
		Type:        opts.Type,
		Status:      job.StatusPending,
		Payload:     payload,
		Metadata:    opts.Metadata,
		Priority:    opts.Priority,
		MaxAttempts: maxAttempts,
		Backoff:     opts.Backoff,
		CreatedAt:   now,
		UpdatedAt:   now,
		ScheduledAt: opts.ScheduledAt,
	}
	e.jobs[id] = j
	return id, nil
}

// Claim atomically claims the next available job from a queue.
// Jobs are ordered by priority (ascending) then created_at (ascending).
func (e *Engine) Claim(ctx context.Context, queue, workerID string) (*job.Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.nowFunc()

	var candidates []*job.Job
	for _, j := range e.jobs {
		if j.Queue != queue || j.Status != job.StatusPending {
			continue
		}
		if j.ScheduledAt != nil && j.ScheduledAt.After(now) {
			continue
		}
		if j.NextRunAt != nil && j.NextRunAt.After(now) {
			continue
		}
		candidates = append(candidates, j)
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	sort.Slice(candidates, func(i, k int) bool {
		if candidates[i].Priority != candidates[k].Priority {
			return candidates[i].Priority < candidates[k].Priority
		}
		return candidates[i].CreatedAt.Before(candidates[k].CreatedAt)
	})

	j := candidates[0]

	if err := e.sm.Transition(ctx,
		domain.State(j.Status),
		domain.State(job.StatusActive),
		false,
	); err != nil {
		return nil, err
	}

	j.Status = job.StatusActive
	j.ClaimedBy = workerID
	j.ClaimedAt = &now
	j.StartedAt = &now
	j.UpdatedAt = now
	j.Attempts++

	exp := now.Add(e.claimTTL)
	j.ExpiresAt = &exp

	cp := *j
	return &cp, nil
}

// Complete marks a job as succeeded.
func (e *Engine) Complete(ctx context.Context, id string, result any) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	j, ok := e.jobs[id]
	if !ok {
		return domain.ErrNotFound
	}

	if err := e.sm.Transition(ctx,
		domain.State(j.Status),
		domain.State(job.StatusSucceeded),
		false,
	); err != nil {
		return err
	}

	now := e.nowFunc()
	j.Status = job.StatusSucceeded
	j.UpdatedAt = now
	j.CompletedAt = &now

	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal result: %w", err)
		}
		j.Result = b
	}
	return nil
}

// Fail marks a job as failed. If opts.Retry is true and attempts < max,
// the job returns to pending with a backoff-computed NextRunAt.
func (e *Engine) Fail(ctx context.Context, id string, opts job.FailOpts) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	j, ok := e.jobs[id]
	if !ok {
		return domain.ErrNotFound
	}

	canRetry := opts.Retry && j.Attempts < j.MaxAttempts

	if canRetry {
		if err := e.sm.Transition(ctx,
			domain.State(j.Status),
			domain.State(job.StatusPending),
			false,
		); err != nil {
			return err
		}

		now := e.nowFunc()
		j.Status = job.StatusPending
		j.Error = opts.Error
		j.ClaimedBy = ""
		j.ClaimedAt = nil
		j.StartedAt = nil
		j.ExpiresAt = nil
		j.UpdatedAt = now

		bo := j.Backoff
		if bo == (job.BackoffStrategy{}) {
			bo = job.DefaultBackoff()
		}
		backoff := bo.Compute(j.Attempts - 1)
		nextRun := now.Add(backoff)
		j.NextRunAt = &nextRun
		return nil
	}

	// Terminal failure.
	if err := e.sm.Transition(ctx,
		domain.State(j.Status),
		domain.State(job.StatusFailed),
		false,
	); err != nil {
		return err
	}

	now := e.nowFunc()
	j.Status = job.StatusFailed
	j.Error = opts.Error
	j.UpdatedAt = now
	j.CompletedAt = &now
	return nil
}

// Timeout marks a job as timed out.
func (e *Engine) Timeout(ctx context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	j, ok := e.jobs[id]
	if !ok {
		return domain.ErrNotFound
	}

	if err := e.sm.Transition(ctx,
		domain.State(j.Status),
		domain.State(job.StatusTimeout),
		false,
	); err != nil {
		return err
	}

	now := e.nowFunc()
	j.Status = job.StatusTimeout
	j.UpdatedAt = now
	j.CompletedAt = &now
	return nil
}

// Cancel marks a job as canceled.
func (e *Engine) Cancel(ctx context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	j, ok := e.jobs[id]
	if !ok {
		return domain.ErrNotFound
	}

	if err := e.sm.Transition(ctx,
		domain.State(j.Status),
		domain.State(job.StatusCancelled),
		false,
	); err != nil {
		return err
	}

	now := e.nowFunc()
	j.Status = job.StatusCancelled
	j.UpdatedAt = now
	j.CompletedAt = &now
	return nil
}

// Heartbeat extends the expiry of an active job.
func (e *Engine) Heartbeat(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	j, ok := e.jobs[id]
	if !ok {
		return domain.ErrNotFound
	}
	if j.Status != job.StatusActive {
		return fmt.Errorf(
			"%w: heartbeat requires active status", domain.ErrValidation,
		)
	}

	now := e.nowFunc()
	exp := now.Add(e.claimTTL)
	j.ExpiresAt = &exp
	j.UpdatedAt = now
	return nil
}

// Get retrieves a job by ID.
func (e *Engine) Get(_ context.Context, id string) (*job.Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	j, ok := e.jobs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *j
	return &cp, nil
}

// List returns jobs matching the query.
func (e *Engine) List(_ context.Context, q job.JobQuery) ([]job.Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var result []job.Job
	for _, j := range e.jobs {
		if q.Queue != "" && j.Queue != q.Queue {
			continue
		}
		if q.Status != "" && string(j.Status) != q.Status {
			continue
		}
		if q.Type != "" && j.Type != q.Type {
			continue
		}
		result = append(result, *j)
	}

	sort.Slice(result, func(i, k int) bool {
		return result[i].CreatedAt.Before(result[k].CreatedAt)
	})

	if q.Limit > 0 && len(result) > q.Limit {
		result = result[:q.Limit]
	}
	return result, nil
}

// ReleaseStaleClaims returns expired active jobs to pending.
func (e *Engine) ReleaseStaleClaims(ctx context.Context) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.nowFunc()
	released := 0

	for _, j := range e.jobs {
		if j.Status != job.StatusActive {
			continue
		}
		if j.ExpiresAt == nil || !j.ExpiresAt.Before(now) {
			continue
		}

		if err := e.sm.Transition(ctx,
			domain.State(j.Status),
			domain.State(job.StatusPending),
			false,
		); err != nil {
			continue
		}

		j.Status = job.StatusPending
		j.ClaimedBy = ""
		j.ClaimedAt = nil
		j.StartedAt = nil
		j.ExpiresAt = nil
		j.UpdatedAt = now
		released++
	}
	return released, nil
}
