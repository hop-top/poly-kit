package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	kitcli "hop.top/kit/go/console/cli"
)

// runMigrate executes `tool migrate <args>` on a kit root, where kit's
// --dry-run and the policy gates apply.
func runMigrate(t *testing.T, m *Migrator, args ...string) (map[string]any, error) {
	t.Helper()
	r := kitcli.New(kitcli.Config{Name: "testapp", Version: "0.0.0", Short: "t", DisableValidate: true},
		kitcli.WithPromptSource(func() *kitcli.PromptTTY { return nil }))
	r.Cmd.AddCommand(MigrateCommand(m, r.Viper))
	var out, errb bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(&errb)
	r.Cmd.SetArgs(append([]string{"migrate"}, args...))
	if err := r.Execute(context.Background()); err != nil {
		return nil, err
	}
	var plan map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &plan), "stdout=%q stderr=%q", out.String(), errb.String())
	return plan, nil
}

func TestMigrateCommand_Run_DryRunPlansPending(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	var applied []string
	RegisterMigration(Migration{
		Version: "1.1.0", Schema: "testdb",
		Up: func(context.Context) error { applied = append(applied, "1.1.0"); return nil },
	})
	d := &mockDriver{name: "testdb", version: "1.0.0"}
	m := NewMigrator("testapp", "1.1.0")
	m.AddDriver(d)

	plan, err := runMigrate(t, m, "run", "--dry-run", "--format", "json")
	require.NoError(t, err)
	effects := plan["effects"].([]any)
	require.Len(t, effects, 1)
	require.Equal(t, "schema:testdb", effects[0].(map[string]any)["target"])
	require.Empty(t, applied, "dry run must not migrate")
	require.False(t, d.backed, "dry run must not back up")
	require.Equal(t, "1.0.0", d.version)
}

func TestMigrateCommand_Rollback_DryRunNamesBackup(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	backup := filepath.Join(data, "hop", "testapp", "backups", "testdb", "pre-1.0.0")
	require.NoError(t, os.MkdirAll(backup, 0o750))

	d := &mockDriver{name: "testdb", version: "1.1.0"}
	m := NewMigrator("testapp", "1.1.0", WithManualRollback())
	m.AddDriver(d)

	plan, err := runMigrate(t, m, "rollback", "--dry-run", "--format", "json")
	require.NoError(t, err)
	effects := plan["effects"].([]any)
	require.Len(t, effects, 1)
	require.Contains(t, effects[0].(map[string]any)["detail"], backup)
	require.False(t, d.restored, "dry run must not restore")

	// Without --dry-run the rollback overwrites state: asked first,
	// refused with no terminal to ask on.
	_, err = runMigrate(t, m, "rollback")
	require.Error(t, err)
	require.False(t, d.restored)
}

func TestMigrateCommand_Rollback_DryRunRefusedInAutoMode(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	_, err := runMigrate(t, NewMigrator("testapp", "1.0.0"), "rollback", "--dry-run")
	require.Error(t, err)
	require.Contains(t, err.Error(), "manual")
}
