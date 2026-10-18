package serve

import (
	"context"
	"time"
)

// optionalSet returns the selected services whose resolved block marks
// them optional (serve-lifecycle.md §"Optional services").
func optionalSet(selected []string, configs map[string]Config) map[string]bool {
	out := make(map[string]bool)
	for _, name := range selected {
		if configs[name].Optional {
			out[name] = true
		}
	}
	return out
}

// release stops an optional service at the point it missed its
// readiness budget, so it cannot begin serving later while reported
// failed. It returns at once; the returned channel closes when the
// release is over.
//
// The Stop runs concurrently and is bounded by the service's stop
// budget and by the run itself: the service never served, so it has
// nothing to drain, and a signal or a fail-fast trip cuts its Stop
// short rather than wait on it. A Stop that has not returned by then
// is written off — the run stops counting the service's goroutine and
// shutdown does not wait for it. Neither outcome is a second failure:
// the service's record stays the failure that put it here, and only a
// clean Stop adds its stopped event. The ordered stop skips a released
// service, and shutdown joins every release.
func (s *Supervisor) release(
	ctx context.Context,
	sup *supervised,
	name string,
	configs map[string]Config,
	st *runState,
	em *emitter,
) <-chan struct{} {
	finished := make(chan struct{})
	svc, ok := s.reg.Lookup(name)
	if !ok {
		close(finished)
		return finished
	}
	st.markReleased(name)

	stopCtx, cancel := context.WithTimeout(ctx, stopBudget(configs, name))
	stopped := make(chan error, 1)
	go func() { stopped <- svc.Stop(stopCtx) }()

	sup.releases.Add(1)
	go func() {
		defer sup.releases.Done()
		defer close(finished)
		defer cancel()
		select {
		case err := <-stopped:
			if err == nil && !st.markStopped(name) {
				em.emit(context.WithoutCancel(ctx), ObjectService, ActionStopped, EventPayload{
					Service: name, ElapsedMS: st.elapsedMS(),
				})
			}
		case <-stopCtx.Done():
			st.markAbandoned(name)
			// Wake whichever loop is counting running services: this
			// one no longer counts.
			sup.exits <- serviceExit{name: name, abandoned: true}
		}
	}()
	return finished
}

// stopBudget is name's stop timeout, or the default.
func stopBudget(configs map[string]Config, name string) time.Duration {
	if c, ok := configs[name]; ok && c.StopTimeout > 0 {
		return c.StopTimeout
	}
	return DefaultStopTimeout
}
