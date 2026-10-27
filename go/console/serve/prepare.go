package serve

import (
	"context"
	"fmt"
	"slices"
)

// prepareAll is the prepare phase (serve-lifecycle.md §"Prepare
// phase"): Prepare on every service in order that implements
// [Preparer], one at a time, on the caller's goroutine, before any
// service starts. Nothing runs concurrently with a Prepare, so a
// service may read the command tree and the configuration the run's
// services share without a lock.
//
// It returns the services to start, in order — every service but an
// optional one whose Prepare failed — and the services whose Prepare
// succeeded, which the run releases if it never starts them. A
// non-nil error is a required service's failure, or the run's
// cancellation; nothing has started.
func (s *Supervisor) prepareAll(
	ctx context.Context,
	order []string,
	st *runState,
	em *emitter,
) (start, prepared []string, err error) {
	start = make([]string, 0, len(order))
	for _, name := range order {
		if err := ctx.Err(); err != nil {
			return start, prepared, err
		}
		svc, ok := s.reg.Lookup(name)
		if !ok {
			// startAll reports a service that left the registry.
			start = append(start, name)
			continue
		}
		p, ok := svc.(Preparer)
		if !ok {
			start = append(start, name)
			continue
		}
		if err := p.Prepare(ctx); err != nil {
			err = fmt.Errorf("prepare: %w", err)
			s.noteFailure(ctx, name, err, OutcomeStartFailed, "prepare", st, em)
			if st.isOptional(name) {
				continue
			}
			return start, prepared, err
		}
		prepared = append(prepared, name)
		start = append(start, name)
	}
	return start, prepared, nil
}

// releaseUnstarted stops, without events, every prepared service the
// run never started: a start failure, a required service's Prepare
// failing or a cancellation ended the run first. Each Stop is bounded
// by the service's stop budget.
func (s *Supervisor) releaseUnstarted(ctx context.Context, prepared []string, st *runState, configs map[string]Config) {
	started, _, _, _ := st.snapshot()
	base := context.WithoutCancel(ctx)
	for _, name := range prepared {
		if slices.Contains(started, name) {
			continue
		}
		svc, ok := s.reg.Lookup(name)
		if !ok {
			continue
		}
		stopCtx, cancel := context.WithTimeout(base, stopBudget(configs, name))
		_ = svc.Stop(stopCtx)
		cancel()
	}
}
