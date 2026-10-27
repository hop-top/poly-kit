package service_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/internal/testhome"
	"hop.top/kit/go/console/cli/service"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/runtime/bus"
)

// eventLog is a Publisher that keeps every lifecycle payload.
type eventLog struct {
	mu     sync.Mutex
	events []bus.Event
}

func (l *eventLog) Publish(_ context.Context, e bus.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
	return nil
}

// failed returns the failed payloads about name.
func (l *eventLog) failed(name string) []serve.EventPayload {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []serve.EventPayload
	for _, e := range l.events {
		if p, ok := e.Payload.(serve.EventPayload); ok && e.Topic == "kit.serve.service.failed" && p.Service == name {
			out = append(out, p)
		}
	}
	return out
}

// unbindable fails its Start the way a listener whose port is taken
// does: at once, before reporting ready.
type unbindable struct {
	name     string
	optional bool
}

func (u *unbindable) Name() string { return u.name }
func (u *unbindable) Start(context.Context, func()) error {
	return errors.New("bind: address already in use")
}
func (u *unbindable) Ready() bool                { return false }
func (u *unbindable) Stop(context.Context) error { return nil }

// declaringUnbindable adds the registration-side default.
type declaringUnbindable struct{ *unbindable }

func (d declaringUnbindable) Optional() bool { return d.optional }

// coreAndAux builds a root running core and an unbindable aux, both
// enabled.
func coreAndAux(t *testing.T, aux serve.Service) (*cli.Root, *stubService) {
	t.Helper()
	testhome.Isolate(t)
	core := &stubService{name: "core"}
	r := newServeRoot(t, service.WithServices(core, aux))
	r.Viper.Set("services.core.enabled", true)
	r.Viper.Set("services.aux.enabled", true)
	return r, core
}

func TestServe_OptionalKeyKeepsTheCoreServing(t *testing.T) {
	r, core := coreAndAux(t, &unbindable{name: "aux"})
	r.Viper.Set("services.aux.optional", true)

	err := runServeArgs(t, r, []string{"serve"}, 300*time.Millisecond)
	require.NoError(t, err, "an optional service failing to bind is not the run failing")
	assert.True(t, core.wasStarted())
}

func TestServe_RequiredServiceFailingToBindStillFailsTheRun(t *testing.T) {
	r, _ := coreAndAux(t, &unbindable{name: "aux"})

	err := runServeArgs(t, r, []string{"serve"}, 2*time.Second)
	var oe *output.Error
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, 1, oe.ExitCode)
	assert.Contains(t, oe.Message, "aux: bind: address already in use")
}

func TestServe_OptionalDeclarationIsTheDefaultAndConfigOverridesIt(t *testing.T) {
	t.Run("declared optional, no key", func(t *testing.T) {
		r, core := coreAndAux(t, declaringUnbindable{&unbindable{name: "aux", optional: true}})
		require.NoError(t, runServeArgs(t, r, []string{"serve"}, 300*time.Millisecond))
		assert.True(t, core.wasStarted())
	})
	t.Run("declared optional, key says required", func(t *testing.T) {
		r, _ := coreAndAux(t, declaringUnbindable{&unbindable{name: "aux", optional: true}})
		r.Viper.Set("services.aux.optional", false)
		var oe *output.Error
		require.ErrorAs(t, runServeArgs(t, r, []string{"serve"}, 2*time.Second), &oe)
		assert.Equal(t, 1, oe.ExitCode)
	})
	t.Run("declared required, key says optional", func(t *testing.T) {
		r, _ := coreAndAux(t, declaringUnbindable{&unbindable{name: "aux", optional: false}})
		r.Viper.Set("services.aux.optional", true)
		require.NoError(t, runServeArgs(t, r, []string{"serve"}, 300*time.Millisecond))
	})
}

func TestServe_ListShowsOptional(t *testing.T) {
	r, _ := coreAndAux(t, declaringUnbindable{&unbindable{name: "aux", optional: true}})
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	require.NoError(t, runServeArgs(t, r, []string{"serve", "--list"}, 2*time.Second))

	lines := strings.Split(out.String(), "\n")
	require.NotEmpty(t, lines)
	assert.Equal(t, []string{"SERVICE", "CONFIGURED", "ENABLED", "READY", "OPTIONAL"}, strings.Fields(lines[0]))
	fields := map[string][]string{}
	for _, l := range lines[1:] {
		if f := strings.Fields(l); len(f) > 0 {
			fields[f[0]] = f
		}
	}
	require.Len(t, fields["aux"], 5)
	assert.Equal(t, "true", fields["aux"][4], "the registration's default shows")
	require.Len(t, fields["core"], 5)
	assert.Equal(t, "false", fields["core"][4])
}

// TestServe_OptionalHasNoSharedDefault pins that optional is a decision
// about one service: services.all.optional is refused, like every
// other lifecycle key under services.all.
func TestServe_OptionalHasNoSharedDefault(t *testing.T) {
	r, _ := coreAndAux(t, &unbindable{name: "aux"})
	r.Viper.Set("services.all.optional", true)

	var oe *output.Error
	require.ErrorAs(t, runServeArgs(t, r, []string{"serve"}, 2*time.Second), &oe)
	assert.Equal(t, 2, oe.ExitCode)
	assert.Contains(t, oe.Message, "services.all.optional")
}

// TestServe_SelectorFormAppliesTheRegistrationDefault pins that naming
// an optional service with no services.<name> block still runs it as
// optional, as --list says: the events carry the default too.
func TestServe_SelectorFormAppliesTheRegistrationDefault(t *testing.T) {
	testhome.Isolate(t)
	log := &eventLog{}
	r := newServeRoot(t,
		service.WithService(declaringUnbindable{&unbindable{name: "aux", optional: true}}),
		service.WithServiceBus(log))

	var oe *output.Error
	require.ErrorAs(t, runServeArgs(t, r, []string{"serve", "aux"}, 2*time.Second), &oe)
	assert.Equal(t, 1, oe.ExitCode, "the only service failing is the run failing")
	failed := log.failed("aux")
	require.Len(t, failed, 1)
	assert.True(t, failed[0].Optional, "the registration's default applies under the selector form")
}

// TestServe_OptionalMustBeABoolean pins that a value optional cannot
// mean is refused, naming the key, rather than read as required.
func TestServe_OptionalMustBeABoolean(t *testing.T) {
	for _, bad := range []any{"ture", "maybe", 2, []string{"true"}} {
		r, core := coreAndAux(t, &unbindable{name: "aux"})
		r.Viper.Set("services.aux.optional", bad)

		var oe *output.Error
		require.ErrorAs(t, runServeArgs(t, r, []string{"serve"}, 2*time.Second), &oe, "%v", bad)
		assert.Equal(t, 2, oe.ExitCode, "%v", bad)
		assert.Contains(t, oe.Message, "services.aux.optional", "%v", bad)
		assert.False(t, core.wasStarted(), "refused before anything starts")
	}
	for _, good := range []any{true, "true", "False", "1"} {
		r, _ := coreAndAux(t, &unbindable{name: "aux"})
		r.Viper.Set("services.aux.optional", good)
		err := runServeArgs(t, r, []string{"serve"}, 300*time.Millisecond)
		var oe *output.Error
		if errors.As(err, &oe) {
			assert.NotEqual(t, 2, oe.ExitCode, "%v is a boolean: %s", good, oe.Message)
		}
	}
}
