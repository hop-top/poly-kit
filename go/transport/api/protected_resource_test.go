package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// TestProtectedResourceDescribesItself pins the RFC 9728 shape: the
// metadata path inserts the resource's path after the well-known
// prefix, the URL is on the resource's origin, and the challenge
// names it.
func TestProtectedResourceDescribesItself(t *testing.T) {
	pr, err := api.NewProtectedResource("https://mcp.example.com/mcp", []string{"https://login.example.com/"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "/.well-known/oauth-protected-resource/mcp", pr.MetadataPath())
	assert.Equal(t, "https://mcp.example.com/.well-known/oauth-protected-resource/mcp", pr.MetadataURL())
	assert.Equal(t, `Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp"`, pr.Challenge())

	root, err := api.NewProtectedResource("https://api.example.com", []string{"https://login.example.com/"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "/.well-known/oauth-protected-resource", root.MetadataPath())

	for _, bad := range []string{"mcp", "/mcp", "ftp://x/mcp", "https://x/mcp?a=1", "https://x/mcp#f"} {
		_, err := api.NewProtectedResource(bad, []string{"https://login.example.com/"}, nil)
		assert.Error(t, err, bad)
	}
	_, err = api.NewProtectedResource("https://x/mcp", nil, nil)
	assert.Error(t, err, "an authorization server is required")
}

// TestProtectedResourceGuard pins the guard: the metadata document is
// public, JSON, CORS-open; every other request needs the verifier and
// is refused 401 with the resource's challenge.
func TestProtectedResourceGuard(t *testing.T) {
	pr, err := api.NewProtectedResource("https://mcp.example.com/mcp", []string{"https://login.example.com/"},
		[]string{"items:read"})
	require.NoError(t, err)
	fn := func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") == "Bearer good" {
			return api.Claims{Subject: "alice"}, nil
		}
		return nil, errors.New("no")
	}
	h := pr.Guard(fn)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := api.IdentityOf(api.ClaimsFromContext(r.Context()))
		_, _ = w.Write([]byte(p))
	}))
	do := func(method, path, auth string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := do(http.MethodGet, pr.MetadataPath(), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	assert.Equal(t, "https://mcp.example.com/mcp", doc["resource"])
	assert.Equal(t, []any{"https://login.example.com/"}, doc["authorization_servers"])
	assert.Equal(t, []any{"items:read"}, doc["scopes_supported"])
	assert.Equal(t, []any{"header"}, doc["bearer_methods_supported"])

	assert.Equal(t, http.StatusNoContent, do(http.MethodOptions, pr.MetadataPath(), "").Code)
	assert.Equal(t, http.StatusMethodNotAllowed, do(http.MethodPost, pr.MetadataPath(), "").Code)

	rec = do(http.MethodPost, "/mcp", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, pr.Challenge(), rec.Header().Get("WWW-Authenticate"))
	assert.Contains(t, rec.Body.String(), `"unauthenticated"`)

	rec = do(http.MethodPost, "/mcp", "Bearer good")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "alice", rec.Body.String())
}
