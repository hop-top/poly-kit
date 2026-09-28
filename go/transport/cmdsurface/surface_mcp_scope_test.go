package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// TestMountMCPScopeRefusalChallenge pins the MCP authorization spec's
// runtime insufficient-scope answer on the deprecated MountMCP: a
// bearer caller whose token lacks a scope the tool declares gets 403
// with an RFC 6750 §3.1 challenge naming the scope and the protected
// resource's metadata document, beside the isError result.
func TestMountMCPScopeRefusalChallenge(t *testing.T) {
	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	pr, err := api.NewProtectedResource(srv.URL+"/mcp", []string{"https://login.example.com"}, nil)
	require.NoError(t, err)
	verify := func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") == "Bearer good" {
			return api.Claims{Subject: "alice", Scopes: []string{"items:read"}}, nil
		}
		return nil, errors.New("no")
	}
	root := newMCPTestTree()
	root.AddCommand(&cobra.Command{
		Use: "vault", Short: "needs items:write",
		Annotations: map[string]string{"kit/side-effect": "read", "kit/permissions": "items:write"},
		RunE:        func(*cobra.Command, []string) error { return nil },
	})
	ran := 0
	b := New(root, WithRunner(&fakeRunner{run: func(context.Context, Invocation) (Result, error) {
		ran++
		return Result{}, nil
	}}))
	r := api.NewRouter()
	require.NoError(t, MountMCP(b, r, WithMCPProtectedResource(pr, verify)))
	handler = r

	resp, err := http.Post(srv.URL+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vault"}}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "no token")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vault"}}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer good")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	var rpc jsonRPCResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpc))
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t,
		`Bearer error="insufficient_scope", scope="items:write", resource_metadata="`+pr.MetadataURL()+`"`,
		resp.Header.Get("WWW-Authenticate"))
	assert.Equal(t, true, resultAsMap(t, rpc)["isError"], "the result still carries the refusal")
	assert.Zero(t, ran)
}

// TestMCPRefusalStatus pins which refusals change the HTTP status:
// only a scope refusal of a caller who presented a bearer token.
func TestMCPRefusalStatus(t *testing.T) {
	scope := &InsufficientScopeError{Path: "vault", Surface: SurfaceMCP, Required: []string{"items:write"}, Missing: []string{"items:write"}}
	bearer := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	bearer.Header.Set("Authorization", "bearer t")
	plain := httptest.NewRequest(http.MethodPost, "/mcp", nil)

	w := httptest.NewRecorder()
	assert.Equal(t, http.StatusForbidden, mcpRefusalStatus(w, bearer, scope))
	assert.Equal(t, `Bearer error="insufficient_scope", scope="items:write"`, w.Header().Get("WWW-Authenticate"),
		"no protected resource: no resource_metadata")

	for _, c := range []struct {
		req *http.Request
		err error
	}{{plain, scope}, {bearer, ErrPermissionDenied}, {bearer, ErrRateLimited}} {
		w := httptest.NewRecorder()
		assert.Equal(t, http.StatusOK, mcpRefusalStatus(w, c.req, c.err), "%v", c.err)
		assert.Empty(t, w.Header().Get("WWW-Authenticate"))
	}
}
