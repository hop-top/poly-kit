package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// okHandler answers 200 so a refusal is the only way to see a non-200.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// serveWithHost runs one request with the given Host header through h.
func serveWithHost(h http.Handler, method, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v1/commands", nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// refusalCode decodes the stable code from a refusal body.
func refusalCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body api.APIError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	assert.Equal(t, rec.Code, body.Status)
	return body.Code
}

// loopbackCheck is the check a listener on 127.0.0.1 derives.
func loopbackCheck(t *testing.T) http.Handler {
	t.Helper()
	hosts, wildcard := api.ListenerHosts("127.0.0.1:8080")
	require.False(t, wildcard)
	return api.HostCheck(api.HostCheckConfig{Allow: hosts})(okHandler)
}

func TestHostCheck_RefusesRebindingHost(t *testing.T) {
	// DNS rebinding: the browser resolved attacker.example to the
	// server's address, so the request arrives with the attacker's
	// name in Host. Nothing about the connection itself is wrong.
	h := loopbackCheck(t)

	for _, host := range []string{
		"attacker.example",
		"attacker.example:8080",
		"localhost.attacker.example:8080",
		"127.0.0.1.nip.io:8080",
		"192.168.1.20:8080",
	} {
		t.Run(host, func(t *testing.T) {
			rec := serveWithHost(h, http.MethodGet, host)
			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.Equal(t, api.CodeHostRejected, refusalCode(t, rec))
		})
	}
}

func TestHostCheck_AcceptsLoopbackForms(t *testing.T) {
	h := loopbackCheck(t)

	for _, host := range []string{
		"localhost",
		"localhost:8080",
		"LocalHost:8080",
		"localhost.:8080",
		"127.0.0.1",
		"127.0.0.1:8080",
		"[::1]",
		"[::1]:8080",
		"[0:0:0:0:0:0:0:1]:8080",
		// A forwarded port (ssh -L 9000:127.0.0.1:8080) arrives
		// with the forwarded port in Host.
		"localhost:9000",
		// HTTP/1.0 without Host is not a browser.
		"",
	} {
		t.Run(host, func(t *testing.T) {
			rec := serveWithHost(h, http.MethodGet, host)
			assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	}
}

func TestHostCheck_Allow(t *testing.T) {
	h := api.HostCheck(api.HostCheckConfig{
		Allow: []string{"api.example.com", "Tool.Internal:8443", "10.0.0.5"},
	})(okHandler)

	cases := []struct {
		host string
		want int
	}{
		{"api.example.com", http.StatusOK},
		{"api.example.com:443", http.StatusOK},
		{"API.EXAMPLE.COM.", http.StatusOK},
		{"tool.internal:8443", http.StatusOK},
		{"10.0.0.5:8080", http.StatusOK},
		{"tool.internal:8080", http.StatusForbidden}, // entry pins the port
		{"tool.internal", http.StatusForbidden},
		{"evil.api.example.com", http.StatusForbidden},
		{"localhost:8080", http.StatusForbidden}, // not derived here
	}
	for _, c := range cases {
		t.Run(c.host, func(t *testing.T) {
			assert.Equal(t, c.want, serveWithHost(h, http.MethodGet, c.host).Code)
		})
	}
}

func TestHostCheck_WildcardEntry(t *testing.T) {
	h := api.HostCheck(api.HostCheckConfig{Allow: []string{"*"}})(okHandler)
	assert.Equal(t, http.StatusOK, serveWithHost(h, http.MethodGet, "attacker.example").Code)
}

func TestHostCheck_CustomRefusal(t *testing.T) {
	h := api.HostCheck(api.HostCheckConfig{
		Refuse: func(w http.ResponseWriter, _ *http.Request, e *api.APIError) {
			w.Header().Set("X-Refusal", e.Code)
			w.WriteHeader(e.Status)
		},
	})(okHandler)
	rec := serveWithHost(h, http.MethodGet, "attacker.example")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, api.CodeHostRejected, rec.Header().Get("X-Refusal"))
}

func TestListenerHosts(t *testing.T) {
	cases := []struct {
		addr     string
		hosts    []string
		wildcard bool
	}{
		{"127.0.0.1:8080", []string{"localhost", "127.0.0.1", "::1"}, false},
		{"127.0.0.2:8080", []string{"localhost", "127.0.0.1", "::1", "127.0.0.2"}, false},
		{"[::1]:8080", []string{"localhost", "127.0.0.1", "::1"}, false},
		{"localhost:8080", []string{"localhost", "127.0.0.1", "::1"}, false},
		{"tool.internal:8080", []string{"tool.internal"}, false},
		{"192.168.1.20:8080", []string{"192.168.1.20"}, false},
		{":8080", nil, true},
		{"0.0.0.0:8080", nil, true},
		{"[::]:8080", nil, true},
	}
	for _, c := range cases {
		t.Run(c.addr, func(t *testing.T) {
			hosts, wildcard := api.ListenerHosts(c.addr)
			assert.Equal(t, c.hosts, hosts)
			assert.Equal(t, c.wildcard, wildcard)
		})
	}
}
