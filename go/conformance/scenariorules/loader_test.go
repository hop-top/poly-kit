package scenariorules_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/conformance/scenariorules"
)

func TestLoadDefault(t *testing.T) {
	d, err := scenariorules.LoadDefault()
	require.NoError(t, err)
	assert.Equal(t, "<embedded>", d.Source)
	assert.Equal(t, scenariorules.SchemaVersionV1, d.SchemaVersion)
	assert.NotEmpty(t, d.RulesVersion)
	assert.NotEmpty(t, d.Verbs)
	assert.NotEmpty(t, d.TopLevelKeys)
	assert.NotEmpty(t, d.CompoundRules)
	// Sanity-check a couple of well-known entries.
	assert.Contains(t, d.Verbs, "exit_code_equals")
	assert.Contains(t, d.TopLevelKeys, "scenario_id")
}

func TestLoadDefaultVerbSet(t *testing.T) {
	d, err := scenariorules.LoadDefault()
	require.NoError(t, err)
	verbs := d.VerbSet()
	_, ok := verbs["exit_code_equals"]
	assert.True(t, ok)
	_, ok = verbs["nonexistent_verb"]
	assert.False(t, ok)
}

func TestLoadDefaultTopLevelKeySet(t *testing.T) {
	d, err := scenariorules.LoadDefault()
	require.NoError(t, err)
	keys := d.TopLevelKeySet()
	_, ok := keys["scenario_id"]
	assert.True(t, ok)
	_, ok = keys["nonexistent_key"]
	assert.False(t, ok)
}

func TestLoadFromPath(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "rules.json")
	require.NoError(t, os.WriteFile(tmp, []byte(`{
  "schema_version": "1",
  "rules_version": "2026.05.12",
  "verbs": ["custom_verb"],
  "top_level_keys": ["custom_key"],
  "compound_rules": [
    {"id": "R1", "description": "key", "kind": "key_at_root", "key": "scenario_id"}
  ]
}`), 0644))
	d, err := scenariorules.LoadFromPath(tmp)
	require.NoError(t, err)
	assert.Equal(t, tmp, d.Source)
	assert.Equal(t, []string{"custom_verb"}, d.Verbs)
}

func TestLoadFromPathMissingFile(t *testing.T) {
	_, err := scenariorules.LoadFromPath("/no/such/file.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open rules file")
}

func TestLoadBytesMissingSchemaVersion(t *testing.T) {
	_, err := scenariorules.LoadBytes([]byte(`{"verbs": [], "top_level_keys": [], "compound_rules": []}`), "<test>")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing schema_version")
}

func TestLoadBytesUnsupportedSchemaVersion(t *testing.T) {
	_, err := scenariorules.LoadBytes([]byte(`{"schema_version": "2", "verbs": [], "top_level_keys": [], "compound_rules": []}`), "<test>")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `schema_version "2" is not supported`)
}

func TestLoadBytesUnknownRuleKind(t *testing.T) {
	_, err := scenariorules.LoadBytes([]byte(`{
  "schema_version": "1",
  "verbs": [],
  "top_level_keys": [],
  "compound_rules": [
    {"id": "RX", "description": "x", "kind": "ufo_rule"}
  ]
}`), "<test>")
	require.Error(t, err)
	assert.True(t, errors.Is(err, scenariorules.ErrUnknownRuleKind))
}

func TestLoadBytesKeyAtRootRequiresKey(t *testing.T) {
	_, err := scenariorules.LoadBytes([]byte(`{
  "schema_version": "1",
  "verbs": [],
  "top_level_keys": [],
  "compound_rules": [
    {"id": "R1", "description": "missing key", "kind": "key_at_root"}
  ]
}`), "<test>")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `requires non-empty "key"`)
}

func TestLoadBytesAnyKeyInSetRequiresKeys(t *testing.T) {
	_, err := scenariorules.LoadBytes([]byte(`{
  "schema_version": "1",
  "verbs": [],
  "top_level_keys": [],
  "compound_rules": [
    {"id": "R3", "description": "missing keys", "kind": "any_key_in_set"}
  ]
}`), "<test>")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `requires non-empty "keys"`)
}

func TestLoadBytesMalformedJSON(t *testing.T) {
	_, err := scenariorules.LoadBytes([]byte(`not json`), "<test>")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse rules file")
}
