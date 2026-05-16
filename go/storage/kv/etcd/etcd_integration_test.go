package etcd_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"hop.top/kit/go/storage/kv/etcd"
)

func startEtcd(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "quay.io/coreos/etcd:v3.5.17",
		ExposedPorts: []string{"2379/tcp"},
		Env: map[string]string{
			"ETCD_ROOT_PASSWORD":         "",
			"ALLOW_NONE_AUTHENTICATION":  "yes",
			"ETCD_ADVERTISE_CLIENT_URLS": "http://0.0.0.0:2379",
			"ETCD_LISTEN_CLIENT_URLS":    "http://0.0.0.0:2379",
		},
		WaitingFor: wait.ForListeningPort("2379/tcp"),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("skipping: could not start etcd container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	endpoint, err := container.Endpoint(ctx, "")
	require.NoError(t, err)
	return endpoint
}

func TestEtcdIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	endpoint := startEtcd(t)
	store, err := etcd.New([]string{endpoint}, "test/")
	require.NoError(t, err)
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
