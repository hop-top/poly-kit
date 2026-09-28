package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
)

// proxiedAPI serves the api service on loopback with a read limit of
// one call a minute, and set applied on top.
func proxiedAPI(t *testing.T, set map[string]any) (url string, rec *auditRecorder, stop func()) {
	t.Helper()
	isolateHome(t)
	rec = &auditRecorder{}
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), WithAuditSinks(rec.spec()))
	r.Viper.Set("services.api.rate_limit.enabled", true)
	r.Viper.Set("services.api.rate_limit.read.per_minute", 1)
	r.Viper.Set("services.api.rate_limit.read.burst", 1)
	for k, v := range set {
		r.Viper.Set(k, v)
	}
	base, stop := serveAPI(t, r)
	return base + "/v1/commands/list", rec, stop
}

// Behind a trusted proxy each client spends its own budget, and the
// audit record names the client and the proxy it came through.
func TestAPIServiceTrustedProxyKeysTheClient(t *testing.T) {
	url, rec, stop := proxiedAPI(t, map[string]any{
		"services.api.trusted_proxies": []string{"127.0.0.1", "::1"},
	})
	defer stop()
	as := func(client string) int {
		resp, _ := get(t, url, map[string]string{"X-Forwarded-For": client})
		return resp.StatusCode
	}

	assert.Equal(t, http.StatusOK, as("198.51.100.1"))
	inv, _, err := rec.last(t)
	require.NoError(t, err)
	assert.Equal(t, "198.51.100.1", inv.Meta.Extra["remote_addr"])
	assert.True(t, strings.HasPrefix(inv.Meta.Extra["peer_addr"], "127.0.0.1:"), inv.Meta.Extra["peer_addr"])

	assert.Equal(t, http.StatusTooManyRequests, as("198.51.100.1"), "the same client is limited")
	_, _, err = rec.last(t)
	assert.ErrorIs(t, err, cmdsurface.ErrRateLimited)

	assert.Equal(t, http.StatusOK, as("198.51.100.2"), "another client behind the proxy has its own budget")
	assert.Equal(t, http.StatusOK, as("2001:db8:1::1"))
	assert.Equal(t, http.StatusTooManyRequests, as("2001:db8:1::2"), "an IPv6 client is keyed by its /64")
}

// Without trusted_proxies the header is the caller's say-so: every
// caller on this connection is the loopback peer, whatever it sends.
func TestAPIServiceIgnoresForwardedForByDefault(t *testing.T) {
	url, rec, stop := proxiedAPI(t, nil)
	defer stop()

	resp, _ := get(t, url, map[string]string{"X-Forwarded-For": "198.51.100.1"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	inv, _, err := rec.last(t)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(inv.Meta.Extra["remote_addr"], "127.0.0.1:"), inv.Meta.Extra["remote_addr"])
	assert.NotContains(t, inv.Meta.Extra, "peer_addr")

	resp, _ = get(t, url, map[string]string{"X-Forwarded-For": "198.51.100.2", "Forwarded": "for=198.51.100.3"})
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode, "a spoofed header buys no fresh budget")
}

// The list comes from services.all as well, as one string the way the
// environment carries it, and the service's own list replaces it.
func TestAPIServiceTrustedProxiesResolution(t *testing.T) {
	url, rec, stop := proxiedAPI(t, map[string]any{
		"services.all.trusted_proxies": "10.0.0.0/8, 127.0.0.1 ::1",
	})
	resp, _ := get(t, url, map[string]string{"X-Forwarded-For": "198.51.100.1"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	inv, _, _ := rec.last(t)
	assert.Equal(t, "198.51.100.1", inv.Meta.Extra["remote_addr"])
	stop()

	url, rec, stop = proxiedAPI(t, map[string]any{
		"services.all.trusted_proxies": []string{"127.0.0.1", "::1"},
		"services.api.trusted_proxies": []string{"10.0.0.0/8"},
	})
	defer stop()
	resp, _ = get(t, url, map[string]string{"X-Forwarded-For": "198.51.100.1"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	inv, _, _ = rec.last(t)
	assert.True(t, strings.HasPrefix(inv.Meta.Extra["remote_addr"], "127.0.0.1:"),
		"the service's list replaces services.all: %s", inv.Meta.Extra["remote_addr"])
}

// HSTS follows the scheme the client used, which a trusted proxy
// reports and nobody else can.
func TestAPIServiceHSTSBehindATrustedProxy(t *testing.T) {
	url, _, stop := proxiedAPI(t, map[string]any{
		"services.api.trusted_proxies":    []string{"127.0.0.1", "::1"},
		"services.api.rate_limit.enabled": false,
	})
	defer stop()
	resp, _ := get(t, url, map[string]string{"X-Forwarded-Proto": "https"})
	assert.NotEmpty(t, resp.Header.Get("Strict-Transport-Security"))
	resp, _ = get(t, url, map[string]string{"X-Forwarded-Proto": "http"})
	assert.Empty(t, resp.Header.Get("Strict-Transport-Security"))

	url2, _, stop2 := proxiedAPI(t, map[string]any{"services.api.rate_limit.enabled": false})
	defer stop2()
	resp, _ = get(t, url2, map[string]string{"X-Forwarded-Proto": "https"})
	assert.Empty(t, resp.Header.Get("Strict-Transport-Security"), "an untrusted peer's forwarded proto")
}

func TestAPIServiceRefusesBadTrustedProxies(t *testing.T) {
	cases := map[string]struct {
		key  string
		val  any
		want string
	}{
		"not a CIDR":  {"services.api.trusted_proxies", []string{"10.0.0.0/8", "proxy.internal"}, `services.api.trusted_proxies: trusted proxy "proxy.internal": not a CIDR or an IP address`},
		"bad mask":    {"services.all.trusted_proxies", "10.0.0.0/33", `services.all.trusted_proxies: trusted proxy "10.0.0.0/33"`},
		"a block":     {"services.api.trusted_proxies.cidrs", []string{"10.0.0.0/8"}, "services.api.trusted_proxies: must be a list of strings"},
		"not strings": {"services.api.trusted_proxies", []any{"10.0.0.0/8", 7}, "services.api.trusted_proxies[1]: want a string"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			isolateHome(t)
			r := authRoot(t, WithAPI(APIConfig{}))
			r.Viper.Set(c.key, c.val)
			oe := usageErr(t, runServeExpect(t, r, []string{"serve", "api"}, 2*time.Second))
			assert.Contains(t, oe.Message, c.want)
		})
	}
}
