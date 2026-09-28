package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// asClient reads once with X-Forwarded-For naming client and returns
// the status and the Strict-Transport-Security header.
func asClient(t *testing.T, apiURL, client, proto string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, apiURL+"/v1/commands/item/list", nil)
	require.NoError(t, err)
	req.Header.Set("X-Forwarded-For", client)
	if proto != "" {
		req.Header.Set("X-Forwarded-Proto", proto)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Strict-Transport-Security")
}

// services.<svc>.trusted_proxies: behind a listed proxy each forwarded
// client has its own rate-limit budget and HSTS follows the forwarded
// scheme; without the list the header is ignored.
func TestBinaryTrustedProxies(t *testing.T) {
	limit := []string{
		"SERVED_SERVICES_API_RATE_LIMIT_ENABLED=true",
		"SERVED_SERVICES_API_RATE_LIMIT_READ_PER_MINUTE=1",
		"SERVED_SERVICES_API_RATE_LIMIT_READ_BURST=1",
	}
	t.Run("trusted: one budget per client", func(t *testing.T) {
		env := append([]string{"SERVED_SERVICES_ALL_TRUSTED_PROXIES=127.0.0.1 ::1"}, limit...)
		r := runBinary(t, newHome(t, ""), env, "serve", "api", "--addr", "127.0.0.1:0")
		require.True(t, r.ready, r.output())
		status, hsts := asClient(t, r.apiURL, "198.51.100.1", "https")
		assert.Equal(t, http.StatusOK, status)
		assert.NotEmpty(t, hsts, "the proxy says the client used https")
		status, _ = asClient(t, r.apiURL, "198.51.100.1", "")
		assert.Equal(t, http.StatusTooManyRequests, status)
		status, hsts = asClient(t, r.apiURL, "198.51.100.2", "")
		assert.Equal(t, http.StatusOK, status)
		assert.Empty(t, hsts)
	})
	t.Run("untrusted: the header buys nothing", func(t *testing.T) {
		r := runBinary(t, newHome(t, ""), limit, "serve", "api", "--addr", "127.0.0.1:0")
		require.True(t, r.ready, r.output())
		status, hsts := asClient(t, r.apiURL, "198.51.100.1", "https")
		assert.Equal(t, http.StatusOK, status)
		assert.Empty(t, hsts, "a forwarded scheme from an untrusted peer")
		status, _ = asClient(t, r.apiURL, "198.51.100.2", "")
		assert.Equal(t, http.StatusTooManyRequests, status)
	})
	t.Run("bad entry: exit 2", func(t *testing.T) {
		home := newHome(t, "services:\n  api:\n    trusted_proxies: [10.0.0.0/8, proxy.internal]\n")
		r := runBinary(t, home, nil, "serve", "api", "--addr", "127.0.0.1:0")
		require.False(t, r.ready, r.output())
		assert.Equal(t, 2, r.exitCode(), r.output())
		assert.Contains(t, r.output(), `services.api.trusted_proxies: trusted proxy "proxy.internal"`)
	})
}
