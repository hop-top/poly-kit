package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func optsForPath(path string, scope Scope) Options {
	switch scope {
	case ScopeUser:
		return Options{UserConfigPath: path}
	case ScopeProject:
		return Options{ProjectConfigPath: path}
	default:
		return Options{SystemConfigPath: path}
	}
}

func TestSet_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("name", "alice", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "name: alice")
}

func TestSet_UpdateExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("name: alice\nport: \"8080\"\n"), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("debug", "true", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "name: alice")
	assert.Contains(t, content, "port:")
	assert.Contains(t, content, "debug: \"true\"")
}

func TestSet_DeepKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("a.b.c", "deep", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "a:")
	assert.Contains(t, content, "b:")
	assert.Contains(t, content, "c: deep")
}

func TestSet_OverwriteValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("name: alice\n"), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("name", "bob", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "name: bob")
	assert.NotContains(t, string(data), "alice")
}

func TestSet_ScopeUser(t *testing.T) {
	userDir := t.TempDir()
	projDir := t.TempDir()
	userPath := filepath.Join(userDir, "config.yaml")
	projPath := filepath.Join(projDir, "config.yaml")

	opts := Options{
		UserConfigPath:    userPath,
		ProjectConfigPath: projPath,
	}

	require.NoError(t, Set("key", "val", ScopeUser, opts))

	// User file should exist with the value.
	data, err := os.ReadFile(userPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "key: val")

	// Project file should NOT exist.
	_, err = os.Stat(projPath)
	assert.True(t, os.IsNotExist(err))
}

func TestSet_EmptyScope(t *testing.T) {
	err := Set("key", "val", ScopeUser, Options{})
	assert.ErrorIs(t, err, ErrEmptyScope)
}

func TestSet_CreatesDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "config.yaml")
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("k", "v", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "k: v")
}

func TestSet_CommentPreservation_Inline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "name: alice # the user name\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("port", "9090", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "the user name")
	assert.Contains(t, content, "port:")
}

func TestSet_CommentPreservation_Block(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "# block comment about name\nname: alice\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("name", "bob", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "block comment about name")
	assert.Contains(t, content, "name: bob")
}

func TestSet_CommentPreservation_Between(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := strings.Join([]string{
		"# first section",
		"alpha: one",
		"# second section",
		"beta: two",
		"",
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("gamma", "three", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "first section")
	assert.Contains(t, content, "second section")
	assert.Contains(t, content, "alpha: one")
	assert.Contains(t, content, "beta: two")
	assert.Contains(t, content, "gamma: three")
}

func TestSet_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("key", "val", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "key: val")
}

func TestSet_WhitespaceOnlyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("  \n\t\n  "), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("key", "val", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "key: val")
}

// TestSetValue_FloatRoundTripsIntoFloatField is the regression test for
// the bug this package had: Set hard-coded !!str, so a float written via
// the config setter came back as the quoted string "0.9" and every later
// load that unmarshalled the key into a float64 failed.
func TestSetValue_FloatRoundTripsIntoFloatField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, SetValue("keyword_threshold", 0.9, ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "keyword_threshold: 0.9")
	assert.NotContains(t, string(data), `"0.9"`)

	var cfg struct {
		KeywordThreshold float64 `yaml:"keyword_threshold"`
	}
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	assert.InDelta(t, 0.9, cfg.KeywordThreshold, 1e-9)
}

// TestSet_FloatStillQuoted documents that Set is unchanged: it remains
// the string-typed path and keeps quoting values that would otherwise
// coerce to a non-string type.
func TestSet_FloatStillQuoted(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"float", "0.9", `keyword_threshold: "0.9"`},
		{"int", "123", `keyword_threshold: "123"`},
		{"bool", "true", `keyword_threshold: "true"`},
		{"null", "null", `keyword_threshold: "null"`},
		{"plain string", "abc", "keyword_threshold: abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			opts := optsForPath(path, ScopeProject)

			require.NoError(t, Set("keyword_threshold", tt.value, ScopeProject, opts))

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(data), tt.want)
		})
	}
}

func TestSetValue_TypedScalars(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"float", 0.9, "k: 0.9"},
		{"int", 3, "k: 3"},
		{"bool true", true, "k: true"},
		{"bool false", false, "k: false"},
		{"nil", nil, "k: null"},
		{"string", "abc", "k: abc"},
		{"numeric string stays quoted", "0.9", `k: "0.9"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			opts := optsForPath(path, ScopeProject)

			require.NoError(t, SetValue("k", tt.value, ScopeProject, opts))

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(data), tt.want)
		})
	}
}

// TestYAML11Lookalikes locks in how yes/on/off/no are emitted by each
// path, measured against yaml.v3 v3.0.1. The two paths differ, and the
// difference is load-bearing:
//
//   - Set hand-builds a node with Tag "!!str" and zero Style. yaml.v3
//     emits the bare token (`k: yes`), because under the YAML 1.2 core
//     schema it reads back as a string anyway and no quoting is needed.
//     That means Set does NOT protect these values from a YAML 1.1
//     parser, which would resolve `yes` to boolean true. Documented, not
//     fixed here.
//   - SetValue routes through yaml.Node.Encode, which additionally sets
//     Style to double-quoted for exactly these lookalikes, so they are
//     emitted as `k: "yes"` and are safe for a YAML 1.1 reader too.
//
// Both forms round-trip back to the original string through yaml.v3. If a
// yaml.v3 bump changes either emission, this test fails loudly.
func TestYAML11Lookalikes(t *testing.T) {
	for _, value := range []string{"yes", "no", "on", "off"} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			setPath := filepath.Join(dir, "set.yaml")
			valuePath := filepath.Join(dir, "setvalue.yaml")

			require.NoError(t, Set("k", value, ScopeProject, optsForPath(setPath, ScopeProject)))
			require.NoError(t, SetValue("k", value, ScopeProject, optsForPath(valuePath, ScopeProject)))

			setData, err := os.ReadFile(setPath)
			require.NoError(t, err)
			valueData, err := os.ReadFile(valuePath)
			require.NoError(t, err)

			assert.Equal(t, "k: "+value+"\n", string(setData),
				"Set emits the bare token, unquoted")
			assert.Equal(t, "k: \""+value+"\"\n", string(valueData),
				"SetValue quotes YAML 1.1 lookalikes via Encode")

			// Both read back as the original string under yaml.v3.
			for _, data := range [][]byte{setData, valueData} {
				var back struct {
					K any `yaml:"k"`
				}
				require.NoError(t, yaml.Unmarshal(data, &back))
				assert.Equal(t, value, back.K)
			}
		})
	}
}

// TestSetValue_AppendBranch exercises the branch that appends a new
// key-value pair to an existing mapping. The tag bug was present here too.
func TestSetValue_AppendBranch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("name: alice\n"), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, SetValue("threshold", 0.9, ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "name: alice")
	assert.Contains(t, string(data), "threshold: 0.9")
}

// TestSetValue_UpdateBranch exercises the branch that overwrites an
// existing key, including retagging a value previously written as !!str.
func TestSetValue_UpdateBranch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("threshold: \"0.5\"\n"), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, SetValue("threshold", 0.9, ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "threshold: 0.9\n", string(data))
}

// TestWriteDoesNotCorruptNeighbors pins the blast radius of a write.
// Every write re-emits the entire document, so this asserts the property
// that makes that safe: nodes the write did not target keep the tags the
// decoder gave them and survive the round trip untouched.
//
// This passes both before and after the fix — the old helper only ever
// retagged the node it was aimed at, so the corruption was confined to
// the targeted key rather than spreading to neighbors. It is kept as a
// standing guard on re-emission, not as a regression test for the bug.
// The tests that actually fail against the old helper are
// TestSetValue_UpdateBranch (typed write onto an existing key) and
// TestSetValue_RepeatedWritesAreStable (the sticky-downgrade case).
func TestWriteDoesNotCorruptNeighbors(t *testing.T) {
	const original = "threshold: 0.9\nretries: 3\nenabled: true\nmissing: null\nname: alice\n"

	writers := map[string]func(path string) error{
		"SetValue": func(path string) error {
			return SetValue("other", "x", ScopeProject, optsForPath(path, ScopeProject))
		},
		"Set": func(path string) error {
			return Set("other", "x", ScopeProject, optsForPath(path, ScopeProject))
		},
	}

	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

			require.NoError(t, write(path))

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			content := string(data)

			// Every pre-existing value keeps its original, unquoted form.
			assert.Contains(t, content, "threshold: 0.9")
			assert.Contains(t, content, "retries: 3")
			assert.Contains(t, content, "enabled: true")
			assert.Contains(t, content, "missing: null")
			assert.Contains(t, content, "name: alice")
			assert.NotContains(t, content, `"0.9"`)
			assert.NotContains(t, content, `"3"`)
			assert.NotContains(t, content, `"true"`)

			// And they still decode into their proper Go types.
			var cfg struct {
				Threshold float64 `yaml:"threshold"`
				Retries   int     `yaml:"retries"`
				Enabled   bool    `yaml:"enabled"`
				Missing   *string `yaml:"missing"`
			}
			require.NoError(t, yaml.Unmarshal(data, &cfg))
			assert.InDelta(t, 0.9, cfg.Threshold, 1e-9)
			assert.Equal(t, 3, cfg.Retries)
			assert.True(t, cfg.Enabled)
			assert.Nil(t, cfg.Missing)
		})
	}
}

// TestSetValue_RepeatedWritesAreStable guards the "sticky corruption"
// property directly: repeatedly rewriting one key must never degrade the
// document, and the file must reach a fixed point.
func TestSetValue_RepeatedWritesAreStable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("threshold: 0.9\nretries: 3\n"), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, SetValue("threshold", 0.75, ScopeProject, opts))
	first, err := os.ReadFile(path)
	require.NoError(t, err)

	for range 3 {
		require.NoError(t, SetValue("threshold", 0.75, ScopeProject, opts))
		again, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, string(first), string(again), "writes must reach a fixed point")
	}

	assert.Contains(t, string(first), "threshold: 0.75")
	assert.Contains(t, string(first), "retries: 3")
	assert.NotContains(t, string(first), `"`)
}

// TestSet_DowngradesTypedNeighborValue characterizes the one residual
// sharp edge: Set is the string path, so pointing it AT a typed key still
// stringifies that key. That is inherent to Set's contract (it takes a
// string), not the bug — the bug was that it also corrupted keys it was
// not aimed at, which TestWriteDoesNotCorruptNeighbors now forbids.
// Callers wanting to preserve type must use SetValue/ParseScalar.
func TestSet_DowngradesTypedNeighborValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("threshold: 0.9\n"), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, Set("threshold", "0.5", ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "threshold: \"0.5\"\n", string(data))

	// SetValue with ParseScalar is the type-preserving alternative.
	require.NoError(t, SetValue("threshold", ParseScalar("0.5"), ScopeProject, opts))
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "threshold: 0.5\n", string(data))
}

func TestSetValue_DeepKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, SetValue("a.b.threshold", 0.9, ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "threshold: 0.9")

	var cfg struct {
		A struct {
			B struct {
				Threshold float64 `yaml:"threshold"`
			} `yaml:"b"`
		} `yaml:"a"`
	}
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	assert.InDelta(t, 0.9, cfg.A.B.Threshold, 1e-9)
}

func TestSetValue_NonScalar(t *testing.T) {
	tests := []struct {
		name  string
		value any
		wants []string
	}{
		{"map", map[string]any{"host": "localhost", "port": 5432}, []string{"host: localhost", "port: 5432"}},
		{"slice", []string{"a", "b"}, []string{"- a", "- b"}},
		{"struct", struct {
			Host string `yaml:"host"`
			TLS  bool   `yaml:"tls"`
		}{Host: "example.com", TLS: true}, []string{"host: example.com", "tls: true"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			opts := optsForPath(path, ScopeProject)

			require.NoError(t, SetValue("db", tt.value, ScopeProject, opts))

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			content := string(data)
			assert.Contains(t, content, "db:")
			for _, want := range tt.wants {
				assert.Contains(t, content, want)
			}
		})
	}
}

// TestSetValue_NonScalarReplacesScalar verifies a non-scalar node splices
// in cleanly over a previously scalar value.
func TestSetValue_NonScalarReplacesScalar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("db: sqlite\n"), 0o644))
	opts := optsForPath(path, ScopeProject)

	require.NoError(t, SetValue("db", map[string]string{"driver": "postgres"}, ScopeProject, opts))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "driver: postgres")
	assert.NotContains(t, string(data), "sqlite")
}

func TestSetValue_CommentPreservation(t *testing.T) {
	tests := []struct {
		name     string
		original string
		key      string
		value    any
		wants    []string
	}{
		{
			name:     "inline comment on untouched key",
			original: "name: alice # the user name\n",
			key:      "threshold",
			value:    0.9,
			wants:    []string{"the user name", "threshold: 0.9"},
		},
		{
			name:     "block comment above overwritten key",
			original: "# block comment about threshold\nthreshold: \"0.5\"\n",
			key:      "threshold",
			value:    0.9,
			wants:    []string{"block comment about threshold", "threshold: 0.9"},
		},
		{
			name:     "inline comment on overwritten key",
			original: "threshold: \"0.5\" # tuning knob\n",
			key:      "threshold",
			value:    0.9,
			wants:    []string{"tuning knob", "threshold: 0.9"},
		},
		{
			name:     "comments between entries",
			original: "# first section\nalpha: one\n# second section\nbeta: two\n",
			key:      "gamma",
			value:    3,
			wants:    []string{"first section", "second section", "alpha: one", "beta: two", "gamma: 3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tt.original), 0o644))
			opts := optsForPath(path, ScopeProject)

			require.NoError(t, SetValue(tt.key, tt.value, ScopeProject, opts))

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			for _, want := range tt.wants {
				assert.Contains(t, string(data), want)
			}
		})
	}
}

func TestSetValue_EmptyScope(t *testing.T) {
	err := SetValue("key", 1, ScopeUser, Options{})
	assert.ErrorIs(t, err, ErrEmptyScope)
}

func TestSetValue_UnencodableValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	opts := optsForPath(path, ScopeProject)

	err := SetValue("k", func() {}, ScopeProject, opts)
	require.Error(t, err)

	// The file must not be created when encoding fails.
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr))
}
