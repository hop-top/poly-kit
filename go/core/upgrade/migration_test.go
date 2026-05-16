package upgrade

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterMigration(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	m := Migration{
		Version: "1.0.0",
		Schema:  "test",
		Up:      func(ctx context.Context) error { return nil },
	}
	RegisterMigration(m)

	registryMu.Lock()
	defer registryMu.Unlock()
	require.Len(t, registry, 1)
	assert.Equal(t, "1.0.0", registry[0].Version)
}

func TestRegisterMigration_NilUpPanics(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	assert.Panics(t, func() {
		RegisterMigration(Migration{Version: "1.0.0", Schema: "test"})
	})
}

// noop is a no-op migration function for tests.
var noop = func(ctx context.Context) error { return nil }

func TestPendingMigrations_BasicRange(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterMigration(Migration{Version: "1.0.0", Schema: "db", Up: noop})
	RegisterMigration(Migration{Version: "1.1.0", Schema: "db", Up: noop})
	RegisterMigration(Migration{Version: "1.2.0", Schema: "db", Up: noop})
	RegisterMigration(Migration{Version: "2.0.0", Schema: "db", Up: noop})
	RegisterMigration(Migration{Version: "1.1.0", Schema: "other", Up: noop})

	pending := pendingMigrations("db", "1.0.0", "2.0.0")

	require.Len(t, pending, 3)
	assert.Equal(t, "1.1.0", pending[0].Version)
	assert.Equal(t, "1.2.0", pending[1].Version)
	assert.Equal(t, "2.0.0", pending[2].Version)
}

func TestPendingMigrations_ExcludesFrom(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterMigration(Migration{Version: "1.0.0", Schema: "db", Up: noop})
	RegisterMigration(Migration{Version: "1.1.0", Schema: "db", Up: noop})

	pending := pendingMigrations("db", "1.0.0", "1.1.0")

	require.Len(t, pending, 1)
	assert.Equal(t, "1.1.0", pending[0].Version)
}

func TestPendingMigrations_IncludesTo(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterMigration(Migration{Version: "1.0.0", Schema: "db", Up: noop})
	RegisterMigration(Migration{Version: "1.1.0", Schema: "db", Up: noop})
	RegisterMigration(Migration{Version: "1.2.0", Schema: "db", Up: noop})

	pending := pendingMigrations("db", "0.9.0", "1.1.0")

	require.Len(t, pending, 2)
	assert.Equal(t, "1.0.0", pending[0].Version)
	assert.Equal(t, "1.1.0", pending[1].Version)
}

func TestPendingMigrations_PreRelease(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterMigration(Migration{Version: "1.0.0-alpha.1", Schema: "db", Up: noop})
	RegisterMigration(Migration{Version: "1.0.0-alpha.2", Schema: "db", Up: noop})
	RegisterMigration(Migration{Version: "1.0.0-beta.1", Schema: "db", Up: noop})
	RegisterMigration(Migration{Version: "1.0.0", Schema: "db", Up: noop})

	pending := pendingMigrations("db", "1.0.0-alpha.1", "1.0.0")

	require.Len(t, pending, 3)
	assert.Equal(t, "1.0.0-alpha.2", pending[0].Version)
	assert.Equal(t, "1.0.0-beta.1", pending[1].Version)
	assert.Equal(t, "1.0.0", pending[2].Version)
}

func TestPendingMigrations_EmptyFrom(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterMigration(Migration{Version: "0.1.0", Schema: "db", Up: noop})
	RegisterMigration(Migration{Version: "1.0.0", Schema: "db", Up: noop})

	pending := pendingMigrations("db", "", "1.0.0")

	require.Len(t, pending, 2)
	assert.Equal(t, "0.1.0", pending[0].Version)
	assert.Equal(t, "1.0.0", pending[1].Version)
}

func TestPendingMigrations_NoneMatch(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterMigration(Migration{Version: "1.0.0", Schema: "db", Up: noop})

	pending := pendingMigrations("db", "1.0.0", "1.0.0")
	assert.Empty(t, pending)

	pending = pendingMigrations("db", "2.0.0", "3.0.0")
	assert.Empty(t, pending)
}

func TestPendingMigrations_Ordering(t *testing.T) {
	resetRegistry()
	defer resetRegistry()

	RegisterMigration(Migration{
		Version: "1.2.0",
		Schema:  "db",
		Up:      func(ctx context.Context) error { return nil },
	})
	RegisterMigration(Migration{
		Version: "1.0.1",
		Schema:  "db",
		Up:      func(ctx context.Context) error { return nil },
	})
	RegisterMigration(Migration{
		Version: "1.1.0",
		Schema:  "db",
		Up:      func(ctx context.Context) error { return nil },
	})

	pending := pendingMigrations("db", "1.0.0", "2.0.0")

	require.Len(t, pending, 3)
	assert.Equal(t, "1.0.1", pending[0].Version)
	assert.Equal(t, "1.1.0", pending[1].Version)
	assert.Equal(t, "1.2.0", pending[2].Version)
}
