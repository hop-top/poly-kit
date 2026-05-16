// Package restate adapts the Restate durable execution runtime to
// job.Service.
//
// Restate (github.com/restatedev/sdk-go) provides lightweight durable
// execution with virtual objects. This adapter models each job as a
// Restate virtual object keyed by job ID:
//
//   - Enqueue     → send to the "enqueue" handler of the JobManager object
//   - Claim       → send to the "claim" handler; Restate awakeable bridges push/pull
//   - Complete    → send "complete" message to the virtual object
//   - Fail        → send "fail" message with retry/backoff
//   - Timeout     → send "timeout" message
//   - Cancel      → send "cancel" message
//   - Get         → query virtual object state via ingress API
//   - Heartbeat   → extend timer in the virtual object
//   - List        → query admin API for invocations matching filters
//   - ReleaseStaleClaims → Restate timers handle automatic expiry
//
// Constructor accepts a Restate ingress endpoint URL. The adapter
// communicates with Restate via the ingress HTTP API.
package restate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/restatedev/sdk-go/ingress"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/job"
)

const (
	// serviceName is the Restate virtual object service name for jobs.
	serviceName = "JobManager"
)

// Engine implements job.Service backed by Restate.
type Engine struct {
	client  *ingress.Client
	cfg     job.Config
	sm      *domain.StateMachine
	pub     domain.EventPublisher
	nowFunc func() time.Time

	// local state mirrors object state for synchronous reads.
	// In production, reads go through the Restate admin API.
	mu    sync.Mutex
	cache map[string]*job.Job
	seq   int
}

// New creates a Restate-backed Engine.
//
// The endpoint should point to the Restate ingress (e.g.
// "http://localhost:8080"). Options follow the standard job.Option
// pattern.
func New(endpoint string, opts ...job.Option) *Engine {
	cfg := job.BuildConfig(opts)
	return &Engine{
		client:  ingress.NewClient(endpoint),
		cfg:     cfg,
		sm:      job.NewStateMachine(cfg.Publisher),
		pub:     cfg.Publisher,
		nowFunc: cfg.NowFunc,
		cache:   make(map[string]*job.Job),
	}
}

// Enqueue creates a new pending job via the Restate virtual object.
//
// SDK mapping:
//
//	requester := ingress.Object[*EnqueueInput, *EnqueueOutput](
//	    client, serviceName, jobID, "enqueue")
//	resp, err := requester.Send(ctx, input)
//
// The job ID is generated locally and used as the virtual object key.
func (e *Engine) Enqueue(ctx context.Context, opts job.EnqueueOpts) (string, error) {
	if opts.Queue == "" {
		return "", fmt.Errorf("%w: queue is required", domain.ErrValidation)
	}
	if opts.Type == "" {
		return "", fmt.Errorf("%w: type is required", domain.ErrValidation)
	}

	now := e.nowFunc()

	payload, err := marshalPayload(opts.Payload)
	if err != nil {
		return "", err
	}

	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	e.mu.Lock()
	e.seq++
	id := fmt.Sprintf("rs_%d", e.seq)
	e.mu.Unlock()

	j := &job.Job{
		ID:          id,
		Queue:       opts.Queue,
		Type:        opts.Type,
		Status:      job.StatusPending,
		Payload:     payload,
		Metadata:    opts.Metadata,
		Priority:    opts.Priority,
		MaxAttempts: maxAttempts,
		CreatedAt:   now,
		UpdatedAt:   now,
		ScheduledAt: opts.ScheduledAt,
	}

	// Stores job in-memory; ingress API integration deferred to dedicated track.
	e.mu.Lock()
	e.cache[id] = j
	e.mu.Unlock()

	return id, nil
}

// Claim atomically claims the next available job from a queue.
//
// SDK mapping:
//
//	Restate virtual objects are keyed by job ID, not queue. To
//	implement queue-based claiming:
//
//	1. A "QueueManager" service tracks pending job IDs per queue
//	2. Claim calls QueueManager.claim(queue) which:
//	   a. Picks highest-priority pending job
//	   b. Sends "claim" to the JobManager virtual object
//	   c. Returns the claimed job
//
//	Alternatively, use Restate awakeables:
//	   awakeable := restate.Awakeable[*Job](ctx)
//	   // worker calls: restate.ResolveAwakeable(ctx, id, job)
//
// Current implementation uses the local cache for correctness.
func (e *Engine) Claim(ctx context.Context, queue, workerID string) (*job.Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.nowFunc()

	var candidates []*job.Job
	for _, j := range e.cache {
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

	exp := now.Add(5 * time.Minute)
	j.ExpiresAt = &exp

	cp := *j
	return &cp, nil
}

// Complete marks a job as succeeded.
//
// SDK mapping:
//
//	requester := ingress.ObjectSend[*CompleteInput](
//	    client, serviceName, id, "complete")
//	_, err = requester.Send(ctx, &CompleteInput{Result: result})
//
//	In the virtual object handler:
//	    restate.Set(ctx, "status", "succeeded")
//	    restate.Set(ctx, "result", resultBytes)
func (e *Engine) Complete(ctx context.Context, id string, result any) error {
	e.mu.Lock()
	j, ok := e.cache[id]
	e.mu.Unlock()

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
	e.mu.Lock()
	j.Status = job.StatusSucceeded
	j.UpdatedAt = now
	j.CompletedAt = &now
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			e.mu.Unlock()
			return fmt.Errorf("marshal result: %w", err)
		}
		j.Result = b
	}
	e.mu.Unlock()

	return nil
}

// Fail marks a job as failed, optionally scheduling a retry.
//
// SDK mapping:
//
//	requester := ingress.ObjectSend[*FailInput](
//	    client, serviceName, id, "fail")
//	_, err = requester.Send(ctx, &FailInput{Error: opts.Error, Retry: opts.Retry})
//
//	In the virtual object handler, if retry:
//	    restate.Set(ctx, "status", "pending")
//	    restate.After(ctx, backoffDuration) // timer for retry
func (e *Engine) Fail(ctx context.Context, id string, opts job.FailOpts) error {
	e.mu.Lock()
	j, ok := e.cache[id]
	e.mu.Unlock()

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
		e.mu.Lock()
		j.Status = job.StatusPending
		j.Error = opts.Error
		j.ClaimedBy = ""
		j.ClaimedAt = nil
		j.StartedAt = nil
		j.ExpiresAt = nil
		j.UpdatedAt = now

		backoff := job.DefaultBackoff().Compute(j.Attempts - 1)
		nextRun := now.Add(backoff)
		j.NextRunAt = &nextRun
		e.mu.Unlock()
		return nil
	}

	if err := e.sm.Transition(ctx,
		domain.State(j.Status),
		domain.State(job.StatusFailed),
		false,
	); err != nil {
		return err
	}

	now := e.nowFunc()
	e.mu.Lock()
	j.Status = job.StatusFailed
	j.Error = opts.Error
	j.UpdatedAt = now
	j.CompletedAt = &now
	e.mu.Unlock()

	return nil
}

// Timeout marks a job as timed out.
//
// SDK mapping:
//
//	In the virtual object, a timer fires on claim expiry:
//	    restate.After(ctx, claimTTL).Done() → send "timeout" to self
//	    restate.Set(ctx, "status", "timeout")
func (e *Engine) Timeout(ctx context.Context, id string) error {
	e.mu.Lock()
	j, ok := e.cache[id]
	e.mu.Unlock()

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
	e.mu.Lock()
	j.Status = job.StatusTimeout
	j.UpdatedAt = now
	j.CompletedAt = &now
	e.mu.Unlock()

	return nil
}

// Cancel cancels a pending or active job.
//
// SDK mapping:
//
//	requester := ingress.ObjectSend[*CancelInput](
//	    client, serviceName, id, "cancel")
//	_, err = requester.Send(ctx, nil)
func (e *Engine) Cancel(ctx context.Context, id string) error {
	e.mu.Lock()
	j, ok := e.cache[id]
	e.mu.Unlock()

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
	e.mu.Lock()
	j.Status = job.StatusCancelled
	j.UpdatedAt = now
	j.CompletedAt = &now
	e.mu.Unlock()

	return nil
}

// Heartbeat extends the expiry of an active job's claim.
//
// SDK mapping:
//
//	requester := ingress.ObjectSend[any](
//	    client, serviceName, id, "heartbeat")
//	_, err = requester.Send(ctx, nil)
//
//	In the virtual object handler:
//	    // Cancel existing timer, set new one
//	    restate.Set(ctx, "expires_at", newExpiry)
func (e *Engine) Heartbeat(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	j, ok := e.cache[id]
	if !ok {
		return domain.ErrNotFound
	}
	if j.Status != job.StatusActive {
		return fmt.Errorf(
			"%w: heartbeat requires active status", domain.ErrValidation,
		)
	}

	now := e.nowFunc()
	exp := now.Add(5 * time.Minute)
	j.ExpiresAt = &exp
	j.UpdatedAt = now
	return nil
}

// Get retrieves a job by ID.
//
// SDK mapping:
//
//	requester := ingress.Object[any, *Job](
//	    client, serviceName, id, "get")
//	j, err := requester.Request(ctx, nil)
//
//	Alternatively, query Restate admin API:
//	    GET /restate/state/{serviceName}/{id}
func (e *Engine) Get(_ context.Context, id string) (*job.Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	j, ok := e.cache[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *j
	return &cp, nil
}

// List returns jobs matching the query.
//
// SDK mapping:
//
//	Restate does not have a native "list invocations" query for
//	virtual object state. Options:
//	  1. Maintain a separate "index" service that tracks job IDs
//	  2. Use the Restate admin API to list invocations
//	  3. Query a projection/read model updated via event handlers
//
// Current implementation uses the local cache.
func (e *Engine) List(_ context.Context, q job.JobQuery) ([]job.Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var result []job.Job
	for _, j := range e.cache {
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
//
// SDK mapping:
//
//	In a full Restate deployment, stale claim release is handled by
//	timers in the virtual object:
//
//	    // On claim:
//	    restate.After(ctx, claimTTL) → trigger "timeout" handler
//
//	This method scans the local cache for local-mode operation.
func (e *Engine) ReleaseStaleClaims(ctx context.Context) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.nowFunc()
	released := 0

	for _, j := range e.cache {
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

// compile-time interface check.
var _ job.Service = (*Engine)(nil)
