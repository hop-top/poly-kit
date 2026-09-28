package main

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/observability"
)

// scrapeOnly is the configuration of a tool whose metrics are read
// from its scrape endpoint alone: recorded, pushed nowhere.
var scrapeOnly = map[string]any{
	"services.api.metrics.enabled":        true,
	"services.api.metrics.exporter":       "none",
	"services.api.metrics.scrape.enabled": true,
}

// scrapeAs GETs base+path with the Host header set to host and no
// credentials, the way a scraper does.
func scrapeAs(t *testing.T, base, path, host string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	require.NoError(t, err)
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

func TestMetricsEndpointServesTheServeInstruments(t *testing.T) {
	run := startServe(t, options{config: scrapeOnly}, "api", "--addr", "127.0.0.1:0")
	base := "http://" + run.waitReady(t, "api").Address

	status, _ := httpDo(t, http.MethodGet, base+"/v1/commands/item/list", "")
	require.Equal(t, http.StatusOK, status)

	status, body := scrapeAs(t, base, "/metrics", "127.0.0.1")
	require.Equal(t, http.StatusOK, status, "a local scraper needs no credentials")
	for _, want := range []string{
		"# TYPE kit_serve_requests_total counter",
		`kit_command="item list"`,
		`kit_outcome="ok"`,
		"# TYPE kit_serve_request_duration_seconds histogram",
		"# TYPE kit_serve_http_requests_active gauge",
		`service_name="served"`,
	} {
		assert.Contains(t, body, want)
	}

	// A DNS-rebinding page reaches the loopback listener under its own
	// name; the Host check refuses it before the exposition is written.
	status, body = scrapeAs(t, base, "/metrics", "evil.example")
	assert.Equal(t, http.StatusForbidden, status)
	assert.Contains(t, body, "host_rejected")
	assert.NotContains(t, body, "kit_serve_requests_total")
}

func TestMetricsEndpointIsOffByDefault(t *testing.T) {
	run := startServe(t, options{observe: observability.NewServe(observability.WithOutput(io.Discard)),
		config: map[string]any{"services.api.metrics.enabled": true, "services.api.metrics.exporter": "stdout"}}, "api", "--addr", "127.0.0.1:0")
	base := "http://" + run.waitReady(t, "api").Address
	status, _ := scrapeAs(t, base, "/metrics", "127.0.0.1")
	assert.Equal(t, http.StatusNotFound, status, "metrics on, endpoint not asked for")
}

func TestMetricsEndpointBeyondLoopbackNeedsItsOwnOptIn(t *testing.T) {
	root := newRoot(options{config: scrapeOnly})
	err := runToCompletion(t, root, []string{"serve", "api", "--addr", "0.0.0.0:0",
		"--insecure-remote", "--insecure-no-policy"}, 5*time.Second)
	require.Error(t, err)
	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, 2, kitErr.ExitCode, "refused at the configuration gate")
	assert.Contains(t, kitErr.Message, "services.api.metrics.scrape.allow_remote: true")

	config := map[string]any{"services.api.metrics.scrape.allow_remote": true}
	for k, v := range scrapeOnly {
		config[k] = v
	}
	run := startServe(t, options{config: config}, "api", "--addr", "0.0.0.0:0",
		"--insecure-remote", "--insecure-no-policy")
	_ = run.waitReady(t, "api")
}
