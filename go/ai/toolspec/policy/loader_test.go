package policy_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec/policy"
)

func writeTempYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "policy.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

func TestLoadFromFile_OK(t *testing.T) {
	t.Parallel()
	p := writeTempYAML(t, `
schema_version: "1.0"
rules:
  - side_effect: read
    network: none
    action: prompt
    reason: "tightened"
`)
	tbl, err := policy.LoadFromFile(p)
	require.NoError(t, err)
	require.Len(t, tbl.Rules, 1)
	assert.Equal(t, p, tbl.Rules[0].Source)
}

func TestLoadFromFile_MissingPath(t *testing.T) {
	t.Parallel()
	_, err := policy.LoadFromFile("/does/not/exist/policy.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/does/not/exist/policy.yaml")
}

func TestLoadOrDefault_EmptyPathReturnsDefault(t *testing.T) {
	t.Parallel()
	tbl, err := policy.LoadOrDefault("")
	require.NoError(t, err)
	def := policy.Default()
	assert.Equal(t, def.SchemaVersion, tbl.SchemaVersion)
	assert.Len(t, tbl.Rules, len(def.Rules))
}

func TestLoadOrDefault_OverlayMerges(t *testing.T) {
	t.Parallel()
	p := writeTempYAML(t, `
schema_version: "1.0"
rules:
  - side_effect: read
    network: none
    action: deny
    reason: "lockdown"
`)
	tbl, err := policy.LoadOrDefault(p)
	require.NoError(t, err)
	d := tbl.Resolve(policy.SideEffectRead, policy.NetworkNone)
	assert.Equal(t, policy.ActionDeny, d.Action)
	assert.Equal(t, p, d.Source)

	// Non-overridden cells keep base behavior.
	d2 := tbl.Resolve(policy.SideEffectDestructive, policy.NetworkEgress)
	assert.Equal(t, policy.ActionDeny, d2.Action)
	assert.Equal(t, "default.yaml", d2.Source)
}
