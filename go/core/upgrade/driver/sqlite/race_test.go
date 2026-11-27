package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/upgrade"
)

const helperEnv = "SQLITE_DRIVER_RACE_DIR"

// TestHelperMigrate is the second process of TestLock_TwoProcessesApplyOnce:
// the test binary re-executed. It is a no-op in a normal run.
func TestHelperMigrate(t *testing.T) {
	dir := os.Getenv(helperEnv)
	if dir == "" {
		t.Skip("helper process only")
	}
	db, err := sql.Open("sqlite", fileDSN(join(dir, "app.db")))
	require.NoError(t, err)
	defer db.Close()
	reg := upgrade.NewRegistry()
	for _, v := range []string{"1.1.0", "1.2.0"} {
		reg.Register(upgrade.Migration{Version: v, Schema: "appdb",
			Up: func(context.Context) error {
				time.Sleep(200 * time.Millisecond) // a window for the other process
				_, err := db.Exec(`INSERT INTO applied (version, pid) VALUES (?, ?)`, v, os.Getpid())
				return err
			}})
	}
	data := join(dir, "data")
	m := upgrade.NewMigrator("tool", "1.2.0", upgrade.WithRegistry(reg), upgrade.WithDataDir(data))
	m.AddDriver(New(join(dir, "app.db"), WithName("appdb"), WithDataDir(data)))
	require.NoError(t, m.Run(context.Background()))
}

// Two processes start the same migrations on one file at once: each
// migration is applied once, by whichever process got the lock.
func TestLock_TwoProcessesApplyOnce(t *testing.T) {
	dir := t.TempDir()
	db := openWAL(t, join(dir, "app.db"))
	_, err := db.Exec(`CREATE TABLE applied (version TEXT, pid INTEGER)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	var cmds []*exec.Cmd
	var outs []*strings.Builder
	for range 2 {
		c := exec.Command(os.Args[0], "-test.run=^TestHelperMigrate$", "-test.count=1")
		c.Env = append(os.Environ(), helperEnv+"="+dir)
		var out strings.Builder
		c.Stdout, c.Stderr = &out, &out
		cmds, outs = append(cmds, c), append(outs, &out)
	}
	for _, c := range cmds {
		require.NoError(t, c.Start())
	}
	for i, c := range cmds {
		require.NoError(t, c.Wait(), "helper %d:\n%s", i, outs[i])
		assert.NotContains(t, outs[i].String(), "SKIP", "helper %d did not run", i)
	}

	got := map[string]int{}
	r, err := mustOpen(t, join(dir, "app.db")).Query(`SELECT version FROM applied`)
	require.NoError(t, err)
	defer r.Close()
	for r.Next() {
		var v string
		require.NoError(t, r.Scan(&v))
		got[v]++
	}
	assert.Equal(t, map[string]int{"1.1.0": 1, "1.2.0": 1}, got, fmt.Sprint(got))
	v, err := upgrade.ReadVersionFileIn(join(dir, "data"), "appdb")
	require.NoError(t, err)
	assert.Equal(t, "1.2.0", v)
}

func mustOpen(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", fileDSN(path))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
