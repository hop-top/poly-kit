package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// TestMountMCPProtectedResource pins WithMCPProtectedResource on the
// deprecated MountMCP, the same flow as mcpsdk.WithProtectedResource:
// the metadata document is public, a request without a verified
// credential is 401 with the resource's challenge before any JSON-RPC
// is read, and a verified one runs a kit/auth-required tool.
func TestMountMCPProtectedResource(t *testing.T) {
	var handler http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	pr, err := api.NewProtectedResource(srv.URL+"/mcp", []string{"https://login.example.com"}, nil)
	require.NoError(t, err)
	verify := func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") == "Bearer good" {
			return api.Claims{Subject: "alice"}, nil
		}
		return nil, errors.New("no")
	}
	b := New(newMCPTestTree(), WithRunner(&fakeRunner{run: func(context.Context, Invocation) (Result, error) {
		return Result{Stdout: "unlocked"}, nil
	}}))
	r := api.NewRouter()
	require.NoError(t, MountMCP(b, r, WithMCPProtectedResource(pr, verify)))
	handler = r

	resp, err := http.Get(srv.URL + pr.MetadataPath())
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, srv.URL+"/mcp", doc["resource"])

	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"secret"}}`
	post := func(auth string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(call))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}
	resp = post("")
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, pr.Challenge(), resp.Header.Get("WWW-Authenticate"))

	resp = post("Bearer good")
	var rpc jsonRPCResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpc))
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, false, resultAsMap(t, rpc)["isError"])

	assert.Error(t, MountMCP(b, api.NewRouter(), WithMCPProtectedResource(pr, nil)))
}

// TestMountMCPProtectedResourceWireFixture replays the api package's
// protected-resource wire fixture against MountMCP: the deprecated
// mount answers the authorization flow as the SDK surface does.
func TestMountMCPProtectedResourceWireFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "api", "testdata", "protected-resource-wire.json"))
	require.NoError(t, err)
	var fx struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		GoodToken            string   `json:"good_token"`
		Cases                []struct {
			Name    string            `json:"name"`
			Method  string            `json:"method"`
			Path    string            `json:"path"`
			Auth    string            `json:"authorization"`
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &fx))
	pr, err := api.NewProtectedResource(fx.Resource, fx.AuthorizationServers, nil)
	require.NoError(t, err)
	verify := func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") == "Bearer "+fx.GoodToken {
			return api.Claims{Subject: "alice"}, nil
		}
		return nil, errors.New("invalid token")
	}
	r := api.NewRouter()
	require.NoError(t, MountMCP(New(newMCPTestTree()), r, WithMCPProtectedResource(pr, verify)))
	for _, c := range fx.Cases {
		t.Run(c.Name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(c.Method, c.Path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
			if c.Auth != "" {
				req.Header.Set("Authorization", c.Auth)
			}
			r.ServeHTTP(rec, req)
			assert.Equal(t, c.Status, rec.Code)
			for k, v := range c.Headers {
				assert.Equal(t, v, rec.Header().Get(k), k)
			}
			assert.Equal(t, c.Body, rec.Body.String())
		})
	}
}
