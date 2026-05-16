package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/ai/toolspec"
	speccli "hop.top/kit/go/ai/toolspec/cli"
	kitcli "hop.top/kit/go/console/cli"
)

// runSpecJSON renders the spec manifest as JSON and decodes it for
// assertions on the 1.1 schema additions.
func runSpecJSON(t *testing.T, r *kitcli.Root, schemaVersion string) toolspec.Manifest {
	t.Helper()
	require.NoError(t, speccli.RegisterSpecCommand(r, schemaVersion))

	var outBuf, errBuf bytes.Buffer
	r.Cmd.SetOut(&outBuf)
	r.Cmd.SetErr(&errBuf)
	r.Cmd.SetArgs([]string{"spec", "--format=json"})
	r.WrapRunE()
	require.NoError(t, r.Cmd.Execute())

	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &m))
	return m
}

func findEntry(m toolspec.Manifest, path ...string) *toolspec.ManifestCommand {
	want := strings.Join(path, " ")
	for i := range m.Commands {
		if strings.Join(m.Commands[i].Path, " ") == want {
			return &m.Commands[i]
		}
	}
	return nil
}

func TestSpecV11_ProjectsLongAndShape(t *testing.T) {
	r := kitcli.New(kitcli.Config{
		Name: "fixture", Version: "0.1.0",
		Short: "fixture for v1.1 manifest tests",
	})
	leaf := &cobra.Command{
		Use:   "init",
		Short: "Initialize",
		Long:  "Initialize the project in the current directory.",
		RunE:  func(*cobra.Command, []string) error { return nil },
	}
	kitcli.SetSideEffect(leaf, kitcli.SideEffectWrite)
	kitcli.SetIdempotency(leaf, kitcli.IdempotencyYes)
	kitcli.SetTopLevelVerb(leaf)
	kitcli.SetRetryable(leaf, true)
	r.Cmd.AddCommand(leaf)

	m := runSpecJSON(t, r, "1.1")

	got := findEntry(m, "fixture", "init")
	require.NotNil(t, got, "init leaf must appear in manifest")
	assert.Equal(t, "Initialize the project in the current directory.", got.Long)
	assert.True(t, got.TopLevelVerb, "kit/top-level-verb must surface")
	assert.True(t, got.Retryable, "kit/retryable must surface")
	assert.True(t, got.DryRunSupported,
		"write leaf without opt-out must surface DryRunSupported=true")
}

func TestSpecV11_ProjectsOutputSchema(t *testing.T) {
	type fooOutput struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	r := kitcli.New(kitcli.Config{
		Name: "fixture", Version: "0.1.0",
		Short: "out-schema fixture",
	})
	parent := &cobra.Command{Use: "foo", Short: "foo group"}
	leaf := &cobra.Command{
		Use: "show", Short: "Show one foo",
		Long: "Render one foo as structured output.",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	kitcli.SetSideEffect(leaf, kitcli.SideEffectRead)
	kitcli.SetIdempotency(leaf, kitcli.IdempotencyYes)
	require.NoError(t, kitcli.SetOutputSchema(leaf, kitcli.OutputSchema{
		Type: &fooOutput{}, Version: "1.0",
	}))
	parent.AddCommand(leaf)
	r.Cmd.AddCommand(parent)

	m := runSpecJSON(t, r, "1.1")
	got := findEntry(m, "fixture", "foo", "show")
	require.NotNil(t, got)
	assert.Equal(t, "1.0", got.OutputSchemaVersion)
	require.NotEmpty(t, got.OutputSchema, "schema bytes must be embedded")
	// The reflected schema is JSON; verify it parses.
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(got.OutputSchema, &decoded))
}

func TestSpecV11_ProjectsExamplesAndNextSteps(t *testing.T) {
	r := kitcli.New(kitcli.Config{
		Name: "fixture", Version: "0.1.0",
		Short: "guidance fixture",
	})
	parent := &cobra.Command{Use: "foo", Short: "foo group"}
	leaf := &cobra.Command{
		Use: "create", Short: "Create foo",
		Long: "Create one foo with the given name.",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	kitcli.SetSideEffect(leaf, kitcli.SideEffectWrite)
	kitcli.SetIdempotency(leaf, kitcli.IdempotencyNo)
	require.NoError(t, kitcli.SetExamples(leaf, []kitcli.Example{
		{Title: "Quick", Command: "fixture foo create --name=alpha"},
	}))
	require.NoError(t, kitcli.SetNextSteps(leaf, []kitcli.NextStep{
		{When: "on success", Suggest: "fixture foo list",
			Reason: "verify the new foo appears"},
	}))
	parent.AddCommand(leaf)
	r.Cmd.AddCommand(parent)

	m := runSpecJSON(t, r, "1.1")
	got := findEntry(m, "fixture", "foo", "create")
	require.NotNil(t, got)
	require.Len(t, got.Examples, 1)
	assert.Equal(t, "Quick", got.Examples[0].Title)
	require.Len(t, got.NextSteps, 1)
	assert.Equal(t, "on success", got.NextSteps[0].When)
}

func TestSpecV11_ProjectsReservedAndDryRunOptOut(t *testing.T) {
	r := kitcli.New(kitcli.Config{
		Name: "fixture", Version: "0.1.0",
		Short: "reserved fixture",
	})
	// Register the spec command to take a reserved entry on the
	// root.
	require.NoError(t, speccli.RegisterSpecCommand(r, "1.1"))

	// Adopter-side leaf — NOT reserved.
	parent := &cobra.Command{Use: "foo", Short: "foo group"}
	leaf := &cobra.Command{
		Use: "destroy", Short: "Destroy foo",
		Long: "Destroy the foo (irreversible).",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	kitcli.SetSideEffect(leaf, kitcli.SideEffectDestructive)
	kitcli.SetIdempotency(leaf, kitcli.IdempotencyYes)
	kitcli.OptOutDryRun(leaf)
	require.NoError(t, kitcli.SetDryRunRationale(leaf,
		"irreversible deletion has no preview semantics"))
	parent.AddCommand(leaf)
	r.Cmd.AddCommand(parent)

	// Drive directly through BuildManifest so we don't depend on
	// register-then-execute timing.
	m := speccli.BuildManifest(r, "1.1", false)

	got := findEntry(m, "fixture", "foo", "destroy")
	require.NotNil(t, got)
	assert.False(t, got.DryRunSupported, "opted-out leaf must not surface DryRunSupported")
	assert.Equal(t, "irreversible deletion has no preview semantics", got.DryRunRationale)
	assert.False(t, got.Reserved, "adopter leaf is not reserved")

	specEntry := findEntry(m, "fixture", "spec")
	require.NotNil(t, specEntry, "spec subcommand must self-surface")
	assert.True(t, specEntry.Reserved, "spec is a reserved kit-shipped subcommand")
	assert.True(t, specEntry.TopLevelVerb,
		"spec self-annotates as top-level verb")
}

func TestSpecV11_DecodeFailureDropsField(t *testing.T) {
	r := kitcli.New(kitcli.Config{
		Name: "fixture", Version: "0.1.0",
		Short: "decode failure fixture",
	})
	parent := &cobra.Command{Use: "foo", Short: "foo group"}
	leaf := &cobra.Command{
		Use: "noisy", Short: "Noisy",
		Long: "leaf with malformed kit/examples",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	kitcli.SetSideEffect(leaf, kitcli.SideEffectRead)
	kitcli.SetIdempotency(leaf, kitcli.IdempotencyYes)
	// Directly write malformed JSON into the annotation, bypassing
	// the typed setter that would have rejected it.
	leaf.Annotations = map[string]string{
		"kit/side-effect": "read",
		"kit/idempotent":  "yes",
		"kit/examples":    "{not-valid",
	}
	parent.AddCommand(leaf)
	r.Cmd.AddCommand(parent)

	m := speccli.BuildManifest(r, "1.1", false)
	got := findEntry(m, "fixture", "foo", "noisy")
	require.NotNil(t, got)
	assert.Empty(t, got.Examples,
		"decode failure must be silently dropped, not surfaced")
}
