package mcpserve_test

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
