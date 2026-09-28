package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// guardRoot isolates config lookups and builds a loopback api root.
func guardRoot(t *testing.T, set map[string]any) *Root {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	t.Setenv("XDG_DATA_HOME", home+"/.local/share")
	t.Setenv("XDG_STATE_HOME", home+"/.local/state")
	t.Setenv("XDG_CACHE_HOME", home+"/.cache")

	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	for k, v := range set {
		r.Viper.Set(k, v)
	}
	return r
}

// guardDo sends one request with an explicit Host and optional Origin.
func guardDo(t *testing.T, method, url, host, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	require.NoError(t, err)
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func guardCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var e api.APIError
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
	return e.Code
}

func TestAPIGuards_Defaults(t *testing.T) {
	r := guardRoot(t, nil)
	base, stop := serveAPI(t, r)
	defer stop()
	port := base[strings.LastIndex(base, ":")+1:]

	// DNS rebinding: the attacker's name in Host.
	resp := guardDo(t, http.MethodGet, base+"/v1/commands", "attacker.example:"+port, "")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, api.CodeHostRejected, guardCode(t, resp))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"),
		"a refusal carries the security headers too")

	// Routes registered outside the router's middleware list (huma's,
	// /capabilities, unmatched paths) are behind the check too.
	resp = guardDo(t, http.MethodGet, base+"/no-such-route", "attacker.example:"+port, "")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Every loopback spelling of the bound address.
	for _, host := range []string{"", "localhost:" + port, "127.0.0.1:" + port, "[::1]:" + port} {
		resp = guardDo(t, http.MethodGet, base+"/v1/commands", host, "")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "Host %q", host)
		assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
		assert.Equal(t, api.DefaultContentSecurityPolicy, resp.Header.Get("Content-Security-Policy"))
		assert.Empty(t, resp.Header.Get("Strict-Transport-Security"))
	}

	// A page on another origin cannot run a write.
	resp = guardDo(t, http.MethodPost, base+"/v1/commands/add", "", "https://attacker.example")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, api.CodeOriginRejected, guardCode(t, resp))

	// Nor can another local origin (a dev server on another port).
	resp = guardDo(t, http.MethodPost, base+"/v1/commands/add", "localhost:"+port, "http://localhost:3000")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Non-browser and same-origin writes pass.
	resp = guardDo(t, http.MethodPost, base+"/v1/commands/add", "", "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp = guardDo(t, http.MethodPost, base+"/v1/commands/add", "localhost:"+port, "http://localhost:"+port)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// The Host and Origin checks wrap the router as a whole, so a
// command's streaming twin is behind them exactly as its
// request/reply route is: a refusal is a status before any stream
// opens.
func TestAPIGuards_CoverStreamRoutes(t *testing.T) {
	r := guardRoot(t, nil)
	base, stop := serveAPI(t, r)
	defer stop()
	port := base[strings.LastIndex(base, ":")+1:]

	resp := guardDo(t, http.MethodPost, base+"/v1/commands/add/stream", "attacker.example:"+port, "")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, api.CodeHostRejected, guardCode(t, resp))

	resp = guardDo(t, http.MethodPost, base+"/v1/commands/add/stream", "", "https://attacker.example")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, api.CodeOriginRejected, guardCode(t, resp))

	resp = guardDo(t, http.MethodPost, base+"/v1/commands/add/stream", "localhost:"+port, "http://localhost:"+port)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream"), resp.Header.Get("Content-Type"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
}

// guardHandler returns the api service's slot 8 and slot 6 wrapping
// around a 200 handler, for addresses a test cannot bind.
func guardHandler(t *testing.T, r *Root, addr string) http.Handler {
	t.Helper()
	a := apiSvc(t, r)
	a.cfg.Addr = addr
	require.NoError(t, a.validateGuards())
	h, err := a.hostOriginChecks(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	require.NoError(t, err)
	return a.securityHeaders(h)
}

func guardServe(h http.Handler, method, host, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v1/commands/add", nil)
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAPIGuards_ConfigKeys(t *testing.T) {
	r := guardRoot(t, map[string]any{
		"services.api.host_check.allow":         []string{"tool.test"},
		"services.api.origin_check.allow":       []string{"https://app.example.com"},
		"services.api.security_headers.enabled": false,
	})
	base, stop := serveAPI(t, r)
	defer stop()

	resp := guardDo(t, http.MethodGet, base+"/v1/commands", "tool.test", "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("X-Content-Type-Options"), "security headers disabled")

	// allow adds to the listener's own hosts; it does not replace them.
	resp = guardDo(t, http.MethodGet, base+"/v1/commands", "localhost", "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp = guardDo(t, http.MethodGet, base+"/v1/commands", "other.test", "")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp = guardDo(t, http.MethodPost, base+"/v1/commands/add", "tool.test", "https://app.example.com")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAPIGuards_ServicesAllDefaults(t *testing.T) {
	// services.all supplies a block's keys to every service; the
	// service's own key wins per key, and a list replaces.
	r := guardRoot(t, map[string]any{
		"services.all.host_check.allow":         []string{"shared.test"},
		"services.all.security_headers.enabled": false,
	})
	h := guardHandler(t, r, "127.0.0.1:8080")
	assert.Equal(t, http.StatusOK, guardServe(h, http.MethodGet, "shared.test", "").Code)
	assert.Empty(t, guardServe(h, http.MethodGet, "localhost", "").Header().Get("X-Content-Type-Options"))

	r = guardRoot(t, map[string]any{
		"services.all.host_check.allow": []string{"shared.test"},
		"services.api.host_check.allow": []string{"own.test"},
	})
	h = guardHandler(t, r, "127.0.0.1:8080")
	assert.Equal(t, http.StatusForbidden, guardServe(h, http.MethodGet, "shared.test", "").Code)
	assert.Equal(t, http.StatusOK, guardServe(h, http.MethodGet, "own.test", "").Code)
}

func TestAPIGuards_EnabledKeys(t *testing.T) {
	r := guardRoot(t, map[string]any{
		"services.api.host_check.enabled":   false,
		"services.api.origin_check.enabled": false,
	})
	h := guardHandler(t, r, "127.0.0.1:8080")
	rec := guardServe(h, http.MethodPost, "attacker.example", "https://attacker.example")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

func TestAPIGuards_BindDerivesHosts(t *testing.T) {
	r := guardRoot(t, nil)

	// A named bind answers to its name, and only to it.
	h := guardHandler(t, r, "tool.internal:8080")
	assert.Equal(t, http.StatusOK, guardServe(h, http.MethodGet, "tool.internal:8080", "").Code)
	assert.Equal(t, http.StatusForbidden, guardServe(h, http.MethodGet, "localhost:8080", "").Code)

	// A wildcard bind derives no Host restriction; Origin still holds.
	for _, addr := range []string{":8080", "0.0.0.0:8080", "[::]:8080"} {
		h = guardHandler(t, r, addr)
		assert.Equal(t, http.StatusOK, guardServe(h, http.MethodGet, "anything.example", "").Code, addr)
		assert.Equal(t, http.StatusForbidden,
			guardServe(h, http.MethodPost, "anything.example", "https://attacker.example").Code, addr)
	}

	// Unless the operator lists names.
	r = guardRoot(t, map[string]any{"services.api.host_check.allow": []string{"api.example.com"}})
	h = guardHandler(t, r, "0.0.0.0:8080")
	assert.Equal(t, http.StatusOK, guardServe(h, http.MethodGet, "api.example.com", "").Code)
	assert.Equal(t, http.StatusForbidden, guardServe(h, http.MethodGet, "anything.example", "").Code)
}

func TestAPIGuards_ConfigErrors(t *testing.T) {
	cases := map[string]struct {
		set  map[string]any
		want string
	}{
		"malformed origin": {
			map[string]any{"services.api.origin_check.allow": []string{"app.example.com"}},
			"services.api.origin_check.allow",
		},
		"unknown key": {
			map[string]any{"services.api.host_check.allowed_hosts": []string{"x"}},
			"unknown key \"allowed_hosts\"",
		},
		"unknown key under all": {
			map[string]any{"services.all.security_headers.csp": "x"},
			"services.all.security_headers.csp",
		},
		"scalar block": {
			map[string]any{"services.api.origin_check": true},
			"must be a block",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := apiSvc(t, guardRoot(t, c.set)).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

// loopbackRequest is httptest.NewRequest addressed the way a local
// client addresses the api service. httptest's default Host,
// example.com, is one the Host check correctly refuses on a loopback
// bind, so tests of the middleware behind it must not use it.
func loopbackRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Host = "127.0.0.1"
	return req
}
