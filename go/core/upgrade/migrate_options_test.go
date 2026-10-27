package upgrade

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/bus"
)

func TestMigrator_VerifyErrorKinds(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	r := NewRegistry()
	r.Register(Migration{Version: "1.1.0", Schema: "db", Up: noop,
		Verify: func(context.Context) error { return errors.New("bad") }})
	m := NewMigrator("vtool", "1.1.0", WithRegistry(r))
	m.AddDriver(&mockDriver{name: "db", version: "1.0.0"})
	err := m.Run(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrVerify)
	assert.NotErrorIs(t, err, context.Canceled)

	final := NewMigrator("vtool", "1.1.0", WithRegistry(NewRegistry()), WithVerify(
		func(context.Context, string, string) error { return errors.New("x") }))
	final.registry.Register(Migration{Version: "1.1.0", Schema: "db", Up: noop})
	final.AddDriver(&mockDriver{name: "db", version: "1.0.0"})
	assert.ErrorIs(t, final.Run(context.Background()), ErrVerify)
}

func TestMigrator_CanceledErrorKind(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	r := NewRegistry()
	r.Register(Migration{Version: "1.1.0", Schema: "db", Up: noop})
	m := NewMigrator("ctool", "1.1.0", WithRegistry(r))
	m.AddDriver(&mockDriver{name: "db", version: "1.0.0"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := m.Run(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, ErrVerify)
}

func TestMigrator_OwnRegistryIgnoresDefault(t *testing.T) {
	resetRegistry()
	defer resetRegistry()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	RegisterMigration(Migration{Version: "1.1.0", Schema: "db", Up: noop})

	m := NewMigrator("rtool", "2.0.0", WithRegistry(NewRegistry()))
	m.AddDriver(&mockDriver{name: "db", version: "1.0.0"})
	assert.Equal(t, 0, m.Status()[0].Pending)
	assert.Equal(t, 1, NewMigrator("rtool", "2.0.0").withDriver(&mockDriver{name: "db", version: "1.0.0"}).Status()[0].Pending)
	assert.Panics(t, func() { NewRegistry().Register(Migration{Version: "1.0.0"}) })
}

func (m *Migrator) withDriver(d SchemaDriver) *Migrator { m.AddDriver(d); return m }

func TestMigrator_DataDirLeavesXDGAlone(t *testing.T) {
	xdgHome, custom := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdgHome)
	r := NewRegistry()
	r.Register(Migration{Version: "1.1.0", Schema: "db", Up: noop})
	m := NewMigrator("dtool", "1.1.0", WithRegistry(r), WithDataDir(custom))
	m.AddDriver(&mockDriver{name: "db", version: "1.0.0"})
	require.NoError(t, m.Run(context.Background()))

	v, err := ReadVersionFileIn(custom, "db")
	require.NoError(t, err)
	assert.Equal(t, "1.1.0", v)
	assert.DirExists(t, filepath.Join(custom, "backups", "db", "pre-1.1.0"))
	_, err = os.Stat(filepath.Join(xdgHome, "hop"))
	assert.True(t, os.IsNotExist(err), "XDG data home untouched")
}

func TestMigrationTopics(t *testing.T) {
	d := DefaultMigrationTopics
	for _, topic := range []bus.Topic{d.RunStarted, d.RunCompleted, d.RunFailed,
		d.Started, d.Applied, d.Verified, d.Failed, d.Reverted} {
		assert.NoError(t, bus.ValidateTopic(topic), topic)
	}

	m := NewMigrator("t", "1.0.0", WithMigrationTopicPrefix("myapp.core.migration"))
	assert.Equal(t, bus.Topic("myapp.core.migration.applied"), m.topics.Applied)
	assert.Equal(t, bus.Topic("myapp.core.migration.run_started"), m.topics.RunStarted)
	assert.Panics(t, func() { WithMigrationTopicPrefix("too.short") })

	m = NewMigrator("t", "1.0.0", WithMigrationTopics(MigrationTopics{Applied: "myapp.core.migration.applied"}))
	assert.Equal(t, bus.Topic("myapp.core.migration.applied"), m.topics.Applied)
	assert.Equal(t, d.Failed, m.topics.Failed)
}

func TestMigrator_NoPublisherIsSilent(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	r := NewRegistry()
	r.Register(Migration{Version: "1.1.0", Schema: "db", Up: noop})
	m := NewMigrator("stool", "1.1.0", WithRegistry(r))
	m.AddDriver(&mockDriver{name: "db", version: "1.0.0"})
	assert.NoError(t, m.Run(context.Background()))
}
