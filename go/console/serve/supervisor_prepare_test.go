package serve_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/serve"
)

// preparingFake adds the optional Prepare step. Prepare records where
// it ran against the trace, so a test reads whether any service had
// started by then.
type preparingFake struct {
	*fake
	prepareErr error
}

func (p preparingFake) Prepare(context.Context) error {
	p.trace.add("prepare:" + p.name)
	return p.prepareErr
}

// indexOf is where s first appears in seen, or -1.
func indexOf(seen []string, s string) int {
	for i, v := range seen {
		if v == s {
			return i
		}
	}
	return -1
}

// Every service prepares, in start order, before the first one starts:
// a service that started would already be serving requests that write
// the state the next one reads while it prepares.
func TestSupervisor_PreparesEveryServiceBeforeStartingAny(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(preparingFake{fake: &fake{name: "alpha", trace: tr}})
	reg.Register(&fake{name: "beta", trace: tr})
	reg.Register(preparingFake{fake: &fake{name: "gamma", trace: tr}})

	ctx, cancel := context.WithCancel(t.Context())
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{})
	done := make(chan serve.Result, 1)
	go func() {
		done <- sup.Run(ctx, []string{"alpha", "beta", "gamma"}, enabledSet("alpha", "beta", "gamma"))
	}()
	waitFor(t, func() bool { return indexOf(tr.events(), "ready:gamma") >= 0 })
	cancel()
	res := <-done

	assert.Equal(t, serve.OutcomeCleanStop, res.Outcome)
	seen := tr.events()
	assert.Equal(t, []string{"prepare:alpha", "prepare:gamma", "start:alpha"}, seen[:3],
		"both prepares come before the first start")
}

// A required service whose Prepare fails ends the run before anything
// starts; a service already prepared is released by its Stop.
func TestSupervisor_RequiredPrepareFailureStartsNothing(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	reg := serve.NewRegistry()
	reg.Register(preparingFake{fake: &fake{name: "alpha", trace: tr}})
	reg.Register(preparingFake{fake: &fake{name: "beta", trace: tr}, prepareErr: errBind})

	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec))
	res := sup.Run(t.Context(), []string{"alpha", "beta"}, enabledSet("alpha", "beta"))

	assert.Equal(t, serve.OutcomeStartFailed, res.Outcome)
	assert.Empty(t, res.Started)
	require.Contains(t, res.Failed, "beta")
	assert.True(t, errors.Is(res.Failed["beta"], errBind))
	assert.Equal(t, []string{"prepare:alpha", "prepare:beta", "stop:alpha", "stopped:alpha"}, tr.events(),
		"nothing starts; the prepared alpha is released")
	fails := failedEvents(rec, "beta")
	require.Len(t, fails, 1)
	assert.Equal(t, "prepare", fails[0].Reason)
}

// An optional service whose Prepare fails is reported and not started;
// the rest of the run starts without it.
func TestSupervisor_OptionalPrepareFailureSkipsTheService(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	rec := &recorder{}
	reg := serve.NewRegistry()
	reg.Register(preparingFake{fake: &fake{name: "alpha", trace: tr}, prepareErr: errBind})
	reg.Register(&fake{name: "beta", trace: tr})

	ctx, cancel := context.WithCancel(t.Context())
	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{}, serve.WithPublisher(rec))
	done := make(chan serve.Result, 1)
	go func() {
		done <- sup.Run(ctx, []string{"alpha", "beta"},
			withOptional(enabledSet("alpha", "beta"), "alpha"))
	}()
	waitFor(t, func() bool { return indexOf(tr.events(), "ready:beta") >= 0 })
	cancel()
	res := <-done

	assert.Equal(t, serve.OutcomeCleanStop, res.Outcome)
	assert.Equal(t, []string{"beta"}, res.Started)
	assert.Contains(t, res.Failed, "alpha")
	assert.Equal(t, -1, indexOf(tr.events(), "start:alpha"))
	require.Len(t, failedEvents(rec, "alpha"), 1)
}

// Every service optional and every Prepare failing leaves nothing to
// serve: the run is down, as when every start fails.
func TestSupervisor_EveryOptionalPrepareFailingIsAStartFailure(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(preparingFake{fake: &fake{name: "alpha", trace: tr}, prepareErr: errBind})

	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{})
	res := sup.Run(t.Context(), []string{"alpha"}, withOptional(enabledSet("alpha"), "alpha"))

	assert.Equal(t, serve.OutcomeStartFailed, res.Outcome)
	assert.Empty(t, res.Started)
}

// A start failure releases every service prepared after the one that
// failed: the run never starts them.
func TestSupervisor_StartFailureReleasesPreparedServices(t *testing.T) {
	t.Parallel()
	tr := &trace{}
	reg := serve.NewRegistry()
	reg.Register(&fake{name: "alpha", trace: tr, startErr: errBind, neverReady: true})
	reg.Register(preparingFake{fake: &fake{name: "beta", trace: tr}})

	sup := serve.NewSupervisor(reg, serve.SupervisorConfig{})
	res := sup.Run(t.Context(), []string{"alpha", "beta"}, enabledSet("alpha", "beta"))

	assert.Equal(t, serve.OutcomeStartFailed, res.Outcome)
	assert.Equal(t, []string{"alpha"}, res.Started)
	assert.Equal(t, -1, indexOf(tr.events(), "start:beta"))
	assert.GreaterOrEqual(t, indexOf(tr.events(), "stopped:beta"), 0, "the prepared beta is released")
}
