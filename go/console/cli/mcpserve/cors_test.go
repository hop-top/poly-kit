package mcpserve_test

// services.mcp.cors, HTTP-plane slot 9 on the mcp service's HTTP
// transport: a browser MCP client on a granted origin gets its
// preflights answered and reads the session id it must send back.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/mcpserve"
)

const (
	corsApp   = "https://app.example"
	corsOther = "https://other.example"
)

const legacyInit = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05",` +
	`"capabilities":{},"clientInfo":{"name":"browser","version":"0"}}}`

func TestMCPServiceCORS(t *testing.T) {
	_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		withConfig(map[string]any{"services.mcp.cors.allow_origins": []string{corsApp}}))

	preflight := func(origin, method, headers string) planeReply {
		return rawDo(t, http.MethodOptions, endpoint, "", map[string]string{
			"Origin":                         origin,
			"Access-Control-Request-Method":  method,
			"Access-Control-Request-Headers": headers,
		}, nil)
	}

	t.Run("the streamable transport's preflights granted", func(t *testing.T) {
		for method, headers := range map[string]string{
			http.MethodPost:   "authorization,content-type,mcp-method,mcp-name,mcp-protocol-version,mcp-session-id",
			http.MethodGet:    "last-event-id,mcp-protocol-version,mcp-session-id",
			http.MethodDelete: "mcp-protocol-version,mcp-session-id",
		} {
			r := preflight(corsApp, method, headers)
			assert.Equal(t, http.StatusNoContent, r.status, method)
			assert.Equal(t, corsApp, r.header.Get("Access-Control-Allow-Origin"), method)
			assert.Equal(t, method, r.header.Get("Access-Control-Allow-Methods"))
			assert.Equal(t, headers, r.header.Get("Access-Control-Allow-Headers"), method)
		}
	})

	t.Run("another origin's preflight is not", func(t *testing.T) {
		r := preflight(corsOther, http.MethodPost, "content-type")
		assert.Empty(t, r.header.Get("Access-Control-Allow-Origin"))
	})

	t.Run("a granted origin reads the session id", func(t *testing.T) {
		r := rawDo(t, http.MethodPost, endpoint, "", map[string]string{
			"Origin":       corsApp,
			"Content-Type": "application/json",
			"Accept":       "application/json, text/event-stream",
		}, strings.NewReader(legacyInit))
		require.Equal(t, http.StatusOK, r.status, r.body)
		assert.NotEmpty(t, r.header.Get("Mcp-Session-Id"))
		assert.Equal(t, corsApp, r.header.Get("Access-Control-Allow-Origin"))
		assert.Contains(t, r.header.Get("Access-Control-Expose-Headers"), "Mcp-Session-Id")
	})

	t.Run("another origin's write is refused at slot 8", func(t *testing.T) {
		r := rawDo(t, http.MethodPost, endpoint, "", map[string]string{
			"Origin":       corsOther,
			"Content-Type": "application/json",
			"Accept":       "application/json, text/event-stream",
		}, strings.NewReader(legacyInit))
		require.Equal(t, http.StatusForbidden, r.status, r.body)
		_, refusal := r.rpcError(t)
		assert.Equal(t, "origin_rejected", refusal)
		assert.Empty(t, r.header.Get("Access-Control-Allow-Origin"))
	})
}

func TestMCPServiceCORSConfigurationRefusals(t *testing.T) {
	oe := serveErr(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		withConfig(map[string]any{"services.mcp.cors.allow_origins": []string{"mcp.example"}}))
	assert.Equal(t, 2, oe.ExitCode)
	assert.Contains(t, oe.Error(), "services.mcp.cors.allow_origins")
}
