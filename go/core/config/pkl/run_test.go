package pkl

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
