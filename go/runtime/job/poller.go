package job

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"time"
)

// HandlerMap routes job types to handler functions.
type HandlerMap map[string]func(ctx context.Context, job Job) error

// PanicError is the error a recovered handler panic is converted to.
// The job is failed with its message; Poller and RunOne never let a
// handler panic escape.
type PanicError struct {
	// Value is the value passed to panic.
	Value any
	// Stack is the goroutine stack at the point of the panic.
	Stack []byte
}

// Error implements error.
func (e *PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.Value)
}

// runHandler invokes h and converts a panic into a *PanicError.
func runHandler(
	ctx context.Context, h func(context.Context, Job) error, j Job,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r, Stack: debug.Stack()}
		}
	}()
	return h(ctx, j)
}

// Poller continuously claims and processes jobs from a queue.
type Poller struct {
	Service       Service
	Interval      time.Duration
	Queue         string
	WorkerID      string
	Handlers      HandlerMap
	StaleInterval time.Duration

	// Logger receives errors from Claim, Complete, Fail and
	// ReleaseStaleClaims, and recovered handler panics. Nil uses
	// slog.Default().
	Logger *slog.Logger
}

func (p *Poller) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// Run starts the poll loop. It returns when ctx is canceled.
//
// Each iteration:
//  1. Release stale claims (throttled by StaleInterval).
//  2. Claim the next job.
//  3. Route to the appropriate handler; a handler panic is recovered
//     and treated as a handler error (*PanicError).
//  4. Complete or fail the job based on handler result.
//  5. Sleep Interval ± 25% jitter.
//
// Errors from the Service calls in steps 1, 2 and 4 do not stop the
// loop; they are logged on Logger and the loop continues.
func (p *Poller) Run(ctx context.Context) error {
	interval := p.Interval
	if interval == 0 {
		interval = 5 * time.Second
	}
	staleInterval := p.StaleInterval
	if staleInterval == 0 {
		staleInterval = 30 * time.Second
	}
	log := p.logger()

	var lastStale time.Time

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Stale check (throttled).
		if time.Since(lastStale) >= staleInterval {
			if _, err := p.Service.ReleaseStaleClaims(ctx); err != nil {
				log.ErrorContext(ctx, "job poller: release stale claims",
					"queue", p.Queue, "error", err)
			}
			lastStale = time.Now()
		}

		// Claim.
		j, err := p.Service.Claim(ctx, p.Queue, p.WorkerID)
		if err != nil {
			log.ErrorContext(ctx, "job poller: claim",
				"queue", p.Queue, "worker", p.WorkerID, "error", err)
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
			p.fail(ctx, j, FailOpts{
				Error: "no handler for type: " + j.Type,
				Retry: false,
			})
			p.sleep(ctx, interval)
			continue
		}

		// Execute.
		if herr := runHandler(ctx, handler, *j); herr != nil {
			if pe, isPanic := herr.(*PanicError); isPanic {
				log.ErrorContext(ctx, "job poller: handler panic",
					"job", j.ID, "type", j.Type, "panic", pe.Value,
					"stack", string(pe.Stack))
			} else {
				log.WarnContext(ctx, "job poller: handler error",
					"job", j.ID, "type", j.Type, "error", herr)
			}
			p.fail(ctx, j, FailOpts{Error: herr.Error(), Retry: true})
		} else if cerr := p.Service.Complete(ctx, j.ID, nil); cerr != nil {
			log.ErrorContext(ctx, "job poller: complete",
				"job", j.ID, "type", j.Type, "error", cerr)
		}

		// Always sleep between jobs to avoid tight loop under load.
		p.sleep(ctx, interval)
	}
}

// fail marks j failed and logs a Fail error instead of dropping it.
func (p *Poller) fail(ctx context.Context, j *Job, opts FailOpts) {
	if err := p.Service.Fail(ctx, j.ID, opts); err != nil {
		p.logger().ErrorContext(ctx, "job poller: fail",
			"job", j.ID, "type", j.Type, "error", err)
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
