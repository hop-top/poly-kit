package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
)

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
	core := &stubService{name: "core"}
	r := newServeRoot(t, cli.WithServices(core, aux))
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
