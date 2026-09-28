package mcpsdk

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// authFlow is a Mount behind WithProtectedResource on a live server,
// with a JWT verifier bound to the resource and a recorder of what the
// runner saw.
type authFlow struct {
	srv      *httptest.Server
	resource string
	pr       *api.ProtectedResource
	kp       *identity.Keypair
	mu       sync.Mutex
	seen     []cmdsurface.Invocation
}

func newAuthFlow(t *testing.T) *authFlow {
	t.Helper()
	f := &authFlow{}
	var handler http.Handler
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(f.srv.Close)
	f.resource = f.srv.URL + "/mcp"

	var err error
	f.kp, err = identity.Generate()
	require.NoError(t, err)
	f.pr, err = api.NewProtectedResource(f.resource, []string{"https://login.example.com"}, []string{"items:read"})
	require.NoError(t, err)
	v, err := authn.NewJWT([]authn.Key{authn.IdentityKey(f.kp)}, authn.Options{Audience: []string{f.resource}})
	require.NoError(t, err)

	b := cmdsurface.New(newTestTree(), cmdsurface.WithRunnerMiddleware(func(next cmdsurface.Runner) cmdsurface.Runner {
		return recordingRunner{next: next, record: func(inv cmdsurface.Invocation) {
			f.mu.Lock()
			f.seen = append(f.seen, inv)
			f.mu.Unlock()
		}}
	}))
	r := api.NewRouter()
	require.NoError(t, Mount(b, r, WithProtectedResource(f.pr, v.AuthFunc())))
	handler = r
	return f
}

func (f *authFlow) token(t *testing.T, aud string) string {
	t.Helper()
	now := time.Now()
	raw, err := f.kp.SignJWT(identity.Claims{Subject: "alice", Tenant: "acme", Scopes: []string{"items:read"},
		Audience: identity.Audience{aud}, IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	require.NoError(t, err)
	return raw
}

func (f *authFlow) connect(t *testing.T, hdr map[string]string) (*mcp.ClientSession, error) {
	hc := &http.Client{Transport: headerTransport{base: http.DefaultTransport, hdr: hdr}}
	c := mcp.NewClient(&mcp.Implementation{Name: "mcpsdk-test", Version: "0.0.1"}, nil)
	return c.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: f.resource, HTTPClient: hc}, nil)
}

// TestProtectedResourceAuthorizationFlow pins the MCP authorization
// flow on Mount, checked with the SDK's own client-side discovery: an
// unauthenticated request is 401 with a challenge naming the metadata
// document; the document validates as the SDK validates it (resource
// equal to the endpoint, an authorization server); a token for this
// resource runs a kit/auth-required tool as its verified caller; a
// token for another audience, and a forged identity header, get
// nothing.
func TestProtectedResourceAuthorizationFlow(t *testing.T) {
	f := newAuthFlow(t)

	resp, err := http.Post(f.resource, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	cs, err := oauthex.ParseWWWAuthenticate(resp.Header.Values("WWW-Authenticate"))
	require.NoError(t, err)
	require.Len(t, cs, 1)
	assert.Equal(t, "bearer", cs[0].Scheme)
	metadataURL := cs[0].Params["resource_metadata"]
	assert.Equal(t, f.srv.URL+"/.well-known/oauth-protected-resource/mcp", metadataURL)

	prm, err := oauthex.GetProtectedResourceMetadata(t.Context(), metadataURL, f.resource, http.DefaultClient)
	require.NoError(t, err, "the SDK accepts the document for this endpoint")
	assert.Equal(t, []string{"https://login.example.com"}, prm.AuthorizationServers)
	assert.Equal(t, []string{"items:read"}, prm.ScopesSupported)

	_, err = f.connect(t, nil)
	assert.Error(t, err, "no token, no session")
	_, err = f.connect(t, map[string]string{"Authorization": "Bearer " + f.token(t, "https://elsewhere.example/mcp")})
	assert.Error(t, err, "a token for another resource is refused")

	sess, err := f.connect(t, map[string]string{
		"Authorization":         "Bearer " + f.token(t, f.resource),
		"X-Kit-Mcpsdk-Verified": `{"principal":"mallory"}`,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "secret"})
	require.NoError(t, err)
	require.False(t, res.IsError, textOf(res))
	assert.Contains(t, textOf(res), "unlocked")

	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.seen, 1)
	m := f.seen[0].Meta
	assert.Equal(t, "alice", m.Caller, "the verified caller, not the forged header")
	assert.Equal(t, "acme", m.Tenant)
	assert.Equal(t, "items:read", m.Extra["scopes"])
	assert.Equal(t, cmdsurface.EstablishedVerified, m.Established)
}

func TestWithProtectedResourceNeedsBothHalves(t *testing.T) {
	pr, err := api.NewProtectedResource("https://x.example/mcp", []string{"https://login.example.com"}, nil)
	require.NoError(t, err)
	_, err = New(cmdsurface.New(&cobra.Command{Use: "r"}), WithProtectedResource(pr, nil))
	assert.Error(t, err)
	_, err = New(cmdsurface.New(&cobra.Command{Use: "r"}), WithProtectedResource(nil, func(*http.Request) (any, error) { return nil, nil }))
	assert.Error(t, err)
}

// authWire is the protected-resource wire fixture of go/transport/api.
type authWire struct {
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

// TestProtectedResourceWireFixture replays the api package's wire
// fixture against Mount with WithProtectedResource: the SDK surface
// answers the authorization flow byte for byte as every kit MCP mount.
func TestProtectedResourceWireFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "api", "testdata", "protected-resource-wire.json"))
	require.NoError(t, err)
	var fx authWire
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
	require.NoError(t, Mount(cmdsurface.New(newTestTree()), r, WithProtectedResource(pr, verify)))
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
