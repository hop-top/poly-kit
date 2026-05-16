// toolspec_test.go covers the `kit toolspec` discovery subcommand.
// Tests run against a synthetic kit Root with a fixture leaf so the
// assertions don't depend on the live kit subcommand tree.

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
	kitcli "hop.top/kit/go/console/cli"
)

// fixtureRoot builds a minimal kit Root with one annotated leaf so
// the manifest emitter has something to project. We deliberately
// skip the real serveCmd/symlinkCmd wiring — the toolspec command
// only needs the cobra tree shape, not the runtime behavior.
func fixtureRoot(t *testing.T) *kitcli.Root {
	t.Helper()
	r := kitcli.New(kitcli.Config{
		Name:    "kit",
		Version: "test",
		Short:   "kit binary fixture for toolspec tests",
	})
	leaf := &cobra.Command{
		Use:   "ping",
		Short: "fixture leaf",
		RunE:  func(*cobra.Command, []string) error { return nil },
	}
	kitcli.SetSideEffect(leaf, kitcli.SideEffectRead)
	kitcli.SetIdempotency(leaf, kitcli.IdempotencyYes)
	r.Cmd.AddCommand(leaf)
	return r
}

// runToolspec wires the toolspec command onto fixtureRoot and runs
// it with the given args, capturing stdout/stderr.
func runToolspec(t *testing.T, args ...string) (string, error) {
	t.Helper()
	r := fixtureRoot(t)
	cmd := toolspecCmd(r)
	r.Cmd.AddCommand(cmd)

	var outBuf bytes.Buffer
	r.Cmd.SetOut(&outBuf)
	r.Cmd.SetErr(&bytes.Buffer{})
	r.Cmd.SetArgs(append([]string{"toolspec"}, args...))
	r.WrapRunE()
	err := r.Cmd.Execute()
	return outBuf.String(), err
}

func TestToolspecCmd_FullManifest(t *testing.T) {
	out, err := runToolspec(t)
	require.NoError(t, err)

	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal([]byte(out), &m))
	assert.Equal(t, "kit", m.Tool)
	assert.Equal(t, "1.1", m.SchemaVersion,
		"schema version bumped to 1.1 by 12fcc-static §5")
	require.NotEmpty(t, m.Commands, "fixture leaf surfaces in manifest")
	assert.Equal(t, []string{"kit", "ping"}, m.Commands[0].Path)
}

func TestToolspecCmd_PolicySubcommand(t *testing.T) {
	out, err := runToolspec(t, "policy")
	require.NoError(t, err)
	assert.Contains(t, out, `"schema_version": "1.0"`)
	assert.Contains(t, out, "auto-allow")
	assert.Contains(t, out, "destructive")
}

func TestToolspecCmd_PolicyOverlayFile(t *testing.T) {
	dir := t.TempDir()
	overlay := dir + "/custom.yaml"
	require.NoError(t, os.WriteFile(overlay, []byte(`
schema_version: "1.0"
rules:
  - side_effect: read
    network: none
    action: deny
    reason: "production lockdown"
`), 0o644))

	out, err := runToolspec(t, "policy", "--file", overlay)
	require.NoError(t, err)
	assert.Contains(t, out, "production lockdown")
	assert.Contains(t, out, overlay, "rule source attribution carries through")
}

func TestToolspecCmd_PolicyFileMissing(t *testing.T) {
	_, err := runToolspec(t, "policy", "--file", "/no/such/file.yaml")
	require.Error(t, err)
}

func TestToolspecCmd_VersionOnly(t *testing.T) {
	out, err := runToolspec(t, "--version")
	require.NoError(t, err)

	var payload struct {
		SchemaVersion string `json:"schema_version"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, "1.1", payload.SchemaVersion,
		"schema version bumped to 1.1 by 12fcc-static §5")
}

func TestNegotiateSchemaVersion_DefaultsToBinary(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		want      string
	}{
		{"unset", "", "1.0"},
		{"matching", "1.0", "1.0"},
		{"future-major", "2.0", "1.0"}, // kit downgrades to highest known
		{"malformed", "garbage", "1.0"},
		{"partial", "1", "1.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := negotiateSchemaVersion("1.0", tc.requested)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseSchemaVersion(t *testing.T) {
	cases := []struct {
		in    string
		major int
		minor int
		ok    bool
	}{
		{"1.0", 1, 0, true},
		{"2.5", 2, 5, true},
		{"10.20", 10, 20, true},
		{"", 0, 0, false},
		{"1", 0, 0, false},
		{"junk", 0, 0, false},
		{"1.x", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			major, minor, ok := parseSchemaVersion(tc.in)
			assert.Equal(t, tc.ok, ok)
			if ok {
				assert.Equal(t, tc.major, major)
				assert.Equal(t, tc.minor, minor)
			}
		})
	}
}
