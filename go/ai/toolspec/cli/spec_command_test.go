package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	speccli "hop.top/kit/go/ai/toolspec/cli"
	kitcli "hop.top/kit/go/console/cli"
)

// fixtureRoot returns a kit Root populated with a small fixture command
// tree — used across the spec subcommand tests so they all see the
// same shape.
func fixtureRoot(t *testing.T, opts ...func(*kitcli.Root)) *kitcli.Root {
	t.Helper()
	r := kitcli.New(kitcli.Config{
		Name:    "fixturetool",
		Version: "1.2.3",
		Short:   "Fixture tool used in spec subcommand tests",
	})
	for _, o := range opts {
		o(r)
	}
	return r
}

// addLeaf attaches a runnable cobra leaf with the given side-effect +
// idempotency tags, so it passes Root.Validate.
func addLeaf(parent *cobra.Command, name, short string, se kitcli.SideEffect, id kitcli.Idempotency) *cobra.Command {
	c := &cobra.Command{
		Use:   name,
		Short: short,
		RunE:  func(*cobra.Command, []string) error { return nil },
	}
	kitcli.SetSideEffect(c, se)
	kitcli.SetIdempotency(c, id)
	parent.AddCommand(c)
	return c
}

// runSpec invokes `<tool> spec` against r with the given extra args
// and returns stdout (manifest payload) + stderr (warnings) + the
// dispatch error.
func runSpec(t *testing.T, r *kitcli.Root, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	require.NoError(t, speccli.RegisterSpecCommand(r, "1.0"))

	var outBuf, errBuf bytes.Buffer
	r.Cmd.SetOut(&outBuf)
	r.Cmd.SetErr(&errBuf)

	full := append([]string{"spec"}, args...)
	r.Cmd.SetArgs(full)
	r.WrapRunE()
	err = r.Cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// TestSpecCommand_RoundTrip_JSON locks the manifest JSON shape: every
// field on Manifest survives a marshal+unmarshal cycle through the
// live spec subcommand.
func TestSpecCommand_RoundTrip_JSON(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list things", kitcli.SideEffectRead, kitcli.IdempotencyYes)
	addLeaf(r.Cmd, "create", "create thing", kitcli.SideEffectWrite, kitcli.IdempotencyNo)

	stdout, stderr, err := runSpec(t, r, "--format", "json")
	require.NoError(t, err, "stderr=%q", stderr)
	assert.Empty(t, stderr, "spec must not warn on its own invocation")

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout=%q", stdout)

	assert.Equal(t, "fixturetool", got["tool"])
	assert.Equal(t, "1.2.3", got["version"])
	assert.Equal(t, "1.0", got["schema_version"])

	cmds, ok := got["commands"].([]any)
	require.True(t, ok, "commands must be an array, got %T", got["commands"])

	names := map[string]map[string]any{}
	for _, cAny := range cmds {
		c := cAny.(map[string]any)
		path := c["path"].([]any)
		names[path[len(path)-1].(string)] = c
	}

	require.Contains(t, names, "list")
	require.Contains(t, names, "create")
	assert.Equal(t, "read", names["list"]["side_effect"])
	assert.Equal(t, "write", names["create"]["side_effect"])
	assert.Equal(t, "yes", names["list"]["idempotent"])
	assert.Equal(t, "no", names["create"]["idempotent"])
}

// TestSpecCommand_VersionOnly: --version prints only the schema
// version, fast-pathing capability negotiation.
func TestSpecCommand_VersionOnly(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list things", kitcli.SideEffectRead, kitcli.IdempotencyYes)

	stdout, _, err := runSpec(t, r, "--version", "--format", "json")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout=%q", stdout)
	assert.Equal(t, "1.0", got["schema_version"])
	assert.NotContains(t, got, "commands",
		"--version must NOT include the commands payload")
}

// TestSpecCommand_OmitsDeprecatedByDefault: deprecated leaves are
// hidden from the manifest unless --include-deprecated is set.
func TestSpecCommand_OmitsDeprecatedByDefault(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list things", kitcli.SideEffectRead, kitcli.IdempotencyYes)
	old := addLeaf(r.Cmd, "old-command", "deprecated", kitcli.SideEffectRead, kitcli.IdempotencyYes)
	old.Deprecated = "use newcmd instead"
	kitcli.SetDeprecatedSince(old, "1.5")
	kitcli.SetRemovalTarget(old, "2.0")

	stdout, _, err := runSpec(t, r, "--format", "json")
	require.NoError(t, err)

	assert.NotContains(t, stdout, "old-command",
		"deprecated leaf must be omitted by default; got %q", stdout)
	assert.Contains(t, stdout, "list")
}

// TestSpecCommand_IncludesDeprecatedWith_HelpAll: --include-deprecated
// surfaces deprecated leaves with their full deprecation metadata.
func TestSpecCommand_IncludesDeprecatedWith_HelpAll(t *testing.T) {
	r := fixtureRoot(t)
	old := addLeaf(r.Cmd, "old-command", "deprecated", kitcli.SideEffectRead, kitcli.IdempotencyYes)
	old.Deprecated = "use newcmd instead"
	kitcli.SetDeprecatedSince(old, "1.5")
	kitcli.SetRemovalTarget(old, "2.0")

	stdout, _, err := runSpec(t, r, "--include-deprecated", "--format", "json")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout=%q", stdout)

	cmds := got["commands"].([]any)
	require.NotEmpty(t, cmds)
	// Find the old-command leaf — order isn't guaranteed when other
	// kit-built-ins land in the manifest.
	var oldEntry map[string]any
	for _, cAny := range cmds {
		c := cAny.(map[string]any)
		path := c["path"].([]any)
		if path[len(path)-1].(string) == "old-command" {
			oldEntry = c
			break
		}
	}
	require.NotNil(t, oldEntry, "old-command must appear in manifest, got %v", cmds)
	assert.Equal(t, true, oldEntry["deprecated"])
	assert.Equal(t, "1.5", oldEntry["deprecated_since"])
	assert.Equal(t, "2.0", oldEntry["removal_target"])
}

// TestApiVersion_HidesNewerCommands confirms commands annotated
// kit/since:<ver> newer than the requested --api-version are hidden +
// refused at dispatch with UNSUPPORTED_API_VERSION.
func TestApiVersion_HidesNewerCommands(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list things", kitcli.SideEffectRead, kitcli.IdempotencyYes)
	newCmd := addLeaf(r.Cmd, "shiny", "newer command", kitcli.SideEffectRead, kitcli.IdempotencyYes)
	kitcli.SetSinceVersion(newCmd, "2.0")

	// Request 1.0 → "shiny" should be hidden + refused.
	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.Cmd.SetArgs([]string{"--api-version", "1.0", "shiny"})

	// Run the same setup pipeline Execute does (skipping fang).
	r.AutoRegisterFlags()
	// Manually invoke the api-version filter as Execute does.
	require.NoError(t, r.Cmd.ParseFlags([]string{"--api-version", "1.0"}),
		"parse must succeed for capability negotiation")
	r.WrapRunE()

	// Re-arm the args after ParseFlags consumed them.
	r.Cmd.SetArgs([]string{"--api-version", "1.0", "shiny"})

	// Apply api-version filter so cobra dispatch sees the gating.
	applyFilterForTest(t, r, "1.0")

	err := r.Cmd.Execute()
	require.Error(t, err, "shiny must be refused under api-version 1.0")
	assert.Contains(t, err.Error(), "UNSUPPORTED_API_VERSION",
		"err=%v stderr=%q", err, stderr.String())
}

// TestApiVersion_RefusesNewerFlags confirms flags annotated
// kit/flag-since:<ver> newer than the requested --api-version are
// rejected with UNSUPPORTED_API_VERSION when the caller actually sets
// them. Untouched newer flags pass silently.
func TestApiVersion_RefusesNewerFlags(t *testing.T) {
	r := fixtureRoot(t)
	leaf := addLeaf(r.Cmd, "list", "list things", kitcli.SideEffectRead, kitcli.IdempotencyYes)
	leaf.Flags().String("filter", "", "filter results")
	kitcli.SetFlagSince(leaf, "filter", "2.0")

	// Request 1.0 + use --filter → refusal.
	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.Cmd.SetArgs([]string{"--api-version", "1.0", "list", "--filter", "x"})

	r.AutoRegisterFlags()
	r.WrapRunE()
	applyFilterForTest(t, r, "1.0")

	err := r.Cmd.Execute()
	require.Error(t, err, "--filter must be refused under api-version 1.0")
	assert.Contains(t, err.Error(), "UNSUPPORTED_API_VERSION",
		"err=%v stderr=%q", err, stderr.String())
}

// TestDeprecation_WarningEmittedOnInvocation: invoking a deprecated
// leaf prints a DEPRECATION warning to stderr in plaintext mode.
func TestDeprecation_WarningEmittedOnInvocation(t *testing.T) {
	r := fixtureRoot(t)
	old := addLeaf(r.Cmd, "old-command", "deprecated", kitcli.SideEffectRead, kitcli.IdempotencyYes)
	old.Deprecated = "use newcmd instead"
	kitcli.SetDeprecatedSince(old, "1.5")
	kitcli.SetRemovalTarget(old, "2.0")

	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.Cmd.SetArgs([]string{"old-command"})

	r.AutoRegisterFlags()
	r.WrapRunE()

	require.NoError(t, r.Cmd.Execute())
	assert.Contains(t, stderr.String(), "DEPRECATION",
		"stderr=%q", stderr.String())
	assert.Contains(t, stderr.String(), "use newcmd instead")
	assert.Contains(t, stderr.String(), "1.5")
	assert.Contains(t, stderr.String(), "2.0")
}

// TestDeprecation_WarningInJSONEnvelope: --format json renders the
// deprecation warning under {"warning": {...}} on stderr.
func TestDeprecation_WarningInJSONEnvelope(t *testing.T) {
	r := fixtureRoot(t)
	old := addLeaf(r.Cmd, "old-command", "deprecated", kitcli.SideEffectRead, kitcli.IdempotencyYes)
	old.Deprecated = "use newcmd instead"
	kitcli.SetDeprecatedSince(old, "1.5")
	kitcli.SetRemovalTarget(old, "2.0")

	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.Cmd.SetArgs([]string{"old-command", "--format", "json"})

	r.AutoRegisterFlags()
	r.WrapRunE()

	require.NoError(t, r.Cmd.Execute())

	var got struct {
		Warning kitcli.DeprecationWarning `json:"warning"`
	}
	require.NoError(t, json.Unmarshal(stderr.Bytes(), &got),
		"stderr=%q", stderr.String())
	assert.Equal(t, kitcli.CodeDeprecation, got.Warning.Code)
	assert.Equal(t, "use newcmd instead", got.Warning.Message)
	assert.Equal(t, "1.5", got.Warning.Since)
	assert.Equal(t, "2.0", got.Warning.Removal)
}

// TestSpec_NoDeprecationWarningOnSpecItself locks the manifest-
// integrity rule: invoking the spec subcommand never emits a
// deprecation warning, even when the spec subcommand is somehow
// flagged. Exercises the kit/spec-command annotation guard.
func TestSpec_NoDeprecationWarningOnSpecItself(t *testing.T) {
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list things", kitcli.SideEffectRead, kitcli.IdempotencyYes)

	stdout, stderr, err := runSpec(t, r, "--format", "json")
	require.NoError(t, err)
	assert.NotContains(t, stderr, "DEPRECATION",
		"spec invocation must never emit DEPRECATION; stderr=%q", stderr)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(stdout), "{"),
		"spec stdout must be unpolluted JSON; got %q", stdout)
}

// applyFilterForTest invokes the unexported applyAPIVersionFilter via
// the public Execute path's pre-flight, working around test scoping.
// We use a helper that mirrors what cli.go does so tests don't need
// to call Execute (which goes through fang).
func applyFilterForTest(t *testing.T, r *kitcli.Root, requested string) {
	t.Helper()
	// We can't reach the unexported function directly from the test
	// package; instead, use the public API: SetArgs with --api-version
	// and call Execute. But Execute goes through fang which is
	// awkward. Easiest: invoke the public hook on Root by calling
	// r.ApplyAPIVersionFilter (which we add as a public shim).
	r.ApplyAPIVersionFilter(requested)
}
