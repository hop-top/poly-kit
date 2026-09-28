package mcpsdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/authn"
	"hop.top/kit/go/transport/cmdsurface"
)

// TestProtectedResourceInsufficientScopeChallenge pins the MCP
// authorization spec's runtime insufficient-scope answer behind
// WithProtectedResource: a tools/call whose bearer token lacks a scope
// the tool declares is 403 with the RFC 6750 §3.1 challenge naming the
// scope and the metadata document, answered before the SDK reads the
// call, and audited; a call the token covers runs as before.
func TestProtectedResourceInsufficientScopeChallenge(t *testing.T) {
	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	resource := srv.URL + "/mcp"
	kp, err := identity.Generate()
	require.NoError(t, err)
	pr, err := api.NewProtectedResource(resource, []string{"https://login.example.com"}, []string{"items:read", "items:write"})
	require.NoError(t, err)
	v, err := authn.NewJWT([]authn.Key{authn.IdentityKey(kp)}, authn.Options{Audience: []string{resource}})
	require.NoError(t, err)

	root := newTestTree()
	root.AddCommand(&cobra.Command{
		Use: "vault", Short: "needs items:write",
		Annotations: map[string]string{"kit/side-effect": "read", "kit/permissions": "items:write"},
		RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("opened"); return nil },
	})
	sink := &errSink{}
	b := cmdsurface.New(root, cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnError: true}))
	r := api.NewRouter()
	require.NoError(t, Mount(b, r, WithProtectedResource(pr, v.AuthFunc())))
	handler = r

	now := time.Now()
	tok, err := kp.SignJWT(identity.Claims{Subject: "alice", Scopes: []string{"items:read"},
		Audience: identity.Audience{resource}, IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, resource,
		strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"vault"}}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var body struct {
		ID    json.RawMessage `json:"id"`
		Error struct {
			Message string `json:"message"`
			Data    struct {
				Code   string   `json:"code"`
				Scopes []string `json:"scopes"`
			} `json:"data"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	_ = resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	cs, err := oauthex.ParseWWWAuthenticate(resp.Header.Values("WWW-Authenticate"))
	require.NoError(t, err)
	require.Len(t, cs, 1)
	assert.Equal(t, "bearer", cs[0].Scheme)
	assert.Equal(t, "insufficient_scope", cs[0].Params["error"])
	assert.Equal(t, "items:write", cs[0].Params["scope"])
	assert.Equal(t, pr.MetadataURL(), cs[0].Params["resource_metadata"])
	assert.JSONEq(t, "7", string(body.ID))
	assert.Equal(t, "insufficient_scope", body.Error.Data.Code)
	assert.Equal(t, []string{"items:write"}, body.Error.Data.Scopes)
	sink.mu.Lock()
	require.Len(t, sink.errs, 1)
	assert.ErrorIs(t, sink.errs[0], cmdsurface.ErrInsufficientScope)
	sink.mu.Unlock()

	// A session on the same token: what it covers runs; the scoped
	// tool is refused at the HTTP layer, which the SDK client surfaces
	// as an error rather than a result.
	hc := &http.Client{Transport: headerTransport{base: http.DefaultTransport, hdr: map[string]string{"Authorization": "Bearer " + tok}}}
	c := mcp.NewClient(&mcp.Implementation{Name: "mcpsdk-test", Version: "0.0.1"}, nil)
	sess, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: resource, HTTPClient: hc}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "secret"})
	require.NoError(t, err)
	require.False(t, res.IsError, textOf(res))
	_, err = sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "vault"})
	require.Error(t, err)
}

// errSink records the error of every invocation it receives.
type errSink struct {
	mu   sync.Mutex
	errs []error
}

func (s *errSink) Emit(_ context.Context, _ cmdsurface.Invocation, _ cmdsurface.Result, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs = append(s.errs, err)
	return nil
}
