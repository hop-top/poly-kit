package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/socket"
)

// keySources gives a root both ways of issuing credentials, each in a
// directory of the test's own.
func keySources(t *testing.T) []func(*cli.Root) {
	t.Helper()
	dir := t.TempDir()
	return []func(*cli.Root){
		cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(dir, "identity")}),
		cli.WithAPIKeys(cli.APIKeysConfig{Path: filepath.Join(dir, "keys", "apikeys.db")}),
	}
}

// tokenPaths are the token leaves a served caller could aim at.
var tokenPaths = [][]string{
	{"token", "key", "create"}, {"token", "key", "list"}, {"token", "key", "revoke"},
	{"token", "create"}, {"token", "verify"}, {"token", "decode"}, {"token", "claims"},
}

// TestTokenIsNeverServedOverREST pins the reserved-command protection
// on the api service: every token leaf is withheld from the projection,
// described as not invocable, and a call to one mints nothing.
func TestTokenIsNeverServedOverREST(t *testing.T) {
	r := newServeRoot(t, append(keySources(t), cli.WithAPI(cli.APIConfig{Addr: "127.0.0.1:0"}))...)
	base, stop := serveAPIInBackground(t, r, []string{"serve", "api"})
	defer stop()

	resp, err := http.Get(base + "/v1/commands")
	require.NoError(t, err)
	var doc api.DiscoveryDocument
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))
	_ = resp.Body.Close()
	listed := 0
	for _, e := range doc.Commands {
		if e.Name == "token" || strings.HasPrefix(e.Name, "token ") {
			listed++
			assert.False(t, e.Invocable, "%s is invocable over REST", e.Name)
		}
	}
	assert.NotZero(t, listed, "the token leaves exist and are described as withheld")

	for _, p := range tokenPaths {
		url := base + "/v1/commands/" + strings.Join(p, "/")
		resp, err := http.Post(url, "application/json", strings.NewReader(`{"flags":{"sub":"mallory"}}`))
		require.NoError(t, err)
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.GreaterOrEqual(t, resp.StatusCode, 400, "%s answered %d: %s", url, resp.StatusCode, raw)
		assert.NotContains(t, string(raw), "kit_", "%s minted a key", url)
	}
}

// TestTokenIsNeverServedOverTheSocket pins the same on the socket, for
// a tool with no REST api at all: the key sources alone mount token,
// and it stays kit-reserved.
func TestTokenIsNeverServedOverTheSocket(t *testing.T) {
	path := shortSocketPath(t)
	r := newServeRoot(t, append(keySources(t), cli.WithSocket(cli.SocketConfig{Path: path}))...)
	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	for _, p := range tokenPaths {
		resp := callSocket(t, path, socket.Request{Path: p, Flags: map[string]any{"sub": "mallory"}})
		assert.False(t, resp.Ok, "%v ran over the socket", p)
		if resp.Result != nil {
			assert.NotContains(t, resp.Result.Stdout, "kit_", "%v minted a key", p)
		}
	}
}

// TestAdoptersOwnTokenCommandIsStillServedOverTheSocket pins the other
// side of the socket's withholding: it covers kit's own token command
// only. A tool with no key source mounts none, and a token command of
// its own is the adopter's to serve.
func TestAdoptersOwnTokenCommandIsStillServedOverTheSocket(t *testing.T) {
	path := shortSocketPath(t)
	r := newServeRoot(t, cli.WithSocket(cli.SocketConfig{Path: path}))
	require.False(t, r.IsReserved("token"), "no key source, no kit token command")
	own := &cobra.Command{Use: "token", Short: "the adopter's own"}
	own.AddCommand(&cobra.Command{
		Use:  "hello",
		RunE: func(cmd *cobra.Command, _ []string) error { cmd.Print("hi"); return nil },
	})
	r.Cmd.AddCommand(own)
	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	resp := callSocket(t, path, socket.Request{Path: []string{"token", "hello"}})
	require.True(t, resp.Ok, "%+v", resp.Error)
	assert.Equal(t, "hi", resp.Result.Stdout)
}
