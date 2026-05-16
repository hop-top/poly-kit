// Package hatchet adapts the Hatchet workflow engine to job.Service.
//
// Hatchet (github.com/hatchet-dev/hatchet) is a workflow orchestration
// platform for AI agent workloads. This adapter maps the pull-based
// job.Service interface onto Hatchet's push-based dispatch model.
//
// # SDK dependency
//
// The Hatchet Go SDK lives in a monorepo that transitively pulls in
// gRPC, pgx, zerolog, and other heavy dependencies. To keep
// hop.top/kit's dependency tree lean, this package does NOT import the
// Hatchet SDK directly. Instead it defines the adapter struct and
// documents the exact SDK calls each method maps to.
//
// When wiring to a real Hatchet server, inject the client via the
// HatchetClient interface defined below, which mirrors the subset of
// github.com/hatchet-dev/hatchet/sdks/go.Client needed by this adapter.
//
// # Mapping summary
//
//   - Enqueue  → client.Runs().Trigger(ctx, workflow, input)
//   - Claim    → internal worker channel (push→pull bridge)
//   - Complete → step handler returns (nil, result)
//   - Fail     → step handler returns error; WithRetries(n)
//   - Cancel   → client.Runs().Cancel(ctx, runID)
//   - Get      → client.Runs().Get(ctx, runID)
//   - List     → client.Runs().List(ctx, opts)
//   - Timeout  → hatchet.WithTimeout("60s") per task
//   - Heartbeat→ managed internally by Hatchet worker SDK
package hatchet

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

// HatchetClient defines the subset of the Hatchet SDK used by Engine.
//
// In production, this is satisfied by *hatchet.Client from
// github.com/hatchet-dev/hatchet/sdks/go. Implement this interface
// with a thin wrapper or use the mock for testing.
//
// The Hatchet SDK client is created via:
//
//	import hatchet "github.com/hatchet-dev/hatchet/sdks/go"
//	client, err := hatchet.NewClient()
type HatchetClient interface {
	// TriggerRun starts a workflow run with the given input.
	// Maps to: client.Runs().Trigger(ctx, workflowRef, input)
	TriggerRun(ctx context.Context, queue string, input map[string]any) (runID string, err error)

	// CancelRun cancels a workflow run.
	// Maps to: client.Runs().Cancel(ctx, runID)
	CancelRun(ctx context.Context, runID string) error

	// GetRun queries a workflow run by ID.
	// Maps to: client.Runs().Get(ctx, runID)
	GetRun(ctx context.Context, runID string) (status string, err error)
}

// Engine implements job.Service backed by Hatchet.
type Engine struct {
	client  HatchetClient
	cfg     job.Config
	sm      *domain.StateMachine
	pub     domain.EventPublisher
	nowFunc func() time.Time

	mu    sync.Mutex
	cache map[string]*job.Job
	seq   int
}

// New creates a Hatchet-backed Engine.
//
// Pass nil for client to use local-only mode (useful for tests and
// scaffolding). When a real HatchetClient is provided, Enqueue and
// Cancel delegate to the Hatchet API.
func New(client HatchetClient, opts ...job.Option) *Engine {
	cfg := job.BuildConfig(opts)
	return &Engine{
		client:  client,
		cfg:     cfg,
		sm:      job.NewStateMachine(cfg.Publisher),
		pub:     cfg.Publisher,
		nowFunc: cfg.NowFunc,
		cache:   make(map[string]*job.Job),
	}
}

// Enqueue triggers a Hatchet workflow run with job data as input.
//
// SDK mapping:
//
//	workflow := client.NewWorkflow(opts.Queue)
//	task := workflow.NewTask("process", handler,
//	    hatchet.WithRetries(opts.MaxAttempts),
//	    hatchet.WithPriority(opts.Priority))
//	result := client.Runs().Trigger(ctx, workflow, input)
//	runID := result.ID()
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
	id := fmt.Sprintf("hatchet_%d", e.seq)
	e.mu.Unlock()

	// Delegate to Hatchet API when client is available.
	if e.client != nil {
		input := toJobInput(opts)
		runID, err := e.client.TriggerRun(ctx, opts.Queue, input)
		if err != nil {
			return "", fmt.Errorf("hatchet trigger: %w", err)
		}
		id = runID
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
		CreatedAt:   now,
		UpdatedAt:   now,
		ScheduledAt: opts.ScheduledAt,
	}

	e.mu.Lock()
	e.cache[id] = j
	e.mu.Unlock()

	return id, nil
}

// Claim retrieves the next available job from the given queue.
//
// SDK mapping:
//
//	Hatchet dispatches work to registered workers. The adapter runs
//	an internal Hatchet worker that receives steps and posts them to
//	a per-queue channel. Claim reads from that channel.
//
//	worker, _ := client.NewWorker("kit-job",
//	    hatchet.WithWorkflows(workflow))
//	// In the step handler:
//	func(ctx hatchet.Context) error {
//	    j := mapContextToJob(ctx)
//	    dispatch[queue] <- j
//	}
//
// Local-mode implementation selects from cache by priority + FIFO.
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
//	In the Hatchet step handler, return nil + result:
//	    func(ctx hatchet.Context) (any, error) { return result, nil }
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
//	In the Hatchet step handler, return an error:
//	    func(ctx hatchet.Context) error { return fmt.Errorf("...") }
//	Retries configured via: hatchet.WithRetries(n)
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
//	hatchet.WithTimeout("60s") per task definition.
//	Hatchet fires a timeout event → adapter sets StatusTimeout.
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
// SDK mapping: client.Runs().Cancel(ctx, runID)
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

	// Delegate to Hatchet API.
	if e.client != nil {
		if err := e.client.CancelRun(ctx, id); err != nil {
			return fmt.Errorf("hatchet cancel: %w", err)
		}
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
// SDK mapping: Hatchet manages heartbeats internally; no-op against
// the API but updates the local cache.
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
// SDK mapping: client.Runs().Get(ctx, runID)
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
//	client.Runs().List(ctx, ListOpts{
//	    WorkflowName: q.Queue,
//	    Status:       mapStatus(q.Status),
//	    Limit:        q.Limit,
//	})
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
// SDK mapping: Hatchet handles stale detection via internal
// timeout/heartbeat. This scans the local cache.
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
