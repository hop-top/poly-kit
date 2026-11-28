package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/upgrade"
)

// published records the events a Migrator publishes.
type published struct {
	mu     sync.Mutex
	events []map[string]string
}

func (p *published) Publish(_ context.Context, topic, _ string, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ev := map[string]string{"topic": topic}
	if mp, ok := payload.(upgrade.MigrationPayload); ok {
		ev["method"] = mp.Method
	}
	p.events = append(p.events, ev)
	return nil
}

func (p *published) method(topic string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ev := range p.events {
		if ev["topic"] == topic {
			return ev["method"], true
		}
	}
	return "", false
}

const revertedTopic = "kit.core.migration.reverted"

// columns lists t's columns, on its own short-lived connection.
func columns(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", fileDSN(path))
	require.NoError(t, err)
	defer db.Close()
	r, err := db.Query(`SELECT name FROM pragma_table_info('t') ORDER BY cid`)
	require.NoError(t, err)
	defer r.Close()
	var out []string
	for r.Next() {
		var n string
		require.NoError(t, r.Scan(&n))
		out = append(out, n)
	}
	require.NoError(t, r.Err())
	return out
}

// addColumnThenFail is an Up that changes the schema on its own
// connection, then fails: a partial change.
func addColumnThenFail(path, col string) func(context.Context) error {
	return func(context.Context) error {
		db, err := sql.Open("sqlite", fileDSN(path))
		if err != nil {
			return err
		}
		defer db.Close()
		if _, err := db.Exec(`ALTER TABLE t ADD COLUMN ` + col + ` TEXT`); err != nil {
			return err
		}
		return errors.New("boom")
	}
}

// failedUpEnv is runEnv with the setup handle released, so a path-only
// Restore may replace the file.
func failedUpEnv(t *testing.T) runEnv {
	t.Helper()
	e := newRunEnv(t)
	require.NoError(t, e.db.Close())
	return e
}

func backupDirs(t *testing.T, e runEnv) []string {
	t.Helper()
	ents, err := os.ReadDir(join(e.dataDir, "backups", "appdb"))
	require.NoError(t, err)
	var out []string
	for _, ent := range ents {
		if ent.IsDir() {
			out = append(out, ent.Name())
		}
	}
	return out
}

// The first migration's Up fails after a partial change: the run
// restores the backup it took, so the change is gone and the version
// stays the pre-run one.
func TestRun_FailedFirstUpRestoresBackup(t *testing.T) {
	e := failedUpEnv(t)
	d := e.driver()
	reg := upgrade.NewRegistry()
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "appdb",
		Up: addColumnThenFail(e.dbPath, "note")})
	pub := &published{}
	m := e.migrator(reg, d, upgrade.WithEventPublisher(pub))

	err := m.Run(context.Background())
	require.ErrorContains(t, err, "up 1.1.0 failed: boom")
	require.NotErrorIs(t, err, upgrade.ErrNotRestored)

	assert.NotContains(t, columns(t, e.dbPath), "note", "the partial change is undone")
	assert.Equal(t, []int{1}, rows(t, e.dbPath))
	assert.Equal(t, "1.0.0", e.version(t))
	assert.Equal(t, []string{"pre-1.1.0"}, backupDirs(t, e), "the backup stays")
	method, ok := pub.method(revertedTopic)
	assert.True(t, ok, "a completed revert is published")
	assert.Equal(t, "restore", method)
	requireLockFree(t, d)
}

// A later migration's Up fails after a partial change: no Down runs,
// the backup is restored, and the earlier step is undone with the rest.
func TestRun_FailedLaterUpRestoresWithoutDown(t *testing.T) {
	e := failedUpEnv(t)
	d := e.driver()
	downs := 0
	reg := upgrade.NewRegistry()
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "appdb",
		Up: func(context.Context) error {
			db, err := sql.Open("sqlite", fileDSN(e.dbPath))
			if err != nil {
				return err
			}
			defer db.Close()
			_, err = db.Exec(`INSERT INTO t (x) VALUES (2)`)
			return err
		},
		Down: func(context.Context) error { downs++; return nil }})
	reg.Register(upgrade.Migration{Version: "1.2.0", Schema: "appdb",
		Up: addColumnThenFail(e.dbPath, "note")})
	pub := &published{}
	m := e.migrator(reg, d, upgrade.WithEventPublisher(pub))

	err := m.Run(context.Background())
	require.ErrorContains(t, err, "up 1.2.0 failed: boom")
	require.NotErrorIs(t, err, upgrade.ErrNotRestored)

	assert.Zero(t, downs, "no Down runs when the newest step cannot be downed")
	assert.NotContains(t, columns(t, e.dbPath), "note")
	assert.Equal(t, []int{1}, rows(t, e.dbPath))
	assert.Equal(t, "1.0.0", e.version(t))
	method, ok := pub.method(revertedTopic)
	assert.True(t, ok)
	assert.Equal(t, "restore", method)
}

// With a handle there is no restore: the failed Up is not downed and
// earlier steps are not downed either; the run ends not restored at the
// last step still applied.
func TestRun_FailedUpWithDBIsNotRestored(t *testing.T) {
	e := newRunEnv(t)
	d := e.driver(WithDB(e.db))
	downs := 0
	reg := upgrade.NewRegistry()
	reg.Register(upgrade.Migration{Version: "1.1.0", Schema: "appdb",
		Up:   func(context.Context) error { _, err := e.db.Exec(`INSERT INTO t (x) VALUES (2)`); return err },
		Down: func(context.Context) error { downs++; return nil }})
	reg.Register(upgrade.Migration{Version: "1.2.0", Schema: "appdb",
		Up: func(context.Context) error { return errors.New("boom") }})
	pub := &published{}
	m := e.migrator(reg, d, upgrade.WithEventPublisher(pub))

	err := m.Run(context.Background())
	require.ErrorIs(t, err, upgrade.ErrNotRestored)
	require.ErrorContains(t, err, "up 1.2.0 failed: boom")

	assert.Zero(t, downs)
	assert.Equal(t, "1.1.0", e.version(t), "stays at the last step applied")
	_, ok := pub.method(revertedTopic)
	assert.False(t, ok, "nothing was reverted")
}
