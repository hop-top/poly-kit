package job

import (
	"context"
	"math/rand/v2"
	"time"
)

// HandlerMap routes job types to handler functions.
type HandlerMap map[string]func(ctx context.Context, job Job) error

// Poller continuously claims and processes jobs from a queue.
type Poller struct {
	Service       Service
	Interval      time.Duration
	Queue         string
	WorkerID      string
	Handlers      HandlerMap
	StaleInterval time.Duration
}

// Run starts the poll loop. It returns when ctx is canceled.
//
// Each iteration:
//  1. Release stale claims (throttled by StaleInterval).
//  2. Claim the next job.
//  3. Route to the appropriate handler.
//  4. Complete or fail the job based on handler result.
//  5. Sleep Interval ± 25% jitter.
func (p *Poller) Run(ctx context.Context) error {
	interval := p.Interval
	if interval == 0 {
		interval = 5 * time.Second
	}
	staleInterval := p.StaleInterval
	if staleInterval == 0 {
		staleInterval = 30 * time.Second
	}

	var lastStale time.Time

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Stale check (throttled).
		if time.Since(lastStale) >= staleInterval {
			_, _ = p.Service.ReleaseStaleClaims(ctx)
			lastStale = time.Now()
		}

		// Claim.
		j, err := p.Service.Claim(ctx, p.Queue, p.WorkerID)
		if err != nil {
			p.sleep(ctx, interval)
			continue
		}
		if j == nil {
			p.sleep(ctx, interval)
			continue
		}

		// Route handler.
		handler, ok := p.Handlers[j.Type]
		if !ok {
			_ = p.Service.Fail(ctx, j.ID, FailOpts{
				Error: "no handler for type: " + j.Type,
				Retry: false,
			})
			p.sleep(ctx, interval)
			continue
		}

		// Execute.
		if herr := handler(ctx, *j); herr != nil {
			_ = p.Service.Fail(ctx, j.ID, FailOpts{
				Error: herr.Error(),
				Retry: true,
			})
		} else {
			_ = p.Service.Complete(ctx, j.ID, nil)
		}

		// Always sleep between jobs to avoid tight loop under load.
		p.sleep(ctx, interval)
	}
}

// sleep waits for the interval with ±25% jitter, respecting ctx.
func (p *Poller) sleep(ctx context.Context, d time.Duration) {
	jitter := float64(d) * 0.25 * (2*rand.Float64() - 1)
	wait := d + time.Duration(jitter)
	if wait < 0 {
		wait = 0
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
