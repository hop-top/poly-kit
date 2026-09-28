package mcpserve_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/mcpserve"
	"hop.top/kit/go/core/identity"
)

// bearerTransport adds a bearer token to every request.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// TestMCPServiceHTTPBearerJWTMode pins services.mcp.auth.mode: jwt on
// the mcp service's HTTP transport: a client presenting a token the
// tool's identity signed connects and runs a kit/auth-required tool;
// a client without one, or with another signer's, does not connect.
func TestMCPServiceHTTPBearerJWTMode(t *testing.T) {
	run, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(t.TempDir(), "identity")}),
		func(r *cli.Root) { r.Viper.Set("services.mcp.auth.mode", "jwt") })
	require.NotNil(t, run.root.Identity)

	now := time.Now()
	good, err := run.root.Identity.SignJWT(identity.Claims{Subject: "alice",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	require.NoError(t, err)
	stranger, err := identity.Generate()
	require.NoError(t, err)
	forged, err := stranger.SignJWT(identity.Claims{Subject: "mallory", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	require.NoError(t, err)

	connect := func(hc *http.Client) (*mcp.ClientSession, error) {
		c := mcp.NewClient(&mcp.Implementation{Name: "cli-test", Version: "0"}, nil)
		return c.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: hc}, nil)
	}

	_, err = connect(http.DefaultClient)
	require.Error(t, err, "no token, no session")
	_, err = connect(&http.Client{Transport: bearerTransport{forged}})
	require.Error(t, err, "another signer's token, no session")

	sess, err := connect(&http.Client{Transport: bearerTransport{good}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	text, isErr := callTool(t, sess, "secret", nil)
	require.False(t, isErr, "the token is an established identity: %s", text)
	assert.Equal(t, "unlocked", text)
}

// oidcProvider is an in-test OpenID provider: discovery naming itself
// as issuer, and a JWKS with one RSA key it signs tokens with.
type oidcProvider struct {
	*httptest.Server
	key *rsa.PrivateKey
}

func newOIDCProvider(t *testing.T) *oidcProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	p := &oidcProvider{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": p.URL, "jwks_uri": p.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
		}})
	})
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

func (p *oidcProvider) token(t *testing.T, aud string) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key},
		(&jose.SignerOptions{}).WithHeader("kid", "k1"))
	require.NoError(t, err)
	raw, err := jwt.Signed(signer).Claims(map[string]any{
		"iss": p.URL, "sub": "alice", "aud": aud, "scope": "items:read",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).Serialize()
	require.NoError(t, err)
	return raw
}

// freeAddr is a loopback address nothing listens on, so the resource
// URL is known before the service binds it.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// TestMCPServiceHTTPAuthorizationFlow pins the MCP authorization flow
// on the mcp service under services.mcp.auth.mode: oidc, checked with
// the SDK's own client-side discovery: a request without a token is
// 401 naming the protected resource metadata; the document validates
// as the SDK validates it, naming the provider; a token issued for the
// endpoint runs a kit/auth-required tool; one for another audience
// gets no session.
func TestMCPServiceHTTPAuthorizationFlow(t *testing.T) {
	idp := newOIDCProvider(t)
	addr := freeAddr(t)
	resource := "http://" + addr + "/mcp"
	_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", addr},
		func(r *cli.Root) {
			r.Viper.Set("services.mcp.auth.mode", "oidc")
			r.Viper.Set("services.mcp.auth.oidc.issuer", idp.URL)
			r.Viper.Set("services.mcp.auth.oidc.audience", resource)
		})
	require.Equal(t, resource, endpoint)

	resp, err := http.Post(endpoint, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	cs, err := oauthex.ParseWWWAuthenticate(resp.Header.Values("WWW-Authenticate"))
	require.NoError(t, err)
	require.Len(t, cs, 1)
	metadataURL := cs[0].Params["resource_metadata"]
	assert.Equal(t, "http://"+addr+"/.well-known/oauth-protected-resource/mcp", metadataURL)

	prm, err := oauthex.GetProtectedResourceMetadata(t.Context(), metadataURL, resource, http.DefaultClient)
	require.NoError(t, err)
	assert.Equal(t, []string{idp.URL}, prm.AuthorizationServers)

	connect := func(tok string) (*mcp.ClientSession, error) {
		c := mcp.NewClient(&mcp.Implementation{Name: "cli-test", Version: "0"}, nil)
		return c.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint,
			HTTPClient: &http.Client{Transport: bearerTransport{tok}}}, nil)
	}
	_, err = connect(idp.token(t, "http://elsewhere.example/mcp"))
	require.Error(t, err, "a token for another resource gets no session")

	sess, err := connect(idp.token(t, resource))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	text, isErr := callTool(t, sess, "secret", nil)
	require.False(t, isErr, text)
	assert.Equal(t, "unlocked", text)
}
