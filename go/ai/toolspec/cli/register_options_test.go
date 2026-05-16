package cli

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/ai/toolspec/adapters"
)

// stubAdapter is a minimal FormatAdapter used to drive collision
// scenarios — name/aliases are caller-controlled; Render is a no-op.
type stubAdapter struct {
	name    string
	aliases []string
}

func (s stubAdapter) Name() string        { return s.name }
func (s stubAdapter) Aliases() []string   { return s.aliases }
func (s stubAdapter) Description() string { return "stub" }
func (s stubAdapter) ContentType() string { return "text/plain" }
func (s stubAdapter) Render(_ io.Writer, _ *toolspec.ToolSpec, _ ...adapters.RenderOption) error {
	return nil
}

// --- registerConfig resolution -----------------------------------

// resolveOK is a tiny test helper: resolves the config and asserts
// no registration error (built-ins never collide on their own; tests
// that intentionally collide should call resolveRegisterConfig
// directly and inspect the error).
func resolveOK(t *testing.T, opts []RegisterOption) *registerConfig {
	t.Helper()
	cfg, err := resolveRegisterConfig(opts)
	require.NoError(t, err)
	return cfg
}

func TestResolveRegisterConfig_Empty(t *testing.T) {
	cfg := resolveOK(t, nil)
	require.NotNil(t, cfg)
	assert.Empty(t, cfg.errorPatterns)
	assert.Empty(t, cfg.workflows)
	assert.Nil(t, cfg.stateIntrospection)
}

func TestResolveRegisterConfig_AllOptions(t *testing.T) {
	cfg := resolveOK(t, []RegisterOption{
		WithErrorPatterns([]toolspec.ErrorPattern{
			{Pattern: "boom", Fix: "duck"},
		}),
		WithWorkflows([]toolspec.Workflow{
			{Name: "do-the-thing", Steps: []string{"step a", "step b"}},
		}),
		WithStateIntrospection(&toolspec.StateIntrospection{
			ConfigCommands: []string{"mytool config"},
			EnvVars:        []string{"MYTOOL_HOME"},
		}),
	})
	require.Len(t, cfg.errorPatterns, 1)
	assert.Equal(t, "boom", cfg.errorPatterns[0].Pattern)
	require.Len(t, cfg.workflows, 1)
	assert.Equal(t, "do-the-thing", cfg.workflows[0].Name)
	require.NotNil(t, cfg.stateIntrospection)
	assert.Equal(t, []string{"mytool config"}, cfg.stateIntrospection.ConfigCommands)
}

// --- WithErrorPatterns -------------------------------------------

func TestWithErrorPatterns_Accumulates(t *testing.T) {
	// Repeated calls accumulate (don't overwrite). Useful when an
	// adopter assembles error patterns from multiple sources.
	cfg := resolveOK(t, []RegisterOption{
		WithErrorPatterns([]toolspec.ErrorPattern{{Pattern: "a"}}),
		WithErrorPatterns([]toolspec.ErrorPattern{{Pattern: "b"}, {Pattern: "c"}}),
	})
	require.Len(t, cfg.errorPatterns, 3)
	patterns := []string{
		cfg.errorPatterns[0].Pattern,
		cfg.errorPatterns[1].Pattern,
		cfg.errorPatterns[2].Pattern,
	}
	assert.Equal(t, []string{"a", "b", "c"}, patterns)
}

func TestWithErrorPatterns_Empty(t *testing.T) {
	// Passing an empty slice should be a no-op (not append nothing
	// and break ordering downstream).
	cfg := resolveOK(t, []RegisterOption{
		WithErrorPatterns(nil),
		WithErrorPatterns([]toolspec.ErrorPattern{}),
	})
	assert.Empty(t, cfg.errorPatterns)
}

// --- WithWorkflows -----------------------------------------------

func TestWithWorkflows_Accumulates(t *testing.T) {
	cfg := resolveOK(t, []RegisterOption{
		WithWorkflows([]toolspec.Workflow{{Name: "w1"}}),
		WithWorkflows([]toolspec.Workflow{{Name: "w2"}}),
	})
	require.Len(t, cfg.workflows, 2)
	assert.Equal(t, "w1", cfg.workflows[0].Name)
	assert.Equal(t, "w2", cfg.workflows[1].Name)
}

// --- WithStateIntrospection --------------------------------------

func TestWithStateIntrospection_Overwrites(t *testing.T) {
	// Repeated calls overwrite (state introspection is tool-level,
	// not accumulating). Latest wins.
	first := &toolspec.StateIntrospection{ConfigCommands: []string{"first"}}
	second := &toolspec.StateIntrospection{ConfigCommands: []string{"second"}}
	cfg := resolveOK(t, []RegisterOption{
		WithStateIntrospection(first),
		WithStateIntrospection(second),
	})
	require.NotNil(t, cfg.stateIntrospection)
	assert.Equal(t, []string{"second"}, cfg.stateIntrospection.ConfigCommands)
}

func TestWithStateIntrospection_Nil(t *testing.T) {
	// Passing nil clears a previously-set introspection.
	cfg := resolveOK(t, []RegisterOption{
		WithStateIntrospection(&toolspec.StateIntrospection{ConfigCommands: []string{"a"}}),
		WithStateIntrospection(nil),
	})
	assert.Nil(t, cfg.stateIntrospection)
}

// --- curatedToolSpec application ---------------------------------

func TestCuratedToolSpec_AppliesAll(t *testing.T) {
	cfg := resolveOK(t, []RegisterOption{
		WithErrorPatterns([]toolspec.ErrorPattern{{Pattern: "x", Fix: "y"}}),
		WithWorkflows([]toolspec.Workflow{{Name: "w"}}),
		WithStateIntrospection(&toolspec.StateIntrospection{EnvVars: []string{"E"}}),
	})
	spec := &toolspec.ToolSpec{Name: "mytool"}
	out := cfg.curatedToolSpec(spec)
	require.Same(t, spec, out, "curatedToolSpec mutates in place and returns the same pointer")
	require.Len(t, out.ErrorPatterns, 1)
	require.Len(t, out.Workflows, 1)
	require.NotNil(t, out.StateIntrospection)
	assert.Equal(t, []string{"E"}, out.StateIntrospection.EnvVars)
}

func TestCuratedToolSpec_NilSpec(t *testing.T) {
	cfg := resolveOK(t, []RegisterOption{
		WithErrorPatterns([]toolspec.ErrorPattern{{Pattern: "x"}}),
	})
	assert.Nil(t, cfg.curatedToolSpec(nil))
}

func TestCuratedToolSpec_PreservesExisting(t *testing.T) {
	// A spec that already has curation (e.g. set by the walker or a
	// previous source) accumulates new items, doesn't overwrite.
	cfg := resolveOK(t, []RegisterOption{
		WithErrorPatterns([]toolspec.ErrorPattern{{Pattern: "new"}}),
		WithWorkflows([]toolspec.Workflow{{Name: "new-wf"}}),
	})
	spec := &toolspec.ToolSpec{
		Name:          "mytool",
		ErrorPatterns: []toolspec.ErrorPattern{{Pattern: "existing"}},
		Workflows:     []toolspec.Workflow{{Name: "existing-wf"}},
	}
	out := cfg.curatedToolSpec(spec)
	require.Len(t, out.ErrorPatterns, 2)
	require.Len(t, out.Workflows, 2)
	assert.Equal(t, "existing", out.ErrorPatterns[0].Pattern)
	assert.Equal(t, "new", out.ErrorPatterns[1].Pattern)
}

func TestCuratedToolSpec_NoCurationLeavesEmpty(t *testing.T) {
	// Tools that haven't curated yet pass nothing; the spec should
	// come out exactly as the walker produced it (no spurious
	// nil-vs-empty changes that would break downstream JSON
	// fixtures).
	cfg := resolveOK(t, nil)
	spec := &toolspec.ToolSpec{Name: "mytool"}
	out := cfg.curatedToolSpec(spec)
	require.Same(t, spec, out)
	assert.Empty(t, out.ErrorPatterns)
	assert.Empty(t, out.Workflows)
	assert.Nil(t, out.StateIntrospection)
}

// Note: Backward-compat of RegisterSpecCommand's variadic signature
// is exercised by every existing test in spec_command_test.go that
// calls `speccli.RegisterSpecCommand(r, "1.0")` without options —
// they must keep passing under the new signature, which they do (the
// variadic gracefully accepts zero options). A dedicated
// "with curation options registered without panic" test would
// duplicate runSpec_RoundTrip_JSON without adding signal once
// T-0335 wires curation through adapter dispatch; defer to that
// task to add the integration test.

// --- Adapter-registration error surfacing ------------------------

func TestResolveRegisterConfig_BuiltinsRegisterCleanly(t *testing.T) {
	// Clean registration of just the built-ins (kit-manifest + mcp)
	// must not return an error — this is the default invocation path
	// and adopters who pass no options must never see surprise errors.
	cfg, err := resolveRegisterConfig(nil)
	require.NoError(t, err)
	require.NotNil(t, cfg.adapterRegistry)
	names := cfg.adapterRegistry.Names()
	assert.Contains(t, names, adapters.KitManifest().Name())
	assert.Contains(t, names, adapters.MCP().Name())
}

func TestResolveRegisterConfig_NameCollisionWithBuiltinReturnsError(t *testing.T) {
	// An extra adapter whose Name() collides with a built-in must be
	// surfaced — silently dropping was the historical behavior this
	// test guards against.
	cfg, err := resolveRegisterConfig([]RegisterOption{
		WithFormatAdapter(stubAdapter{name: adapters.KitManifest().Name()}),
	})
	require.Error(t, err, "name collision with kit-manifest must error")
	assert.Contains(t, err.Error(), adapters.KitManifest().Name(),
		"error message must name the colliding adapter")
	assert.Contains(t, err.Error(), "already registered",
		"error message must explain the collision cause")
	// The registry still mounted the built-in; the extra just lost
	// the race. Adopters keep a usable spec subcommand.
	require.NotNil(t, cfg.adapterRegistry)
	assert.NotNil(t, cfg.adapterRegistry.Lookup(adapters.KitManifest().Name()))
}

func TestResolveRegisterConfig_AliasCollisionReturnsError(t *testing.T) {
	// Alias collisions are equally important — MCP aliases "prompt",
	// so an extra adapter whose Name is "prompt" must be flagged.
	cfg, err := resolveRegisterConfig([]RegisterOption{
		WithFormatAdapter(stubAdapter{name: "prompt"}),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompt")
	// Built-ins still mounted.
	require.NotNil(t, cfg.adapterRegistry)
	assert.NotNil(t, cfg.adapterRegistry.Lookup(adapters.MCP().Name()))
}

func TestResolveRegisterConfig_TwoExtrasCollideBothSurfaced(t *testing.T) {
	// Two extras sharing a name: the second registration fails. With
	// errors.Join the caller sees every problem, not just the first.
	a := stubAdapter{name: "duplicate"}
	b := stubAdapter{name: "duplicate"}
	c := stubAdapter{name: "also-duplicate"}
	d := stubAdapter{name: "also-duplicate"}
	_, err := resolveRegisterConfig([]RegisterOption{
		WithFormatAdapter(a), WithFormatAdapter(b),
		WithFormatAdapter(c), WithFormatAdapter(d),
	})
	require.Error(t, err)
	// errors.Join formats each error on its own line; the message
	// must mention both colliding names so adopters fix them in one
	// pass.
	assert.Contains(t, err.Error(), "duplicate")
	assert.Contains(t, err.Error(), "also-duplicate")
}

func TestResolveRegisterConfig_ExtraExcludedAdapterIsSkipped(t *testing.T) {
	// WithoutAdapter on an extra adapter's name skips registration,
	// so no collision error fires. (Parallel to how excluding mcp
	// suppresses its built-in registration.)
	_, err := resolveRegisterConfig([]RegisterOption{
		WithFormatAdapter(stubAdapter{name: "custom"}),
		WithFormatAdapter(stubAdapter{name: "custom"}),
		WithoutAdapter("custom"),
	})
	require.NoError(t, err, "excluded extras skip registration entirely")
}
