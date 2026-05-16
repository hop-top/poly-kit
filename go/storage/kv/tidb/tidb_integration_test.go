package tidb_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"hop.top/kit/go/storage/kv/tidb"
)

func startMySQL(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "mysql:8",
		ExposedPorts: []string{"3306/tcp"},
		Env: map[string]string{
			"MYSQL_ROOT_PASSWORD": "test",
			"MYSQL_DATABASE":      "testdb",
		},
		WaitingFor: wait.ForAll(
			wait.ForLog("ready for connections").WithOccurrence(2),
			wait.ForListeningPort("3306/tcp"),
		).WithDeadline(120 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("skipping: could not start mysql container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "3306")
	require.NoError(t, err)
	return fmt.Sprintf("root:test@tcp(%s:%s)/testdb", host, port.Port())
}

func TestTiDBIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	dsn := startMySQL(t)
	store, err := tidb.New(dsn, "test_kv")
	if err != nil {
		t.Skipf("skipping: could not connect to mysql: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	t.Run("PutGet", func(t *testing.T) {
		require.NoError(t, store.Put(ctx, "k1", []byte("v1")))
		val, ok, err := store.Get(ctx, "k1")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, []byte("v1"), val)
	})

	t.Run("GetMissing", func(t *testing.T) {
		_, ok, err := store.Get(ctx, "nonexistent")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("Delete", func(t *testing.T) {
		require.NoError(t, store.Put(ctx, "k2", []byte("v2")))
		require.NoError(t, store.Delete(ctx, "k2"))
		_, ok, err := store.Get(ctx, "k2")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("List", func(t *testing.T) {
		require.NoError(t, store.Put(ctx, "prefix/a", []byte("1")))
		require.NoError(t, store.Put(ctx, "prefix/b", []byte("2")))
		keys, err := store.List(ctx, "prefix/")
		require.NoError(t, err)
		assert.Contains(t, keys, "prefix/a")
		assert.Contains(t, keys, "prefix/b")
	})

	t.Run("Overwrite", func(t *testing.T) {
		require.NoError(t, store.Put(ctx, "ow", []byte("first")))
		require.NoError(t, store.Put(ctx, "ow", []byte("second")))
		val, ok, err := store.Get(ctx, "ow")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, []byte("second"), val)
	})
}
