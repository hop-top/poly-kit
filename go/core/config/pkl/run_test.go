package pkl

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"hop.top/kit/go/core/config"
)

func TestRunWizard_Headless(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	err := RunWizard(context.Background(), "testdata/project.pkl", WizardOpts{
		ConfigOpts: config.Options{ProjectConfigPath: cfgPath},
		Scope:      config.ScopeProject,
		Headless:   map[string]any{"name": "myapp", "lang": "go", "git": true, "port": "9090"},
	})
	require.NoError(t, err)

	val, err := config.Get("name", config.Options{ProjectConfigPath: cfgPath})
	require.NoError(t, err)
	assert.Equal(t, "myapp", val)

	val, err = config.Get("port", config.Options{ProjectConfigPath: cfgPath})
	require.NoError(t, err)
	assert.Equal(t, "9090", val)
}

func TestRunWizard_DryRun(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	err := RunWizard(context.Background(), "testdata/project.pkl", WizardOpts{
		ConfigOpts: config.Options{ProjectConfigPath: cfgPath},
		Scope:      config.ScopeProject,
		DryRun:     true,
		Headless:   map[string]any{"name": "test"},
	})
	require.NoError(t, err)

	_, err = os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(err))
}

func TestPrefillDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfgOpts := config.Options{ProjectConfigPath: cfgPath}
	require.NoError(t, config.Set("name", "existing-app", config.ScopeProject, cfgOpts))

	schema, err := LoadSchema("testdata/project.pkl")
	require.NoError(t, err)

	fields := prefillDefaults(schema.Fields, cfgOpts)

	for _, f := range fields {
		if f.Path == "name" {
			assert.Equal(t, "existing-app", f.Default)
		}
	}
}

func TestRunWizard_PrefillsFromExisting(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgOpts := config.Options{ProjectConfigPath: cfgPath}

	require.NoError(t, config.Set("name", "old-app", config.ScopeProject, cfgOpts))

	err := RunWizard(context.Background(), "testdata/project.pkl", WizardOpts{
		ConfigOpts: cfgOpts,
		Scope:      config.ScopeProject,
		Headless:   map[string]any{"lang": "python", "git": false, "port": "3000"},
	})
	require.NoError(t, err)

	val, err := config.Get("name", cfgOpts)
	require.NoError(t, err)
	assert.Equal(t, "old-app", val)
}

// TestRunWizard_TypedWrites is the regression test for the wizard
// writing every answer as a quoted string. It unmarshals the generated
// file into a typed struct: if numbers, bools or lists are written as
// strings the unmarshal fails outright.
func TestRunWizard_TypedWrites(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	err := RunWizard(context.Background(), "testdata/typed.pkl", WizardOpts{
		ConfigOpts: config.Options{ProjectConfigPath: cfgPath},
		Scope:      config.ScopeProject,
		Headless: map[string]any{
			"name":    "myapp",
			"retries": "7",
			"ratio":   "0.25",
			"enabled": false,
			"env":     "prod",
			"timeout": "45s",
		},
	})
	require.NoError(t, err)

	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	var got struct {
		Name    string  `yaml:"name"`
		Retries int     `yaml:"retries"`
		Ratio   float64 `yaml:"ratio"`
		Enabled bool    `yaml:"enabled"`
		Env     string  `yaml:"env"`
		Timeout string  `yaml:"timeout"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &got), "config: %s", raw)

	assert.Equal(t, "myapp", got.Name)
	assert.Equal(t, 7, got.Retries)
	assert.InDelta(t, 0.25, got.Ratio, 1e-9)
	assert.False(t, got.Enabled)
	assert.Equal(t, "prod", got.Env)
	assert.Equal(t, "45s", got.Timeout)
}

// TestRunWizard_TypedWrites_RawYAML asserts the bytes on disk, not just
// what unmarshals. Numeric and bool fields must be unquoted scalars and
// the string list must be a real YAML sequence — the old %v path wrote
// the literal `tags: "[alpha beta]"`, which is not a sequence at all.
func TestRunWizard_TypedWrites_RawYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	err := RunWizard(context.Background(), "testdata/typed.pkl", WizardOpts{
		ConfigOpts: config.Options{ProjectConfigPath: cfgPath},
		Scope:      config.ScopeProject,
		Headless: map[string]any{
			"name":    "myapp",
			"retries": "7",
			"ratio":   "0.25",
			"enabled": false,
			"env":     "prod",
			"timeout": "45s",
		},
	})
	require.NoError(t, err)

	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	out := string(raw)

	assert.Contains(t, out, "retries: 7")
	assert.Contains(t, out, "ratio: 0.25")
	assert.Contains(t, out, "enabled: false")
	assert.NotContains(t, out, `"7"`)
	assert.NotContains(t, out, `"false"`)

	// Genuine string fields stay strings and still round-trip.
	var probe map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &probe))
	assert.IsType(t, "", probe["name"])
	assert.IsType(t, "", probe["env"])
	assert.IsType(t, "", probe["timeout"], "duration writes as string")
	assert.IsType(t, 0, probe["retries"])
	assert.IsType(t, false, probe["enabled"])
}

// TestWriteConfig_TypedValues drives writeConfig with exactly the Go
// values the pkl evaluator produces. Resolve decodes its JSON output
// into map[string]any, so integers arrive as float64 and string lists
// as []any — verified against the real pkl binary. The wizard's own
// headless layer requires string answers for TextInput steps, which
// masks those shapes, so writeConfig is exercised directly here.
//
// This is the case that proves TypeStringList was destroyed rather than
// merely mistyped: fmt.Sprintf("%v", []any{"alpha","beta"}) is the
// literal "[alpha beta]", which no YAML parser reads back as a list.
func TestWriteConfig_TypedValues(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgOpts := config.Options{ProjectConfigPath: cfgPath}

	schema, err := LoadSchema("testdata/typed.pkl")
	require.NoError(t, err)

	// Shapes produced by Resolve's JSON round-trip.
	results := map[string]any{
		"name":    "myapp",
		"retries": float64(7),
		"ratio":   float64(0.25),
		"enabled": false,
		"env":     "prod",
		"timeout": "45s",
		"tags":    []any{"alpha", "beta"},
	}

	require.NoError(t, writeConfig(
		context.Background(), "testdata/typed.pkl", schema, results,
		WizardOpts{ConfigOpts: cfgOpts, Scope: config.ScopeProject},
	))

	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	out := string(raw)

	// Unquoted scalars of the right YAML type.
	assert.Contains(t, out, "retries: 7")
	assert.Contains(t, out, "ratio: 0.25")
	assert.Contains(t, out, "enabled: false")

	// A real sequence, not the Go slice debug-print.
	assert.NotContains(t, out, "[alpha beta]")
	assert.Contains(t, out, "tags:\n  - alpha\n  - beta")

	var got struct {
		Name    string   `yaml:"name"`
		Retries int      `yaml:"retries"`
		Ratio   float64  `yaml:"ratio"`
		Enabled bool     `yaml:"enabled"`
		Env     string   `yaml:"env"`
		Timeout string   `yaml:"timeout"`
		Tags    []string `yaml:"tags"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &got), "config: %s", raw)

	assert.Equal(t, "myapp", got.Name)
	assert.Equal(t, 7, got.Retries)
	assert.InDelta(t, 0.25, got.Ratio, 1e-9)
	assert.False(t, got.Enabled)
	assert.Equal(t, "prod", got.Env)
	assert.Equal(t, "45s", got.Timeout, "duration writes as a string")
	assert.Equal(t, []string{"alpha", "beta"}, got.Tags)

	// Genuine string fields must still be strings, and the enum and
	// duration must not have been coerced into some other type.
	var probe map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &probe))
	assert.IsType(t, "", probe["name"])
	assert.IsType(t, "", probe["env"], "string enum stays a string")
	assert.IsType(t, "", probe["timeout"], "duration stays a string")
	assert.IsType(t, 0, probe["retries"])
	assert.IsType(t, false, probe["enabled"])
}

// TestWriteConfig_StringListSingleElement guards the one-element case,
// where "[alpha]" is closer to plausible-looking YAML than "[a b]".
func TestWriteConfig_StringListSingleElement(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	schema, err := LoadSchema("testdata/typed.pkl")
	require.NoError(t, err)

	require.NoError(t, writeConfig(
		context.Background(), "testdata/typed.pkl", schema,
		map[string]any{"tags": []any{"solo"}},
		WizardOpts{
			ConfigOpts: config.Options{ProjectConfigPath: cfgPath},
			Scope:      config.ScopeProject,
		},
	))

	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	var doc struct {
		Tags []string `yaml:"tags"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	assert.Equal(t, []string{"solo"}, doc.Tags)
}

func TestNewConfigCommand(t *testing.T) {
	cmd := NewConfigCommand("testdata/project.pkl", CommandOpts{
		ConfigOpts: config.Options{
			ProjectConfigPath: filepath.Join(t.TempDir(), "c.yaml"),
		},
	})
	assert.Equal(t, "init", cmd.Use)
	assert.NotNil(t, cmd.RunE)

	assert.NotNil(t, cmd.Flags().Lookup("dry-run"))
	assert.NotNil(t, cmd.Flags().Lookup("answers-file"))
	assert.NotNil(t, cmd.Flags().Lookup("scope"))
}

func TestParseScope(t *testing.T) {
	s, err := parseScope("project")
	require.NoError(t, err)
	assert.Equal(t, config.ScopeProject, s)

	s, err = parseScope("user")
	require.NoError(t, err)
	assert.Equal(t, config.ScopeUser, s)

	s, err = parseScope("system")
	require.NoError(t, err)
	assert.Equal(t, config.ScopeSystem, s)

	_, err = parseScope("invalid")
	assert.Error(t, err)
}
