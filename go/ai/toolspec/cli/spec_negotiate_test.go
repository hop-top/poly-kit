package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/ai/toolspec/adapters"
	speccli "hop.top/kit/go/ai/toolspec/cli"
	kitcli "hop.top/kit/go/console/cli"
)

// runSpecDeclared registers `spec` with the given declared label and
// runs it with args, returning stdout.
func runSpecDeclared(t *testing.T, declared string, args ...string) string {
	t.Helper()
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list things", kitcli.SideEffectRead, kitcli.IdempotencyYes)
	require.NoError(t, speccli.RegisterSpecCommand(r, declared))

	var outBuf, errBuf bytes.Buffer
	r.Cmd.SetOut(&outBuf)
	r.Cmd.SetErr(&errBuf)
	r.Cmd.SetArgs(append([]string{"spec"}, args...))
	r.WrapRunE()
	require.NoError(t, r.Cmd.Execute(), "stderr=%q", errBuf.String())
	return outBuf.String()
}

func schemaOf(t *testing.T, out string) string {
	t.Helper()
	var payload struct {
		SchemaVersion string `json:"schema_version"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload), "out=%q", out)
	return payload.SchemaVersion
}

// TestSpecCommand_NegotiatesSchemaVersion pins that `<tool> spec`
// reads KIT_TOOLSPEC_SCHEMA through the same negotiator as
// `kit toolspec`: the registered label is the floor, the request
// raises it to the highest version kit emits that the request
// allows, and malformed or lower requests leave the label alone.
// Both the full manifest and the --version probe answer the same.
func TestSpecCommand_NegotiatesSchemaVersion(t *testing.T) {
	cases := []struct {
		name     string
		declared string
		env      string
		set      bool
		want     string
	}{
		{"1.0/unset", "1.0", "", false, "1.0"},
		{"1.0/empty", "1.0", "", true, "1.0"},
		{"1.0/request-1.0", "1.0", "1.0", true, "1.0"},
		{"1.0/request-1.1", "1.0", "1.1", true, "1.1"},
		{"1.0/request-2.0", "1.0", "2.0", true, "1.1"},
		{"1.0/request-0.9", "1.0", "0.9", true, "1.0"},
		{"1.0/malformed", "1.0", "latest", true, "1.0"},
		{"1.1/unset", "1.1", "", false, "1.1"},
		{"1.1/request-1.0", "1.1", "1.0", true, "1.1"},
		{"1.1/request-1.1", "1.1", "1.1", true, "1.1"},
		{"1.1/request-2.0", "1.1", "2.0", true, "1.1"},
		{"1.1/malformed", "1.1", "x.y", true, "1.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(toolspec.SchemaVersionEnv, tc.env)
			} else {
				unsetEnv(t, toolspec.SchemaVersionEnv)
			}

			full := runSpecDeclared(t, tc.declared, "--format", "json")
			assert.Equal(t, tc.want, schemaOf(t, full), "full manifest")

			probe := runSpecDeclared(t, tc.declared, "--version")
			assert.Equal(t, tc.want, schemaOf(t, probe), "--version probe")

			plain := runSpecDeclared(t, tc.declared, "--version", "--format", "text")
			assert.Equal(t, tc.want, strings.TrimSpace(plain), "--version plaintext")
		})
	}
}

// TestSpecCommand_NegotiatesAtRunTime pins that the variable is read
// when `spec` runs, not when it is registered: a harness sets it on
// the process it spawns, long after the adopter wired its root.
func TestSpecCommand_NegotiatesAtRunTime(t *testing.T) {
	unsetEnv(t, toolspec.SchemaVersionEnv)
	r := fixtureRoot(t)
	require.NoError(t, speccli.RegisterSpecCommand(r, "1.0"))
	t.Setenv(toolspec.SchemaVersionEnv, "1.1")

	var outBuf bytes.Buffer
	r.Cmd.SetOut(&outBuf)
	r.Cmd.SetErr(&bytes.Buffer{})
	r.Cmd.SetArgs([]string{"spec", "--version"})
	r.WrapRunE()
	require.NoError(t, r.Cmd.Execute())
	assert.Equal(t, "1.1", schemaOf(t, outBuf.String()))
}

// TestSpecCommand_NegotiatedVersionReachesAdapters pins that non
// kit-manifest adapters see the negotiated version too, not the
// registered label.
func TestSpecCommand_NegotiatedVersionReachesAdapters(t *testing.T) {
	t.Setenv(toolspec.SchemaVersionEnv, "1.1")
	r := fixtureRoot(t)
	addLeaf(r.Cmd, "list", "list things", kitcli.SideEffectRead, kitcli.IdempotencyYes)
	var seen []string
	require.NoError(t, speccli.RegisterSpecCommand(r, "1.0",
		speccli.WithFormatAdapter(versionProbeAdapter{seen: &seen})))

	var outBuf bytes.Buffer
	r.Cmd.SetOut(&outBuf)
	r.Cmd.SetErr(&bytes.Buffer{})
	r.Cmd.SetArgs([]string{"spec", "--format", "version-probe"})
	r.WrapRunE()
	require.NoError(t, r.Cmd.Execute())
	assert.Equal(t, []string{"1.1", "1.1"}, seen,
		"walked spec and render option both carry the negotiated version")
}

// versionProbeAdapter records the schema version it is handed on the
// walked spec and through the render options.
type versionProbeAdapter struct{ seen *[]string }

func (versionProbeAdapter) Name() string        { return "version-probe" }
func (versionProbeAdapter) Aliases() []string   { return nil }
func (versionProbeAdapter) Description() string { return "records the schema version" }
func (versionProbeAdapter) ContentType() string { return "text/plain" }
func (a versionProbeAdapter) Render(_ io.Writer, spec *toolspec.ToolSpec, opts ...adapters.RenderOption) error {
	cfg := adapters.ResolveRenderOptions(opts)
	*a.seen = append(*a.seen, spec.SchemaVersion, cfg.SchemaVersion)
	return nil
}

// unsetEnv removes key for the test's duration and restores it after.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	require.NoError(t, os.Unsetenv(key))
}
