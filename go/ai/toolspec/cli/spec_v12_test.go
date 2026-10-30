package cli_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
	speccli "hop.top/kit/go/ai/toolspec/cli"
	kitcli "hop.top/kit/go/console/cli"
)

// TestSpecV12_FlagRequiredChoicesArgVariadic pins the 1.2 manifest
// additions as a live `<tool> spec` run emits them: a flag cobra
// marks required says so, a flag declared through WithFlagEnum
// carries its closed set as choices (and keeps the "(one of: …)"
// help suffix, the accepted duplication), and a kit/args entry
// ending in "..." is variadic.
func TestSpecV12_FlagRequiredChoicesArgVariadic(t *testing.T) {
	r := kitcli.New(kitcli.Config{
		Name: "fixture", Version: "0.1.0",
		Short: "fixture for v1.2 manifest tests",
		// The tree is deliberately minimal (no status verb, a bare
		// depth-1 leaf); the manifest, not shape validation, is
		// under test.
		DisableValidate: true,
	})
	leaf := &cobra.Command{
		Use:   "attach",
		Short: "Attach files",
		RunE:  func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{
			"kit/args": "name, note?, files...?",
		},
	}
	kitcli.SetSideEffect(leaf, kitcli.SideEffectWrite)
	leaf.Flags().String("mode", "fast", "Attach mode")
	require.NoError(t, leaf.MarkFlagRequired("mode"))
	leaf.Flags().String("label", "", "Free-form label")
	r.Cmd.AddCommand(leaf)
	r.WithCommandFlagEnum("attach", "mode", "fast", "safe")
	require.NoError(t, speccli.RegisterSpecCommand(r, "1.2"))
	// Prepare is the half of Execute that stamps the enum onto the
	// flag; the spec then reads what a live run reads.
	require.NoError(t, r.Prepare())

	var outBuf, errBuf bytes.Buffer
	r.Cmd.SetOut(&outBuf)
	r.Cmd.SetErr(&errBuf)
	r.Cmd.SetArgs([]string{"spec", "--format=json"})
	require.NoError(t, r.Cmd.Execute(), "stderr=%q", errBuf.String())
	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &m))
	assert.Equal(t, "1.2", m.SchemaVersion)
	entry := findEntry(m, "fixture", "attach")
	require.NotNil(t, entry)

	assert.Equal(t, []toolspec.ManifestArg{
		{Name: "name", Required: true},
		{Name: "note"},
		{Name: "files", Variadic: true},
	}, entry.Args)

	flags := map[string]toolspec.ManifestFlag{}
	for _, f := range entry.Flags {
		flags[f.Name] = f
	}
	mode := flags["mode"]
	assert.True(t, mode.Required)
	assert.Equal(t, []string{"fast", "safe"}, mode.Choices)
	assert.Equal(t, "fast", mode.Default)
	assert.Equal(t, "Attach mode (one of: fast, safe)", mode.Description,
		"the help suffix stays; choices duplicates it on purpose")
	label := flags["label"]
	assert.False(t, label.Required)
	assert.Nil(t, label.Choices)
}

// TestSpecV12_LatestIsKnown pins that the newest layout is the one
// negotiation raises to and that a 1.1 declaration still answers a
// 1.2 request with 1.2.
func TestSpecV12_LatestIsKnown(t *testing.T) {
	assert.Equal(t, "1.2", toolspec.LatestSchemaVersion)
	assert.Equal(t, "1.2", toolspec.NegotiateSchemaVersion("1.1", "1.2"))
	assert.Equal(t, "1.2", toolspec.NegotiateSchemaVersion("1.0", "9.9"))
}
