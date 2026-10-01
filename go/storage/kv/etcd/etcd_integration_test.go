package etcd_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/etcd"
)

func startEtcd(t *testing.T) string {
	t.Helper()
	_, endpoint := startEtcdContainer(t)
	return endpoint
}

func startEtcdContainer(t *testing.T) (testcontainers.Container, string) {
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
	return container, endpoint
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

// Credentials from kv.Config reach a real cluster with auth enabled: the
// right ones open and write, none are refused by the server, and a wrong
// password fails the open without the password appearing in the error.
func TestEtcdIntegrationAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	container, endpoint := startEtcdContainer(t)
	ctx := context.Background()
	for _, cmd := range [][]string{
		{"/usr/local/bin/etcdctl", "user", "add", "root:kit-secret"},
		{"/usr/local/bin/etcdctl", "user", "grant-role", "root", "root"},
		{"/usr/local/bin/etcdctl", "auth", "enable"},
	} {
		code, out, err := container.Exec(ctx, cmd)
		require.NoError(t, err)
		if code != 0 {
			msg, _ := io.ReadAll(out)
			t.Fatalf("%v: exit %d: %s", cmd, code, msg)
		}
	}

	open := func(t *testing.T, username, password string) (kv.Store, error) {
		t.Helper()
		octx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return kv.OpenContext(octx, kv.Config{
			Backend:   "etcd",
			Endpoints: []string{endpoint},
			Prefix:    "auth/",
			Username:  username,
			Password:  password,
		})
	}

	t.Run("Credentials", func(t *testing.T) {
		store, err := open(t, "root", "kit-secret")
		require.NoError(t, err)
		defer store.Close()
		require.NoError(t, store.Put(ctx, "k", []byte("v")))
		val, ok, err := store.Get(ctx, "k")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, []byte("v"), val)
	})

	t.Run("NoCredentials", func(t *testing.T) {
		store, err := open(t, "", "")
		require.NoError(t, err, "an unauthenticated open is lazy and must not fail")
		defer store.Close()
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		assert.Error(t, store.Put(pctx, "k", []byte("v")), "server accepted an unauthenticated write")
	})

	t.Run("WrongPassword", func(t *testing.T) {
		store, err := open(t, "root", "kit-wrong-secret")
		if err == nil {
			_ = store.Close()
			t.Fatal("open with a wrong password succeeded")
		}
		assert.False(t, strings.Contains(err.Error(), "kit-wrong-secret"), "error leaks the password: %v", err)
	})
}
