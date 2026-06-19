package httpcache_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/httpcache"
	"hop.top/kit/go/storage/kv"
)

// newStore opens a sqlite-backed TTLStore in a temp dir, registered for
// cleanup. sqlite is used over badger to keep the test dependency-light.
func newStore(t *testing.T) kv.TTLStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cache.db")
	s, err := kv.Open(kv.Config{Backend: "sqlite", Path: path})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	ttl, ok := s.(kv.TTLStore)
	require.True(t, ok, "sqlite store must implement TTLStore")
	return ttl
}

// countingRT records how many times it was invoked, so tests can assert
// cache hits avoid the inner transport.
type countingRT struct {
	calls atomic.Int64
	rt    http.RoundTripper
}

func (c *countingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return c.rt.RoundTrip(req)
}

func TestRoundTrip_CachesGET(t *testing.T) {
	var origin atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin.Add(1)
		_, _ = io.WriteString(w, "hello")
	}))
	t.Cleanup(srv.Close)

	counter := &countingRT{rt: http.DefaultTransport}
	client := &http.Client{Transport: httpcache.New(newStore(t), counter)}

	// First call: miss, hits origin.
	body1 := get(t, client, srv.URL)
	assert.Equal(t, "hello", body1)
	assert.Equal(t, int64(1), origin.Load())
	assert.Equal(t, int64(1), counter.calls.Load())

	// Second call: hit, origin untouched and inner transport not called.
	body2 := get(t, client, srv.URL)
	assert.Equal(t, "hello", body2)
	assert.Equal(t, int64(1), origin.Load(), "cache hit must not reach origin")
	assert.Equal(t, int64(1), counter.calls.Load(), "cache hit must not reach inner transport")
}

func TestRoundTrip_DoesNotCachePOST(t *testing.T) {
	var origin atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}

	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodPost, srv.URL, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}
	assert.Equal(t, int64(2), origin.Load(), "POST must never be served from cache")
}

func TestRoundTrip_DoesNotCacheNon2xx(t *testing.T) {
	var origin atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin.Add(1)
		http.Error(w, "nope", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}
	get(t, client, srv.URL)
	get(t, client, srv.URL)
	assert.Equal(t, int64(2), origin.Load(), "404 must not be cached")
}

func TestRoundTrip_RespectsNoStore(t *testing.T) {
	var origin atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin.Add(1)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, "secret")
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}
	get(t, client, srv.URL)
	get(t, client, srv.URL)
	assert.Equal(t, int64(2), origin.Load(), "no-store response must not be cached")
}

func TestRoundTrip_TTLExpiry(t *testing.T) {
	var origin atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin.Add(1)
		_, _ = io.WriteString(w, "v")
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport,
		httpcache.WithTTL(50*time.Millisecond))}

	get(t, client, srv.URL)
	assert.Equal(t, int64(1), origin.Load())
	time.Sleep(80 * time.Millisecond)
	get(t, client, srv.URL)
	assert.Equal(t, int64(2), origin.Load(), "expired entry must refetch")
}

func TestRoundTrip_PreservesHeadersAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "abc")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "body")
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}
	get(t, client, srv.URL) // prime

	resp, err := client.Get(srv.URL) // served from cache
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "abc", resp.Header.Get("X-Custom"))
	assert.Equal(t, "body", string(body))
}

// TestContract_Cacheability is the cross-language parity gate: it drives
// the same vectors the TS and Python ports consume. Because the gate
// helpers are unexported, this exercises them end-to-end through a stub
// transport rather than calling them directly.
func TestContract_Cacheability(t *testing.T) {
	raw, err := os.ReadFile(contractPath(t, "cacheability.json"))
	require.NoError(t, err)

	var vectors struct {
		RequestCacheable []struct {
			Name    string            `json:"name"`
			Method  string            `json:"method"`
			Headers map[string]string `json:"headers"`
			Want    bool              `json:"want"`
		} `json:"request_cacheable"`
		ResponseCacheable []struct {
			Name    string            `json:"name"`
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
			Want    bool              `json:"want"`
		} `json:"response_cacheable"`
	}
	require.NoError(t, json.Unmarshal(raw, &vectors))
	require.NotEmpty(t, vectors.RequestCacheable)
	require.NotEmpty(t, vectors.ResponseCacheable)

	for _, v := range vectors.RequestCacheable {
		t.Run("req/"+v.Name, func(t *testing.T) {
			var origin atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				origin.Add(1)
				_, _ = io.WriteString(w, "x")
			}))
			t.Cleanup(srv.Close)
			client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}

			do := func() {
				req, err := http.NewRequest(v.Method, srv.URL, nil)
				require.NoError(t, err)
				for k, val := range v.Headers {
					req.Header.Set(k, val)
				}
				resp, err := client.Do(req)
				require.NoError(t, err)
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
			do()
			do()
			// want=true (cacheable) → second call served from cache → origin hit once.
			if v.Want {
				assert.Equal(t, int64(1), origin.Load(), "cacheable: 2nd call should be a hit")
			} else {
				assert.Equal(t, int64(2), origin.Load(), "non-cacheable: both calls reach origin")
			}
		})
	}

	for _, v := range vectors.ResponseCacheable {
		t.Run("resp/"+v.Name, func(t *testing.T) {
			var origin atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				origin.Add(1)
				for k, val := range v.Headers {
					w.Header().Set(k, val)
				}
				w.WriteHeader(v.Status)
				_, _ = io.WriteString(w, "x")
			}))
			t.Cleanup(srv.Close)
			client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}
			get(t, client, srv.URL)
			get(t, client, srv.URL)
			if v.Want {
				assert.Equal(t, int64(1), origin.Load(), "cacheable response: 2nd call should be a hit")
			} else {
				assert.Equal(t, int64(2), origin.Load(), "non-cacheable response: both calls reach origin")
			}
		})
	}
}

func TestRoundTrip_ZeroTTLCachesWithoutExpiry(t *testing.T) {
	var origin atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin.Add(1)
		_, _ = io.WriteString(w, "v")
	}))
	t.Cleanup(srv.Close)

	// WithTTL(0) must store with no expiry, NOT stamp an already-past
	// expiry (which would make every entry a perpetual miss).
	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport,
		httpcache.WithTTL(0))}

	get(t, client, srv.URL)
	get(t, client, srv.URL)
	assert.Equal(t, int64(1), origin.Load(), "WithTTL(0) must cache (no expiry), not expire immediately")
}

func TestRoundTrip_StripsFramingHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Force chunked by writing without a Content-Length and flushing.
		w.Header().Set("X-Keep", "yes")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "chunked-body")
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: httpcache.New(newStore(t), http.DefaultTransport)}
	get(t, client, srv.URL) // prime

	resp, err := client.Get(srv.URL) // from cache
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, "chunked-body", string(body))
	assert.Equal(t, "yes", resp.Header.Get("X-Keep"), "non-framing headers preserved")
	// Framing headers must not survive into the reconstructed response:
	// the body length is authoritative via resp.ContentLength.
	assert.Empty(t, resp.Header.Get("Transfer-Encoding"), "Transfer-Encoding must be stripped")
	assert.Empty(t, resp.Header.Get("Content-Length"), "Content-Length header must be stripped (ContentLength field is authoritative)")
	assert.Equal(t, int64(len("chunked-body")), resp.ContentLength)
}

func get(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	resp, err := c.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

// contractPath resolves contracts/httpcache-v1/<name> from the package
// dir (go/storage/httpcache → repo root is three levels up).
func contractPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "contracts", "httpcache-v1", name)
}

var _ = context.Background
