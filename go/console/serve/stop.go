package serve

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/runtime/bus"
)

// stopAll invokes Stop in the exact reverse of the order services
// actually started, one at a time, so a dependent is always fully
// stopped before its dependency (serve-lifecycle.md §"Ordered stop").
//
// Each Stop is bounded by that service's stop timeout. A Stop that
// exceeds its budget is abandoned — logged, emitted as failed, and the
// supervisor proceeds to the next service rather than blocking the
// whole shutdown on one straggler. Exceeding the supervisor's total
// budget ends the sequence with OutcomeShutdownTimeout.
//
// parent is the caller's context, which is typically already canceled
// by the time shutdown runs; the stop budgets are therefore derived
// from context.WithoutCancel so a service still gets its drain window
// after the signal that triggered it.
func (s *Supervisor) stopAll(parent context.Context, st *runState, em *emitter) {
	st.mu.Lock()
	order := append([]string(nil), st.started...)
	configs := st.stopConfigs
	st.mu.Unlock()

	base := context.WithoutCancel(parent)
	totalCtx, cancelTotal := context.WithTimeout(base, s.cfg.ShutdownTimeout)
	defer cancelTotal()

	for i := len(order) - 1; i >= 0; i-- {
		name := order[i]

		// An optional service stopped where it failed is not stopped
		// a second time.
		if st.isReleased(name) {
			continue
		}

		// A second signal aborts the drain: the remaining services
		// are abandoned and the run exits with the crash code.
		select {
		case <-s.escalate:
			err := errors.New("drain aborted by second signal")
			st.record(OutcomeRuntimeCrash)
			for _, abandoned := range order[:i+1] {
				if st.isReleased(abandoned) {
					continue
				}
				st.markFailed(abandoned, err, "escalated")
				em.emit(base, ObjectService, ActionFailed, EventPayload{
					Qualifiers: bus.Qualifiers{Reason: "escalated"},
					Service:    abandoned, Error: err.Error(), ElapsedMS: st.elapsedMS(),
				})
			}
			return
		default:
		}

		if totalCtx.Err() != nil {
			st.record(OutcomeShutdownTimeout)
			em.emit(base, ObjectService, ActionFailed, EventPayload{
				Qualifiers: bus.Qualifiers{Reason: "shutdown_timeout"},
				Service:    name,
				Error:      fmt.Sprintf("shutdown budget %s exhausted before stopping", s.cfg.ShutdownTimeout),
				ElapsedMS:  st.elapsedMS(),
			})
			continue
		}

		svc, ok := s.reg.Lookup(name)
		if !ok {
			continue
		}

		s.stopOne(totalCtx, svc, name, stopBudget(configs, name), st, em)
	}
}

// stopOne bounds one Stop by budget and by whatever remains of the
// supervisor's total budget, whichever expires first.
func (s *Supervisor) stopOne(
	totalCtx context.Context,
	svc Service,
	name string,
	budget time.Duration,
	st *runState,
	em *emitter,
) {
	stopCtx, cancel := context.WithTimeout(totalCtx, budget)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- svc.Stop(stopCtx) }()

	select {
	case err := <-done:
		if err != nil {
			s.noteFailure(totalCtx, name, err, OutcomeRuntimeCrash, "stop", st, em)
			return
		}
		// A service that returned on its own already reported
		// stopped when it did; Stop released its resources, and the
		// event is not repeated (contract: one stopped per service).
		if st.markStopped(name) {
			return
		}
		em.emit(totalCtx, ObjectService, ActionStopped, EventPayload{
			Service: name, ElapsedMS: st.elapsedMS(),
		})

	case <-stopCtx.Done():
		// Abandoned, not awaited: the goroutine is left to finish on
		// its own so one straggler cannot hold the whole shutdown.
		reason := "stop_timeout"
		outcome := OutcomeRuntimeCrash
		if totalCtx.Err() != nil {
			reason = "shutdown_timeout"
			outcome = OutcomeShutdownTimeout
		}
		err := fmt.Errorf("stop exceeded %s", budget)
		st.markFailed(name, err, reason)
		// The total budget running out is the run's failure, whoever
		// it ran out on; one optional service's own stop budget is not,
		// and its goroutine, which may never return, is written off
		// rather than waited for.
		if outcome == OutcomeShutdownTimeout || !st.isOptional(name) {
			st.record(outcome)
		}
		if st.isOptional(name) {
			st.markAbandoned(name)
		}
		em.emit(context.WithoutCancel(totalCtx), ObjectService, ActionFailed, EventPayload{
			Qualifiers: bus.Qualifiers{Reason: reason},
			Service:    name, Error: err.Error(), ElapsedMS: st.elapsedMS(),
		})
	}
}

// failureError renders the outcome as the kit error envelope the
// command layer returns, carrying the contract's Code and ExitCode.
//
// A failure that wraps a kit transient error propagates exit 6
// unchanged, so agents and retry wrappers keep their existing branch
// (serve-lifecycle.md §"Exit behavior"). Only the failures the outcome
// is about are consulted: the required services', or every service's
// when all that failed were optional.
func failureError(o LifecycleOutcome, failed map[string]error, optional map[string]bool) *output.Error {
	msg := failureMessage(o, failed, optional)

	causes := make([]string, 0, len(failed))
	for _, name := range sortedKeys(failed) {
		if !optional[name] {
			causes = append(causes, name)
		}
	}
	if len(causes) == 0 {
		causes = sortedKeys(failed)
	}
	for _, name := range causes {
		var kitErr *output.Error
		if errors.As(failed[name], &kitErr) && kitErr.Transience == output.TransienceTransient {
			out := output.TransientError(msg)
			out.SuggestedFix = kitErr.SuggestedFix
			return out
		}
	}

	return &output.Error{
		Code:       CodeFor(o),
		Message:    msg,
		ExitCode:   ExitCodeFor(o),
		Transience: output.TransiencePermanent,
	}
}

// failureMessage names every failed service. An optional one is marked,
// so an operator reads which failure the exit is about.
func failureMessage(o LifecycleOutcome, failed map[string]error, optional map[string]bool) string {
	var b strings.Builder
	switch o {
	case OutcomeStartFailed:
		b.WriteString("service failed to start")
	case OutcomeShutdownTimeout:
		b.WriteString("shutdown budget exceeded")
	default:
		b.WriteString("service failed")
	}
	names := sortedKeys(failed)
	for i, name := range names {
		if i == 0 {
			b.WriteString(": ")
		} else {
			b.WriteString("; ")
		}
		if optional[name] {
			fmt.Fprintf(&b, "%s (optional): %v", name, failed[name])
			continue
		}
		fmt.Fprintf(&b, "%s: %v", name, failed[name])
	}
	return b.String()
}

func sortedKeys(m map[string]error) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
