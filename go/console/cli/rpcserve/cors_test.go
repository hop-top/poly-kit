package rpcserve_test

// services.rpc.cors, HTTP-plane slot 9 on the rpc listener: a Connect
// or gRPC-Web page on a granted origin gets its preflight answered and
// reads the protocol's headers; every other origin stays refused.

import (
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/rpcserve"
)

const (
	corsApp   = "https://app.example"
	corsOther = "https://other.example"
	invoke    = "/cmdsurface.v1.Commands/Invoke"
)

// corsPreflight is the preflight a browser sends before a Connect or
// gRPC-Web call from origin.
func corsPreflight(t *testing.T, url, origin, headers string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, url, nil)
	require.NoError(t, err)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", headers)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp
}

func TestRPCServiceCORS(t *testing.T) {
	base := startDefault(t, rpcserve.Config{}, withConfig(map[string]any{
		"services.rpc.cors.allow_origins": []string{corsApp},
	}))

	t.Run("Connect and gRPC-Web preflights granted", func(t *testing.T) {
		for _, headers := range []string{
			"connect-protocol-version,connect-timeout-ms,content-type",
			"content-type,grpc-timeout,x-grpc-web,x-user-agent",
			"authorization,content-type,idempotency-key,x-confirm-token",
		} {
			resp := corsPreflight(t, base+invoke, corsApp, headers)
			assert.Equal(t, http.StatusNoContent, resp.StatusCode, headers)
			assert.Equal(t, corsApp, resp.Header.Get("Access-Control-Allow-Origin"), headers)
			assert.Equal(t, headers, resp.Header.Get("Access-Control-Allow-Headers"))
		}
	})

	t.Run("another origin's preflight is not", func(t *testing.T) {
		resp := corsPreflight(t, base+invoke, corsOther, "content-type")
		assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
		assert.Empty(t, resp.Header.Get("Access-Control-Allow-Methods"))
	})

	t.Run("a granted origin calls and reads the status headers", func(t *testing.T) {
		resp, body := rawInvoke(t, base, map[string]string{"Origin": corsApp})
		require.Equal(t, http.StatusOK, resp.StatusCode, body)
		assert.Contains(t, body, "pong")
		assert.Equal(t, corsApp, resp.Header.Get("Access-Control-Allow-Origin"))
		exposed := resp.Header.Get("Access-Control-Expose-Headers")
		for _, h := range []string{"Grpc-Status", "Grpc-Message", "Grpc-Status-Details-Bin", "Idempotent-Replayed"} {
			assert.Contains(t, exposed, h)
		}
	})

	t.Run("another origin's call is refused at slot 8", func(t *testing.T) {
		resp, body := rawInvoke(t, base, map[string]string{"Origin": corsOther})
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, body)
		assert.Contains(t, body, "origin_rejected")
		assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	})
}

// The protected-resource metadata document stays open to every origin
// with cors on: a client finds the authorization server before any
// grant could name its origin.
func TestRPCServiceCORSLeavesTheMetadataDocumentOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	resource := "http://" + addr + "/rpc"

	startRPC(t, rpcserve.With(rpcserve.Config{}), []string{"rpc", "--rpc-addr", addr},
		cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(t.TempDir(), "identity")}),
		withConfig(map[string]any{
			"services.rpc.auth.mode":          "jwt",
			"services.rpc.auth.jwt.issuer":    "https://login.example.com",
			"services.rpc.auth.jwt.audience":  []string{resource},
			"services.rpc.cors.allow_origins": []string{corsApp},
		}))
	base := "http://" + addr

	resp := corsPreflight(t, base+"/.well-known/oauth-protected-resource/rpc", corsOther, "mcp-protocol-version")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))

	// The service itself is still behind the grant.
	resp = corsPreflight(t, base+invoke, corsOther, "authorization")
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	resp = corsPreflight(t, base+invoke, corsApp, "authorization")
	assert.Equal(t, corsApp, resp.Header.Get("Access-Control-Allow-Origin"))
	assert.False(t, strings.Contains(resp.Header.Get("Www-Authenticate"), "Bearer"),
		"a preflight is answered ahead of authentication")
}

func TestRPCServiceCORSConfigurationRefusals(t *testing.T) {
	oe := serveErr(t, rpcserve.Config{}, []string{"rpc", "--rpc-addr", "127.0.0.1:0"},
		withConfig(map[string]any{
			"services.rpc.cors.allow_origins":     "*",
			"services.rpc.cors.allow_credentials": true,
		}))
	assert.Equal(t, 2, oe.ExitCode)
	assert.Contains(t, oe.Error(), "services.rpc.cors.allow_credentials")
}
