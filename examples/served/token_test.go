package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/rpcserve"
)

// runCLI executes the root with args and returns stdout and the error.
func runCLI(t *testing.T, root *cli.Root, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root.Cmd.SetOut(&out)
	root.Cmd.SetErr(io.Discard)
	root.SetArgs(args)
	err := root.Execute(context.Background())
	return out.String(), err
}

// TestTokenCreateServeAndCall is the round trip: `token create` signs
// a token with the tool's identity keypair; `serve` with
// services.all.auth.mode: jwt accepts it on the api and rpc services,
// where `item sync` — kit/auth-required — runs for it and is refused
// without it; `token verify` agrees.
func TestTokenCreateServeAndCall(t *testing.T) {
	ident := &cli.IdentityConfig{Dir: filepath.Join(t.TempDir(), "identity")}

	out, err := runCLI(t, newRoot(options{identity: ident}),
		"token", "create", "--sub", "alice", "--tenant", "acme", "--scopes", "items:read", "--expires", "10m")
	require.NoError(t, err)
	token := strings.TrimSpace(out)
	require.Len(t, strings.Split(token, "."), 3, out)

	out, err = runCLI(t, newRoot(options{identity: ident}), "token", "verify", token)
	require.NoError(t, err, out)
	assert.Contains(t, out, `"valid": true`)

	run := startServe(t, options{identity: ident, config: map[string]any{"services.all.auth.mode": "jwt"}},
		"--enable", "rpc", "--addr", "127.0.0.1:0", "--rpc-addr", "127.0.0.1:0")
	api := "http://" + run.waitReady(t, "api").Address
	rpcBase := run.waitReady(t, rpcserve.ServiceName).Address

	t.Run("api", func(t *testing.T) {
		status, body := httpDo(t, http.MethodGet, api+"/v1/commands/item/sync", "")
		assert.Equal(t, http.StatusUnauthorized, status, string(body))

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, api+"/v1/commands/item/sync", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
		res := decodeResult(t, raw)
		assert.Equal(t, 0, res.ExitCode, res.Stderr)
		assert.Contains(t, res.Stdout, "synced 2 items")
	})

	t.Run("rpc", func(t *testing.T) {
		for name, c := range rpcClients(rpcBase) {
			t.Run(name, func(t *testing.T) {
				_, err := c.Invoke(t.Context(), rpcCall("item sync", nil))
				assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

				req := rpcCall("item sync", nil)
				req.Header().Set("Authorization", "Bearer "+token)
				resp, err := c.Invoke(t.Context(), req)
				require.NoError(t, err)
				assert.Equal(t, int32(0), resp.Msg.GetExitCode(), resp.Msg.GetStderr())
				assert.Contains(t, resp.Msg.GetStdout(), "synced 2 items")
			})
		}
	})

	t.Run("token create is never served", func(t *testing.T) {
		status, body := httpDo(t, http.MethodPost, api+"/v1/commands/token/create", `{"flags":{"sub":"mallory"}}`)
		assert.NotEqual(t, http.StatusOK, status, string(body))
		assert.NotContains(t, string(body), "eyJ", "no token is minted over a served surface")
	})
}

// TestAPIKeyIssueServeAndRevoke is the API key round trip: `token key
// create` issues a key; `serve` with services.all.auth.mode: apikey
// accepts it in X-API-Key on the api and rpc services for `item sync`;
// `token key revoke` stops it while the services run.
func TestAPIKeyIssueServeAndRevoke(t *testing.T) {
	keys := filepath.Join(t.TempDir(), "apikeys.db")
	out, err := runCLI(t, newRoot(options{apiKeys: keys}), "token", "key", "create", "--sub", "ci-bot", "--scopes", "items:read")
	require.NoError(t, err)
	key := strings.TrimSpace(out)
	id := strings.Split(key, "_")[1]

	run := startServe(t, options{apiKeys: keys, config: map[string]any{"services.all.auth.mode": "apikey"}},
		"--enable", "rpc", "--addr", "127.0.0.1:0", "--rpc-addr", "127.0.0.1:0")
	api := "http://" + run.waitReady(t, "api").Address
	c := rpcClients(run.waitReady(t, rpcserve.ServiceName).Address)["connect"]

	sync := func() (int, int32) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, api+"/v1/commands/item/sync", nil)
		require.NoError(t, err)
		req.Header.Set("X-API-Key", key)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		r := rpcCall("item sync", nil)
		r.Header().Set("X-API-Key", key)
		res, err := c.Invoke(t.Context(), r)
		if err != nil {
			return resp.StatusCode, -int32(connect.CodeOf(err))
		}
		return resp.StatusCode, res.Msg.GetExitCode()
	}
	status, exit := sync()
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, int32(0), exit)

	_, err = runCLI(t, newRoot(options{apiKeys: keys}), "token", "key", "revoke", id)
	require.NoError(t, err)
	status, exit = sync()
	assert.Equal(t, http.StatusUnauthorized, status, "revoked over REST")
	assert.Equal(t, -int32(connect.CodeUnauthenticated), exit, "revoked over RPC")
}
