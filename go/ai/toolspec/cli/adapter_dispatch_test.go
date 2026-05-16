package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/ai/toolspec/adapters"
	speccli "hop.top/kit/go/ai/toolspec/cli"
	kitcli "hop.top/kit/go/console/cli"
)

// collidingAdapter is a minimal stub used to drive
// RegisterSpecCommand collision tests at the public-API surface
// (the internal-package collision tests live in
// register_options_test.go).
type collidingAdapter struct{ name string }

func (c collidingAdapter) Name() string        { return c.name }
func (c collidingAdapter) Aliases() []string   { return nil }
func (c collidingAdapter) Description() string { return "stub" }
func (c collidingAdapter) ContentType() string { return "text/plain" }
func (c collidingAdapter) Render(_ io.Writer, _ *toolspec.ToolSpec, _ ...adapters.RenderOption) error {
	return nil
}

// runSpecWithOpts invokes `<tool> spec` against r using the supplied
// RegisterSpecCommand options, returning stdout/stderr/err. Lets
// adapter-dispatch tests opt into curation, custom adapters, etc.
func runSpecWithOpts(
	t *testing.T,
	r *kitcli.Root,
	opts []speccli.RegisterOption,
	args ...string,
) (stdout, stderr string, err error) {
	t.Helper()
	require.NoError(t, speccli.RegisterSpecCommand(r, "1.0", opts...))

	var outBuf, errBuf bytes.Buffer
	r.Cmd.SetOut(&outBuf)
	r.Cmd.SetErr(&errBuf)

	full := append([]string{"spec"}, args...)
	r.Cmd.SetArgs(full)
	r.WrapRunE()
	err = r.Cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// --- Default adapter (kit-manifest) -------------------------------

func TestAdapterDispatch_DefaultIsKitManifest(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list things", kitcli.SideEffectRead, kitcli.IdempotencyYes)

	// No --format → kit-manifest by default. Output should contain
	// the "tool" + "schema_version" envelope (Manifest shape).
	stdout, stderr, err := runSpecWithOpts(t, r, nil)
	require.NoError(t, err, "stderr=%q", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout=%q", stdout)
	assert.Equal(t, "fixturetool", got["tool"])
	assert.Equal(t, "1.0", got["schema_version"])
}

// --- Format aliases dispatch correctly ---------------------------

func TestAdapterDispatch_FormatAliasesKitManifest(t *testing.T) {
	for _, alias := range []string{"kit-manifest", "kit", "manifest"} {
		t.Run(alias, func(t *testing.T) {
			r := fixtureRoot(t)
			addLeaf(r.Cmd, "list", "list", kitcli.SideEffectRead, kitcli.IdempotencyYes)
			stdout, stderr, err := runSpecWithOpts(t, r, nil, "--format", alias)
			require.NoError(t, err, "stderr=%q", stderr)

			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(stdout), &got))
			assert.Equal(t, "fixturetool", got["tool"], "%s alias dispatched to kit-manifest", alias)
		})
	}
}

func TestAdapterDispatch_FormatAliasesMCP(t *testing.T) {
	for _, alias := range []string{"mcp", "prompt"} {
		t.Run(alias, func(t *testing.T) {
			r := fixtureRoot(t)
			addLeaf(r.Cmd, "list", "list", kitcli.SideEffectRead, kitcli.IdempotencyYes)
			stdout, stderr, err := runSpecWithOpts(t, r, nil, "--format", alias)
			require.NoError(t, err, "stderr=%q", stderr)

			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(stdout), &got))
			assert.Equal(t, "fixturetool", got["name"],
				"%s alias dispatched to MCP (envelope has 'name', not 'tool')", alias)
			_, hasInputSchema := got["inputSchema"]
			assert.True(t, hasInputSchema, "MCP envelope has inputSchema")
		})
	}
}

// --- Legacy --format json/yaml/table back-compat -----------------

func TestAdapterDispatch_LegacyJSONFallsThroughToKitManifest(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list", kitcli.SideEffectRead, kitcli.IdempotencyYes)

	stdout, stderr, err := runSpecWithOpts(t, r, nil, "--format", "json")
	require.NoError(t, err, "stderr=%q", stderr)

	// `--format json` should produce kit-manifest output
	// (envelope has "tool", not MCP's "name").
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "fixturetool", got["tool"])
}

func TestAdapterDispatch_LegacyYAMLFallsThroughToKitManifest(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list", kitcli.SideEffectRead, kitcli.IdempotencyYes)

	stdout, stderr, err := runSpecWithOpts(t, r, nil, "--format", "yaml")
	require.NoError(t, err, "stderr=%q", stderr)
	// YAML output starts with `tool: fixturetool` (Manifest shape)
	// not `name: fixturetool` (MCP shape).
	assert.Contains(t, stdout, "tool: fixturetool")
}

// --- Unknown format errors --------------------------------------

func TestAdapterDispatch_UnknownFormatErrors(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list", kitcli.SideEffectRead, kitcli.IdempotencyYes)

	_, stderr, err := runSpecWithOpts(t, r, nil, "--format", "definitely-not-a-format")
	require.Error(t, err)
	// Error message lists registered adapters and legacy formats.
	combined := stderr + err.Error()
	assert.Contains(t, combined, "definitely-not-a-format")
	assert.Contains(t, combined, "kit-manifest")
	assert.Contains(t, combined, "mcp")
}

// --- WithoutAdapter excludes built-ins ---------------------------

func TestAdapterDispatch_WithoutMCP(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list", kitcli.SideEffectRead, kitcli.IdempotencyYes)

	opts := []speccli.RegisterOption{
		speccli.WithoutAdapter("mcp"),
	}
	_, _, err := runSpecWithOpts(t, r, opts, "--format", "mcp")
	require.Error(t, err, "mcp adapter excluded; should error")
}

func TestAdapterDispatch_WithoutKitManifest(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list", kitcli.SideEffectRead, kitcli.IdempotencyYes)

	opts := []speccli.RegisterOption{
		speccli.WithoutAdapter("kit-manifest"),
	}
	// Default fallback should now hit MCP (the next adapter in
	// registration order).
	stdout, stderr, err := runSpecWithOpts(t, r, opts)
	require.NoError(t, err, "stderr=%q", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "fixturetool", got["name"], "fell through to MCP")
}

// --- WithDefaultFormat overrides registry default ---------------

func TestAdapterDispatch_WithDefaultFormat(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list", kitcli.SideEffectRead, kitcli.IdempotencyYes)

	opts := []speccli.RegisterOption{
		speccli.WithDefaultFormat("mcp"),
	}
	stdout, stderr, err := runSpecWithOpts(t, r, opts) // no --format
	require.NoError(t, err, "stderr=%q", stderr)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "fixturetool", got["name"], "default → MCP")
}

// --- RegisterSpecCommand surfaces collision errors ---------------

func TestRegisterSpecCommand_CleanRegistrationReturnsNoError(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list", kitcli.SideEffectRead, kitcli.IdempotencyYes)
	require.NoError(t, speccli.RegisterSpecCommand(r, "1.0"))
}

func TestRegisterSpecCommand_CollisionWithBuiltinReturnsError(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list", kitcli.SideEffectRead, kitcli.IdempotencyYes)

	err := speccli.RegisterSpecCommand(r, "1.0",
		speccli.WithFormatAdapter(collidingAdapter{name: "kit-manifest"}))
	require.Error(t, err, "WithFormatAdapter colliding with kit-manifest must surface")
	assert.Contains(t, err.Error(), "kit-manifest",
		"error must name the colliding adapter")

	// The subcommand still mounted so the binary remains usable.
	assert.NotNil(t, r.Cmd.Commands(), "spec subcommand still mounts despite collision")
}
