package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateCommand_Status_Table(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	RegisterMigration(Migration{
		Version: "1.1.0", Schema: "testdb",
		Up: func(ctx context.Context) error { return nil },
	})

	d := &mockDriver{name: "testdb", version: "1.0.0"}
	m := NewMigrator("testapp", "1.1.0")
	m.AddDriver(d)

	v := viper.New()
	v.Set("format", "table")
	cmd := MigrateCommand(m, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"status"})
	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "SCHEMA")
	assert.Contains(t, out, "testdb")
	assert.Contains(t, out, "1")
}

func TestMigrateCommand_Status_JSON(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	d := &mockDriver{name: "testdb", version: "2.0.0"}
	m := NewMigrator("testapp", "2.0.0")
	m.AddDriver(d)

	v := viper.New()
	cmd := MigrateCommand(m, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"status", "--format", "json"})
	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, `"schema"`)
	assert.Contains(t, out, `"testdb"`)
}

func TestMigrateCommand_Run_AppliesMigrations(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	var applied []string
	RegisterMigration(Migration{
		Version: "1.1.0", Schema: "testdb",
		Up: func(ctx context.Context) error { applied = append(applied, "1.1.0"); return nil },
	})

	d := &mockDriver{name: "testdb", version: "1.0.0"}
	m := NewMigrator("testapp", "1.1.0")
	m.AddDriver(d)

	v := viper.New()
	cmd := MigrateCommand(m, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"run"})
	err := cmd.Execute()
	require.NoError(t, err)

	assert.Equal(t, []string{"1.1.0"}, applied)
	assert.Contains(t, buf.String(), "Applied 1 migration(s)")
}

func TestMigrateCommand_Run_AlreadyCurrent(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	d := &mockDriver{name: "testdb", version: "2.0.0"}
	m := NewMigrator("testapp", "2.0.0")
	m.AddDriver(d)

	v := viper.New()
	cmd := MigrateCommand(m, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"run"})
	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Already up to date")
}

func TestMigrateCommand_Rollback_AutoModeBlocked(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	m := NewMigrator("testapp", "1.0.0") // auto rollback by default
	v := viper.New()
	cmd := MigrateCommand(m, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"rollback"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manual")
}

func TestMigrateCommand_History_Empty(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	m := NewMigrator("testapp", "1.0.0")
	v := viper.New()
	cmd := MigrateCommand(m, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"history"})
	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "No migrations applied")
}

func TestMigrateCommand_History_AfterRun(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	RegisterMigration(Migration{
		Version: "1.1.0", Schema: "testdb",
		Up: func(ctx context.Context) error { return nil },
	})

	d := &mockDriver{name: "testdb", version: "1.0.0"}
	m := NewMigrator("testapp", "1.1.0")
	m.AddDriver(d)

	v := viper.New()
	v.Set("format", "table")

	// Run first.
	require.NoError(t, m.Run(context.Background()))

	cmd := MigrateCommand(m, v)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"history"})
	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "VERSION")
	assert.Contains(t, out, "1.1.0")
}

func TestMigrateCommand_Status_JSON_Structure(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	RegisterMigration(Migration{
		Version: "1.1.0", Schema: "testdb",
		Up: func(ctx context.Context) error { return nil },
	})

	d := &mockDriver{name: "testdb", version: "1.0.0"}
	m := NewMigrator("testapp", "1.1.0")
	m.AddDriver(d)

	v := viper.New()
	cmd := MigrateCommand(m, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"status", "--format", "json"})
	err := cmd.Execute()
	require.NoError(t, err)

	// Decode into structured type to verify fields.
	type statusEntry struct {
		Schema  string `json:"schema"`
		Current string `json:"current"`
		Target  string `json:"target"`
		Pending int    `json:"pending"`
	}
	var entries []statusEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries), "JSON should decode")
	require.Len(t, entries, 1)
	assert.Equal(t, "testdb", entries[0].Schema)
	assert.Equal(t, "1.0.0", entries[0].Current)
	assert.Equal(t, "1.1.0", entries[0].Target)
	assert.Equal(t, 1, entries[0].Pending)
}

func TestMigrateCommand_StatusNoDrivers(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	m := NewMigrator("testapp", "1.0.0")
	v := viper.New()
	cmd := MigrateCommand(m, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"status"})
	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "No schema drivers registered")
}

// TestMigrateCommand_Status_OutputToFile_CSV exercises the Dispatch
// migration: --output writes to a path, --format csv switches the
// formatter, and the file extension matches the format. Regression
// guard for the T-0990 callsite swap.
func TestMigrateCommand_Status_OutputToFile_CSV(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	RegisterMigration(Migration{
		Version: "1.1.0", Schema: "testdb",
		Up: func(ctx context.Context) error { return nil },
	})

	d := &mockDriver{name: "testdb", version: "1.0.0"}
	m := NewMigrator("testapp", "1.1.0")
	m.AddDriver(d)

	v := viper.New()
	cmd := MigrateCommand(m, v)

	out := filepath.Join(t.TempDir(), "status.csv")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"status", "--format", "csv", "-o", out})
	require.NoError(t, cmd.Execute())

	body, err := os.ReadFile(out)
	require.NoError(t, err)
	contents := string(body)
	// CSV header row uses the table:"" tag headers.
	assert.Contains(t, contents, "SCHEMA")
	assert.Contains(t, contents, "testdb")
	assert.Contains(t, contents, "1.0.0")
}
