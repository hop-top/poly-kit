package mcpserve_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/mcpserve"
	_ "hop.top/kit/go/storage/kv/sqlite"
)

// tokenLeaves returns the names under `token` (and `token key`), or
// nil when the root has no token command.
func tokenLeaves(r *cli.Root) []string {
	for _, c := range r.Cmd.Commands() {
		if c.Name() != "token" {
			continue
		}
		var out []string
		for _, l := range c.Commands() {
			out = append(out, l.Name())
			for _, ll := range l.Commands() {
				out = append(out, l.Name()+" "+ll.Name())
			}
		}
		return out
	}
	return nil
}

// runTokenOn runs `token <args>` on r and returns stdout.
func runTokenOn(t *testing.T, r *cli.Root, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(io.Discard)
	r.SetArgs(append([]string{"token"}, args...))
	require.NoError(t, r.Execute(context.Background()))
	return strings.TrimSpace(out.String())
}

// connectWith opens an MCP session at endpoint presenting token as a
// bearer credential; an empty token presents none.
func connectWith(t *testing.T, endpoint, token string) (*mcp.ClientSession, error) {
	t.Helper()
	hc := http.DefaultClient
	if token != "" {
		hc = &http.Client{Transport: bearerTransport{token}}
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "cli-test", Version: "0"}, nil)
	sess, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: hc}, nil)
	if err == nil {
		t.Cleanup(func() { _ = sess.Close() })
	}
	return sess, err
}

// TestMCPOnlyRootIssuesAPIKeys pins that a tool serving only MCP under
// auth.mode: apikey can issue its own keys: WithAPIKeys mounts token
// key create|list|revoke without WithAPI, and a key it issues
// authenticates against the mcp service over HTTP.
func TestMCPOnlyRootIssuesAPIKeys(t *testing.T) {
	isolateServeHome(t)
	path := filepath.Join(t.TempDir(), "keys", "apikeys.db")
	keys := cli.WithAPIKeys(cli.APIKeysConfig{Path: path})
	apikeyMode := func(r *cli.Root) { r.Viper.Set("services.mcp.auth.mode", cli.AuthModeAPIKey) }

	issuer := newServeRoot(t, mcpserve.With(mcpserve.Config{}), keys)
	require.Equal(t, []string{mcpserve.ServiceName}, issuer.ServeRegistry().Names(), "mcp is the only service")
	leaves := tokenLeaves(issuer)
	for _, want := range []string{"key create", "key list", "key revoke"} {
		assert.Contains(t, leaves, want)
	}
	key := runTokenOn(t, issuer, "key", "create", "--service", mcpserve.ServiceName, "--sub", "alice")
	require.True(t, strings.HasPrefix(key, "kit_"), key)

	_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"}, keys, apikeyMode)

	_, err := connectWith(t, endpoint, "")
	require.Error(t, err, "no key, no session")
	_, err = connectWith(t, endpoint, "kit_0000000000000000_"+strings.Repeat("0", 64))
	require.Error(t, err, "a key the tool did not issue, no session")

	sess, err := connectWith(t, endpoint, key)
	require.NoError(t, err)
	text, isErr := callTool(t, sess, "secret", nil)
	require.False(t, isErr, "the key is an established identity: %s", text)
	assert.Equal(t, "unlocked", text)

	list := runTokenOn(t, newServeRoot(t, mcpserve.With(mcpserve.Config{}), keys),
		"key", "list", "--service", mcpserve.ServiceName, "--format", "json")
	assert.Contains(t, list, `"alice"`)
}

// TestMCPOnlyRootSignsTokensForJWTMode pins the same for auth.mode:
// jwt: WithIdentity mounts token create without WithAPI, and a token
// it signs authenticates against the mcp service over HTTP.
func TestMCPOnlyRootSignsTokensForJWTMode(t *testing.T) {
	isolateServeHome(t)
	dir := filepath.Join(t.TempDir(), "identity")
	id := cli.WithIdentity(cli.IdentityConfig{Dir: dir})
	jwtMode := func(r *cli.Root) { r.Viper.Set("services.mcp.auth.mode", "jwt") }

	issuer := newServeRoot(t, mcpserve.With(mcpserve.Config{}), id)
	assert.Contains(t, tokenLeaves(issuer), "create")
	assert.NotContains(t, tokenLeaves(issuer), "key create", "no key store without WithAPIKeys")
	token := runTokenOn(t, issuer, "create", "--sub", "alice", "--expires", "1h")

	_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"}, id, jwtMode)

	_, err := connectWith(t, endpoint, "")
	require.Error(t, err, "no token, no session")

	sess, err := connectWith(t, endpoint, token)
	require.NoError(t, err)
	text, isErr := callTool(t, sess, "secret", nil)
	require.False(t, isErr, "the token is an established identity: %s", text)
	assert.Equal(t, "unlocked", text)
}

// TestTokenIsNeverAnMCPTool pins the reserved-command protection on
// the mcp surface: with every leaf token can have mounted, none of
// them is a tool, so no MCP caller can mint, list, revoke or probe
// keys or tokens.
func TestTokenIsNeverAnMCPTool(t *testing.T) {
	isolateServeHome(t)
	dir := filepath.Join(t.TempDir(), "identity")
	path := filepath.Join(t.TempDir(), "keys", "apikeys.db")
	run, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		cli.WithAPIKeys(cli.APIKeysConfig{Path: path}))
	require.Contains(t, tokenLeaves(run.root), "key create", "the leaves exist on the CLI")
	require.Contains(t, tokenLeaves(run.root), "create")

	sess := dialMCP(t, endpoint, nil, nil)
	names := toolNames(t, sess)
	require.Contains(t, names, "ping", "the catalog is not empty")
	for _, n := range names {
		assert.False(t, n == "token" || strings.HasPrefix(n, "token."), "%q is a tool", n)
	}
	for _, n := range []string{"token.key.create", "token.key.list", "token.create", "token.verify"} {
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: n, Arguments: map[string]any{"sub": "mallory"}})
		assert.True(t, err != nil || res.IsError, "%s must be refused", n)
	}
}

// TestNoTokenCommandWithoutAKeySource pins the other side: a root with
// none of WithAPI, WithAPIKeys and WithIdentity has no token command.
func TestNoTokenCommandWithoutAKeySource(t *testing.T) {
	isolateServeHome(t)
	assert.Nil(t, tokenLeaves(newServeRoot(t, mcpserve.With(mcpserve.Config{}))))
}
