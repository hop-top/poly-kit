package rpcserve_test

// The HTTP-plane middleware on the rpc service's listener: the chain
// every kit HTTP listener shares, configured by services.rpc.*, with
// refusals written as Connect, gRPC and gRPC-Web errors, and the read
// limit and compression Connect enforces per message set from the
// same blocks.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/rpcserve"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
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

// hostRewriter sends every request with the Host header host, the way
// a DNS-rebinding page's request arrives.
type hostRewriter struct {
	base http.RoundTripper
	host string
}

func (h hostRewriter) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = h.host
	return h.base.RoundTrip(r)
}

func clientAs(base, host string, p protocol) cmdsurfacev1connect.CommandsClient {
	hc := h2cClient()
	hc.Transport = hostRewriter{base: hc.Transport, host: host}
	return cmdsurfacev1connect.NewCommandsClient(hc, base, p.opts...)
}

// rawInvoke POSTs a Connect unary Invoke with a JSON body, as a
// Connect client sends it, and returns the reply undecoded.
func rawInvoke(t *testing.T, base string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		base+"/cmdsurface.v1.Commands/Invoke", strings.NewReader(`{"path":["ping"]}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	c := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(raw)
}

func getPath(t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestRPCServiceHTTPPlane(t *testing.T) {
	base := startDefault(t, rpcserve.Config{}, withConfig(map[string]any{
		"services.rpc.body_limit.max_bytes":  1024,
		"services.rpc.compression.enabled":   true,
		"services.rpc.compression.min_bytes": 0,
	}))

	t.Run("served with security headers", func(t *testing.T) {
		resp, body := rawInvoke(t, base, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode, body)
		assert.Contains(t, body, "pong")
		assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		assert.NotEmpty(t, resp.Header.Get("X-Request-Id"), "slot 1 wraps the listener")
	})

	t.Run("rebinding Host refused on every protocol", func(t *testing.T) {
		for _, p := range protocols {
			_, err := clientAs(base, "evil.example", p).Invoke(t.Context(), call("ping", nil))
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), p.name)
			assert.ErrorContains(t, err, "host_rejected", p.name)

			_, _, err = streamAll(t.Context(), clientAs(base, "evil.example", p), call("tick", nil))
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), p.name+" stream")
		}
	})

	t.Run("health probes", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, getPath(t, base+"/healthz"))
		assert.Equal(t, http.StatusOK, getPath(t, base+"/readyz"))
	})

	t.Run("message over the cap on every protocol", func(t *testing.T) {
		for _, p := range protocols {
			req := call("ping", nil)
			req.Msg.Args = []string{strings.Repeat("x", 4096)}
			_, err := client(base, p).Invoke(t.Context(), req)
			assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err), p.name)

			req = call("tick", nil)
			req.Msg.Args = []string{strings.Repeat("x", 4096)}
			_, _, err = streamAll(t.Context(), client(base, p), req)
			assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err), p.name+" stream")

			req = call("ping", nil)
			req.Msg.Args = []string{strings.Repeat("x", 512)}
			_, err = client(base, p).Invoke(t.Context(), req)
			assert.NotEqual(t, connect.CodeResourceExhausted, connect.CodeOf(err), p.name+" under the cap")
		}
	})

	t.Run("compression negotiated per message", func(t *testing.T) {
		resp, _ := rawInvoke(t, base, map[string]string{"Accept-Encoding": "gzip"})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	})
}

// Compression is off by default: Connect answers uncompressed even to
// a client that accepts gzip, as every other kit listener does.
func TestRPCServiceCompressionOffByDefault(t *testing.T) {
	base := startDefault(t, rpcserve.Config{})
	resp, body := rawInvoke(t, base, map[string]string{"Accept-Encoding": "gzip"})
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	assert.Contains(t, body, "pong")
}

// The block overrides the code option, and the read limit follows it.
func TestRPCServiceBodyLimitBlockOverridesTheCodeOption(t *testing.T) {
	base := startDefault(t, rpcserve.Config{MaxBodyBytes: 1024},
		withConfig(map[string]any{"services.rpc.body_limit.max_bytes": 64 << 10}))
	req := call("ping", nil)
	req.Msg.Args = []string{strings.Repeat("x", 4096)}
	_, err := client(base, protocols[1]).Invoke(t.Context(), req)
	assert.NotEqual(t, connect.CodeResourceExhausted, connect.CodeOf(err))
}

func TestRPCServiceHTTPPlaneConfigurationRefusals(t *testing.T) {
	oe := serveErr(t, rpcserve.Config{}, []string{"rpc", "--rpc-addr", "127.0.0.1:0"},
		withConfig(map[string]any{"services.rpc.health.path_prefix": "nope"}))
	assert.Equal(t, 2, oe.ExitCode)
	assert.Contains(t, oe.Error(), "services.rpc.health.path_prefix")

	oe = serveErr(t, rpcserve.Config{}, []string{"rpc", "--rpc-addr", "127.0.0.1:0"},
		withConfig(map[string]any{"services.rpc.compression.min_bytes": -1}))
	assert.Equal(t, 2, oe.ExitCode)
	assert.Contains(t, oe.Error(), "compression.min_bytes")
}
