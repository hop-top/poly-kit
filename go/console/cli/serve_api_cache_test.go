package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/memory"
)

// cacheRoot is a loopback api root with a read command declaring a
// one-minute kit/cache-ttl, counting its runs, with keys set.
func cacheRoot(t *testing.T, keys map[string]any) (*Root, *atomic.Int64) {
	t.Helper()
	r := guardRoot(t, keys)
	runs := &atomic.Int64{}
	stats := &cobra.Command{
		Use:         "stats",
		Short:       "stats",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("run %d", runs.Add(1))
			return nil
		},
	}
	SetCacheTTL(stats, time.Minute)
	r.Cmd.AddCommand(stats)
	return r, runs
}

func getStats(t *testing.T, base, inm string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/v1/commands/stats", nil)
	require.NoError(t, err)
	if inm != "" {
		req.Header.Set("If-None-Match", inm)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(body)
}

func TestAPIResultCache_OnByDefault(t *testing.T) {
	r, runs := cacheRoot(t, nil)
	base, stopServe := serveAPI(t, r)
	var once sync.Once
	stop := func() { once.Do(stopServe) }
	defer stop()

	first, body := getStats(t, base, "")
	require.Equal(t, http.StatusOK, first.StatusCode, body)
	etag := first.Header.Get("ETag")
	require.NotEmpty(t, etag)
	assert.Equal(t, "public, max-age=60", first.Header.Get("Cache-Control"))

	second, again := getStats(t, base, "")
	assert.Equal(t, body, again, "a hit answers the stored result")
	assert.Equal(t, etag, second.Header.Get("ETag"))

	notModified, empty := getStats(t, base, etag)
	assert.Equal(t, http.StatusNotModified, notModified.StatusCode)
	assert.Empty(t, empty)
	assert.Equal(t, int64(1), runs.Load(), "stats ran once for three calls")

	a := apiSvc(t, r)
	a.mu.Lock()
	store, _ := a.cacheStore.(*memory.Store)
	a.mu.Unlock()
	require.NotNil(t, store, "the default store is memory")
	stop()
	_, _, err := store.Get(t.Context(), "k")
	assert.ErrorIs(t, err, memory.ErrClosed, "Stop closes the store")
}

func TestAPIResultCache_Disabled(t *testing.T) {
	r, runs := cacheRoot(t, map[string]any{"services.all.cache.enabled": false})
	base, stop := serveAPI(t, r)
	defer stop()

	for range 2 {
		resp, body := getStats(t, base, "*")
		require.Equal(t, http.StatusOK, resp.StatusCode, body)
		assert.Empty(t, resp.Header.Get("ETag"))
	}
	assert.Equal(t, int64(2), runs.Load())
}

// plainStore is a kv.Store without TTL support.
type plainStore struct{ kv.Store }

func init() {
	kv.RegisterBackendContext("clitest-ttl", func(context.Context, kv.Config) (kv.Store, error) {
		return memory.New(), nil
	})
	kv.RegisterBackendContext("clitest-plain", func(context.Context, kv.Config) (kv.Store, error) {
		return plainStore{memory.New()}, nil
	})
}

// trackedStores records every store the clitest-tracked backend opens.
var trackedStores struct {
	sync.Mutex
	all []*memory.Store
}

func init() {
	kv.RegisterBackendContext("clitest-tracked", func(context.Context, kv.Config) (kv.Store, error) {
		s := memory.New()
		trackedStores.Lock()
		trackedStores.all = append(trackedStores.all, s)
		trackedStores.Unlock()
		return s, nil
	})
}

// A start that fails once the handler is built — the address taken,
// the server timeouts unreadable — closes the result cache's store:
// no Stop follows a failed start to close it.
func TestAPIResultCache_ClosedWhenStartFails(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close()

	cases := map[string]map[string]any{
		"listen fails":         {"services.api.addr": occupied.Addr().String()},
		"server timeouts fail": {"services.api.timeouts.read": "soon"},
	}
	for name, keys := range cases {
		t.Run(name, func(t *testing.T) {
			keys["services.api.cache.backend"] = "clitest-tracked"
			keys["services.api.cache.path"] = filepath.Join(t.TempDir(), "cache")
			r, _ := cacheRoot(t, keys)
			trackedStores.Lock()
			before := len(trackedStores.all)
			trackedStores.Unlock()

			err := apiSvc(t, r).Start(t.Context(), func() { t.Error("ready on a failed start") })
			require.Error(t, err)

			trackedStores.Lock()
			opened := trackedStores.all[before:]
			trackedStores.Unlock()
			require.Len(t, opened, 1, "the start opened the store")
			_, _, err = opened[0].Get(t.Context(), "k")
			assert.ErrorIs(t, err, memory.ErrClosed, "the failed start closed the store")
		})
	}
}

func TestAPIResultCache_RegisteredBackend(t *testing.T) {
	r, runs := cacheRoot(t, map[string]any{
		"services.api.cache.backend": "clitest-ttl",
		"services.api.cache.path":    filepath.Join(t.TempDir(), "cache"),
	})
	base, stop := serveAPI(t, r)
	defer stop()
	for range 2 {
		resp, body := getStats(t, base, "")
		require.Equal(t, http.StatusOK, resp.StatusCode, body)
		assert.NotEmpty(t, resp.Header.Get("ETag"))
	}
	assert.Equal(t, int64(1), runs.Load())
}

func TestAPIResultCache_BackendWithoutTTL(t *testing.T) {
	r, _ := cacheRoot(t, map[string]any{
		"services.api.cache.backend": "clitest-plain",
		"services.api.cache.path":    "x",
	})
	a := apiSvc(t, r)
	require.NoError(t, a.Validate(), "TTL support is only known once the store is open")
	_, _, err := a.openResultCache(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stores without a TTL")
}

func TestAPIResultCacheValidation(t *testing.T) {
	cases := map[string]struct {
		keys map[string]any
		want string
	}{
		"unknown key":                 {map[string]any{"services.api.cache.ttl": "1m"}, "services.api.cache.ttl"},
		"unknown key in services.all": {map[string]any{"services.all.cache.size": 1}, "services.all.cache.size"},
		"enabled not a bool":          {map[string]any{"services.api.cache.enabled": "maybe"}, "services.api.cache.enabled"},
		"max_bytes zero":              {map[string]any{"services.api.cache.max_bytes": 0}, "services.api.cache.max_bytes"},
		"max_bytes not a number":      {map[string]any{"services.api.cache.max_bytes": "lots"}, "services.api.cache.max_bytes"},
		"backend without ttl":         {map[string]any{"services.api.cache.backend": "etcd"}, "without a TTL"},
		"backend unregistered":        {map[string]any{"services.api.cache.backend": "redis"}, "not registered"},
		"backend empty":               {map[string]any{"services.api.cache.backend": " "}, "services.api.cache.backend"},
		"max_bytes off memory": {map[string]any{
			"services.api.cache.backend":   "badger",
			"services.api.cache.max_bytes": 1024,
		}, "memory backend only"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, _ := cacheRoot(t, tc.keys)
			err := apiSvc(t, r).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	t.Run("known keys pass", func(t *testing.T) {
		r, _ := cacheRoot(t, map[string]any{
			"services.all.cache.enabled":   true,
			"services.api.cache.backend":   "memory",
			"services.api.cache.max_bytes": 1 << 20,
		})
		require.NoError(t, apiSvc(t, r).Validate())
	})
	t.Run("disabled skips the backend check", func(t *testing.T) {
		r, _ := cacheRoot(t, map[string]any{
			"services.api.cache.enabled": false,
			"services.api.cache.backend": "redis",
		})
		require.NoError(t, apiSvc(t, r).Validate())
	})
}

func TestValidateRefusesMisplacedCacheTTL(t *testing.T) {
	leaf := func(use, tier, ttl string) *cobra.Command {
		return &cobra.Command{
			Use:  use,
			RunE: func(*cobra.Command, []string) error { return nil },
			Annotations: map[string]string{
				"kit/side-effect": tier,
				"kit/idempotent":  "yes",
				"kit/cache-ttl":   ttl,
			},
		}
	}
	cases := map[string]struct {
		cmd  *cobra.Command
		want string
	}{
		"read with a duration": {leaf("stats", "read", "30s"), ""},
		"malformed":            {leaf("stats", "read", "soon"), `stats="soon"`},
		"zero":                 {leaf("stats", "read", "0s"), `stats="0s"`},
		"on a write":           {leaf("stats", "write", "30s"), "only a kit/side-effect: read command is cached"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := New(Config{Name: "tool", Version: "1.0.0", Short: "tool", DisableValidate: true})
			r.Cmd.AddCommand(tc.cmd)
			err := r.Validate()
			var ve *ValidationError
			if tc.want == "" {
				if errors.As(err, &ve) {
					assert.Empty(t, ve.InvalidCacheTTL)
				}
				return
			}
			require.ErrorAs(t, err, &ve)
			require.Len(t, ve.InvalidCacheTTL, 1)
			assert.Contains(t, ve.InvalidCacheTTL[0], tc.want)
			assert.Contains(t, err.Error(), "with invalid kit/cache-ttl")
		})
	}
}

func TestSetCacheTTL(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	SetCacheTTL(cmd, 90*time.Second)
	assert.Equal(t, "1m30s", cmd.Annotations["kit/cache-ttl"])
}
