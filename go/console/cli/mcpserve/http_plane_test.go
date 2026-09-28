package mcpserve_test

// The HTTP-plane middleware on the mcp service's listener: the chain
// every kit HTTP listener shares, configured by services.mcp.*, with
// refusals written as the MCP transport expects them.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/mcpserve"
)

// withConfig sets configuration keys on the root, as a config file
// would.
func withConfig(kv map[string]any) func(*cli.Root) {
	return func(r *cli.Root) {
		for k, v := range kv {
			r.Viper.Set(k, v)
		}
	}
}

// planeReply is one raw HTTP exchange.
type planeReply struct {
	status int
	header http.Header
	body   string
}

// rpcError is the JSON-RPC error body of an HTTP-plane refusal.
func (r planeReply) rpcError(t *testing.T) (code int, refusal string) {
	t.Helper()
	var body struct {
		ID    any `json:"id"`
		Error struct {
			Code int `json:"code"`
			Data struct {
				Code string `json:"code"`
			} `json:"data"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.body), &body), r.body)
	assert.Nil(t, body.ID, "the request was never parsed")
	return body.Error.Code, body.Error.Data.Code
}

// rawDo sends one request with Host set to host (the URL's own when
// empty) and returns the reply undecoded: the transport neither adds
// Accept-Encoding nor decompresses.
func rawDo(t *testing.T, method, url, host string, hdr map[string]string, body io.Reader) planeReply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	require.NoError(t, err)
	if host != "" {
		req.Host = host
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	c := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return planeReply{status: resp.StatusCode, header: resp.Header, body: string(raw)}
}

// discover is a 2026-07-28 server/discover request, padded with pad
// bytes of _meta so its size can be chosen.
func discover(pad int) (hdr map[string]string, body string) {
	hdr = map[string]string{
		"Content-Type":         "application/json",
		"Accept":               "application/json, text/event-stream",
		"MCP-Protocol-Version": "2026-07-28",
		"Mcp-Method":           "server/discover",
	}
	meta := modernMeta(`{}`)
	if pad > 0 {
		meta = strings.Replace(meta, `"_meta":{`, `"_meta":{"pad":"`+strings.Repeat("x", pad)+`",`, 1)
	}
	return hdr, `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + meta + `}}`
}

func TestMCPServiceHTTPPlane(t *testing.T) {
	_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		withConfig(map[string]any{
			"services.mcp.body_limit.max_bytes":  2048,
			"services.mcp.compression.enabled":   true,
			"services.mcp.compression.min_bytes": 0,
		}))
	base := strings.TrimSuffix(endpoint, mcpserve.DefaultPath)
	hdr, small := discover(0)

	t.Run("served with security headers", func(t *testing.T) {
		r := rawDo(t, http.MethodPost, endpoint, "", hdr, strings.NewReader(small))
		require.Equal(t, http.StatusOK, r.status, r.body)
		assert.Equal(t, "nosniff", r.header.Get("X-Content-Type-Options"))
		assert.NotEmpty(t, r.header.Get("Content-Security-Policy"))
		assert.NotEmpty(t, r.header.Get("X-Request-Id"), "slot 1 wraps the listener")
	})

	t.Run("rebinding Host refused by kit", func(t *testing.T) {
		r := rawDo(t, http.MethodPost, endpoint, "evil.example", hdr, strings.NewReader(small))
		require.Equal(t, http.StatusForbidden, r.status, r.body)
		code, refusal := r.rpcError(t)
		assert.Equal(t, -32600, code)
		assert.Equal(t, "host_rejected", refusal)
		assert.Equal(t, "nosniff", r.header.Get("X-Content-Type-Options"), "a refusal carries slot 6")
	})

	t.Run("cross-origin write refused", func(t *testing.T) {
		h := map[string]string{"Origin": "https://evil.example"}
		for k, v := range hdr {
			h[k] = v
		}
		r := rawDo(t, http.MethodPost, endpoint, "", h, strings.NewReader(small))
		require.Equal(t, http.StatusForbidden, r.status, r.body)
		_, refusal := r.rpcError(t)
		assert.Equal(t, "origin_rejected", refusal)
	})

	t.Run("health probes", func(t *testing.T) {
		for _, p := range []string{"/healthz", "/readyz"} {
			r := rawDo(t, http.MethodGet, base+p, "", nil, nil)
			assert.Equal(t, http.StatusOK, r.status, p)
			assert.Contains(t, r.body, `"status"`, p)
		}
		// Probes address a pod by IP: they answer ahead of the Host check.
		r := rawDo(t, http.MethodGet, base+"/healthz", "10.0.0.7:8081", nil, nil)
		assert.Equal(t, http.StatusOK, r.status)
	})

	t.Run("body over the cap", func(t *testing.T) {
		_, big := discover(4096)
		r := rawDo(t, http.MethodPost, endpoint, "", hdr, strings.NewReader(big))
		require.Equal(t, http.StatusRequestEntityTooLarge, r.status, r.body)
		code, refusal := r.rpcError(t)
		assert.Equal(t, -32600, code)
		assert.Equal(t, "body_too_large", refusal)

		// Of unknown length, the read that crosses the cap ends it.
		r = rawDo(t, http.MethodPost, endpoint, "", hdr, unsizedReader{strings.NewReader(big)})
		assert.Equal(t, http.StatusRequestEntityTooLarge, r.status, r.body)

		_, fits := discover(1024)
		r = rawDo(t, http.MethodPost, endpoint, "", hdr, strings.NewReader(fits))
		assert.Equal(t, http.StatusOK, r.status, r.body)
	})

	t.Run("compression negotiated", func(t *testing.T) {
		r := rawDo(t, http.MethodGet, base+"/nothing-here", "", map[string]string{"Accept-Encoding": "gzip"}, nil)
		assert.Equal(t, http.StatusNotFound, r.status)
		assert.Equal(t, "gzip", r.header.Get("Content-Encoding"))
		assert.Contains(t, r.header.Values("Vary"), "Accept-Encoding")
	})
}

// unsizedReader hides the reader type so the body is sent chunked.
type unsizedReader struct{ io.Reader }

// The SDK's own DNS-rebinding check stays beneath kit's, but never
// refuses a host the service's configuration allows.
func TestMCPServiceHTTPHostCheckConfiguration(t *testing.T) {
	hdr, body := discover(0)

	t.Run("an allowed host reaches the SDK", func(t *testing.T) {
		_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
			withConfig(map[string]any{"services.mcp.host_check.allow": []string{"mcp.internal"}}))
		r := rawDo(t, http.MethodPost, endpoint, "mcp.internal", hdr, strings.NewReader(body))
		assert.Equal(t, http.StatusOK, r.status, r.body)

		r = rawDo(t, http.MethodPost, endpoint, "evil.example", hdr, strings.NewReader(body))
		require.Equal(t, http.StatusForbidden, r.status, r.body)
		_, refusal := r.rpcError(t)
		assert.Equal(t, "host_rejected", refusal)
	})

	t.Run("the check switched off", func(t *testing.T) {
		_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
			withConfig(map[string]any{"services.all.host_check.enabled": false}))
		r := rawDo(t, http.MethodPost, endpoint, "tool.example", hdr, strings.NewReader(body))
		assert.Equal(t, http.StatusOK, r.status, r.body)
	})

	t.Run("the SDK's cap follows the block", func(t *testing.T) {
		// 1.5 MiB: over the SDK's 1 MiB default, under the configured cap.
		_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
			withConfig(map[string]any{"services.mcp.body_limit.max_bytes": 2 << 20}))
		_, big := discover(3 << 19)
		r := rawDo(t, http.MethodPost, endpoint, "", hdr, strings.NewReader(big))
		assert.Equal(t, http.StatusOK, r.status, r.body)

		_, endpoint = startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
			withConfig(map[string]any{"services.mcp.body_limit.enabled": false}))
		r = rawDo(t, http.MethodPost, endpoint, "", hdr, strings.NewReader(big))
		assert.Equal(t, http.StatusOK, r.status, "no cap at either layer")
	})

	t.Run("an unknown key is refused", func(t *testing.T) {
		oe := serveErr(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
			withConfig(map[string]any{"services.mcp.body_limit.max_byte": 10}))
		assert.Equal(t, 2, oe.ExitCode)
		assert.Contains(t, oe.Error(), "services.mcp.body_limit.max_byte")
	})

	t.Run("a bad health prefix is refused", func(t *testing.T) {
		oe := serveErr(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
			withConfig(map[string]any{"services.mcp.health.path_prefix": "nope"}))
		assert.Equal(t, 2, oe.ExitCode)
		assert.Contains(t, oe.Error(), "services.mcp.health.path_prefix")
	})
}
