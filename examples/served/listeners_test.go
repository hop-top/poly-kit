package main

// The HTTP-plane middleware on every kit HTTP listener: the api
// service's router, the mcp service's HTTP transport and the rpc
// server share one chain, each configured by its own services.<svc>.*
// blocks.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/mcpserve"
	"hop.top/kit/go/console/cli/rpcserve"
	"hop.top/kit/go/console/output"
)

// listenerBase starts service on a loopback port with config and
// returns the listener's base URL, with no path.
func listenerBase(t *testing.T, service string, config map[string]any) string {
	t.Helper()
	var args []string
	switch service {
	case mcpserve.ServiceName:
		args = []string{"mcp", "--mcp-addr", "127.0.0.1:0"}
	case rpcserve.ServiceName:
		args = []string{"rpc", "--rpc-addr", "127.0.0.1:0"}
	default:
		args = []string{"api", "--addr", "127.0.0.1:0"}
	}
	addr := startServe(t, options{config: config}, args...).waitReady(t, service).Address
	addr = strings.TrimSuffix(addr, mcpserve.DefaultPath)
	if !strings.HasPrefix(addr, "http://") {
		addr = "http://" + addr
	}
	return addr
}

func TestEveryHTTPListenerAnswersProbesWithHeadersAndChecksHost(t *testing.T) {
	for _, svc := range []string{"api", mcpserve.ServiceName, rpcserve.ServiceName} {
		t.Run(svc, func(t *testing.T) {
			base := listenerBase(t, svc, nil)

			status, _ := scrapeAs(t, base, "/healthz", "10.1.2.3")
			assert.Equal(t, http.StatusOK, status, "probes answer ahead of the Host check")
			status, _ = scrapeAs(t, base, "/readyz", "127.0.0.1")
			assert.Equal(t, http.StatusOK, status, "the service is ready")

			req, err := http.NewRequest(http.MethodGet, base+"/anything", nil)
			require.NoError(t, err)
			req.Host = "evil.example"
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			_ = resp.Body.Close()
			assert.Equal(t, http.StatusForbidden, resp.StatusCode)
			assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		})
	}
}

func TestMetricsEndpointOnTheMCPAndRPCListeners(t *testing.T) {
	for _, svc := range []string{mcpserve.ServiceName, rpcserve.ServiceName} {
		t.Run(svc, func(t *testing.T) {
			base := listenerBase(t, svc, map[string]any{
				"services." + svc + ".metrics.enabled":        true,
				"services." + svc + ".metrics.exporter":       "none",
				"services." + svc + ".metrics.scrape.enabled": true,
			})
			// One request through the listener, so slot 5 has recorded it.
			_, _ = scrapeAs(t, base, "/healthz", "127.0.0.1")

			status, body := scrapeAs(t, base, "/metrics", "127.0.0.1")
			require.Equal(t, http.StatusOK, status, "a local scraper needs no credentials")
			assert.Contains(t, body, "# TYPE kit_serve_http_requests_active gauge")
			assert.Contains(t, body, `kit_service="`+svc+`"`)

			status, body = scrapeAs(t, base, "/metrics", "evil.example")
			assert.Equal(t, http.StatusForbidden, status)
			assert.NotContains(t, body, "kit_serve_http_requests_active")
		})
	}
}

func TestMetricsEndpointBeyondLoopbackNeedsItsOptInOnEveryListener(t *testing.T) {
	for _, tc := range []struct{ svc, flag string }{
		{mcpserve.ServiceName, "--mcp-addr"},
		{rpcserve.ServiceName, "--rpc-addr"},
	} {
		t.Run(tc.svc, func(t *testing.T) {
			root := newRoot(options{config: map[string]any{
				"services." + tc.svc + ".metrics.enabled":             true,
				"services." + tc.svc + ".metrics.exporter":            "none",
				"services." + tc.svc + ".metrics.scrape.enabled":      true,
				"services." + tc.svc + ".insecure_remote":             true,
				"services." + tc.svc + ".insecure_no_policy":          true,
				"services." + tc.svc + ".metrics.scrape.allow_remote": false,
			}})
			err := runToCompletion(t, root, []string{"serve", tc.svc, tc.flag, "0.0.0.0:0"}, 5*time.Second)
			require.Error(t, err)
			var kitErr *output.Error
			require.ErrorAs(t, err, &kitErr)
			assert.Equal(t, 2, kitErr.ExitCode)
			assert.Contains(t, kitErr.Message, "services."+tc.svc+".metrics.scrape.allow_remote: true")
		})
	}
}

func TestSocketRefusesHTTPOnlyBlocks(t *testing.T) {
	root := newRoot(options{config: map[string]any{
		"services.socket.body_limit.max_bytes": 1024,
		"services.socket.path":                 shortSocketPath(t),
	}})
	err := runToCompletion(t, root, []string{"serve", "socket"}, 5*time.Second)
	require.Error(t, err)
	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, 2, kitErr.ExitCode)
	assert.Contains(t, kitErr.Message, "services.socket.body_limit")
	assert.Contains(t, kitErr.Message, "no HTTP listener")
}
