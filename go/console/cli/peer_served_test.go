package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/alias"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/cmdmeta"
	"hop.top/kit/go/console/cli/internal/testhome"
	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/runtime/peer"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/socket"
)

// peerSources gives a root an identity and the peer mesh, in
// directories of the test's own.
func peerSources(t *testing.T) []func(*cli.Root) {
	t.Helper()
	dir := t.TempDir()
	return []func(*cli.Root){
		cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(dir, "identity")}),
		cli.WithPeers(cli.PeerConfig{DataDir: filepath.Join(dir, "peers")}),
	}
}

// seedPendingPeer records a LAN announcer the way first contact does:
// known, not trusted, waiting on a person's trust step.
func seedPendingPeer(t *testing.T, r *cli.Root) string {
	t.Helper()
	stranger, err := identity.Generate()
	require.NoError(t, err)
	pub, err := stranger.MarshalPublicKey()
	require.NoError(t, err)
	info := peer.PeerInfo{ID: stranger.PublicKeyID(), Name: "evil", Addrs: []string{"127.0.0.1:1"}, PublicKey: pub}
	require.NoError(t, r.PeerTrust.AcceptTOFU(info))
	requirePending(t, r, info.ID)
	return info.ID
}

func requirePending(t *testing.T, r *cli.Root, id string) {
	t.Helper()
	rec, err := r.PeerRegistry.Get(id)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, peer.PendingTOFU, rec.Trust, "the peer is still waiting on a person")
}

// peerPaths are the peer leaves a served caller could aim at.
var peerPaths = [][]string{{"peer", "list"}, {"peer", "trust"}, {"peer", "block"}, {"peer", "revoke"}}

// TestPeerIsReserved pins that the peer command, mounted by cli.New
// once the mesh is up, is kit-reserved like every other command kit
// mounts.
func TestPeerIsReserved(t *testing.T) {
	testhome.Isolate(t)
	r := newServeRoot(t, peerSources(t)...)
	assert.True(t, r.IsReserved("peer"))
}

// TestPeerIsNeverServedOverREST pins that no REST caller lists, trusts,
// blocks or revokes a peer: the trust step stays a person's.
func TestPeerIsNeverServedOverREST(t *testing.T) {
	testhome.Isolate(t)
	r := newServeRoot(t, append(peerSources(t), cli.WithAPI(cli.APIConfig{Addr: "127.0.0.1:0"}))...)
	id := seedPendingPeer(t, r)
	base, stop := serveAPIInBackground(t, r, []string{"serve", "api"})
	defer stop()

	resp, err := http.Get(base + "/v1/commands")
	require.NoError(t, err)
	var doc api.DiscoveryDocument
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))
	_ = resp.Body.Close()
	for _, e := range doc.Commands {
		if e.Name == "peer" || strings.HasPrefix(e.Name, "peer ") {
			assert.False(t, e.Invocable, "%s is invocable over REST", e.Name)
		}
	}

	for _, p := range peerPaths {
		url := base + "/v1/commands/" + strings.Join(p, "/")
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req, err := http.NewRequest(method, url, strings.NewReader(`{"args":["`+id+`"]}`))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			assert.GreaterOrEqual(t, resp.StatusCode, 400, "%s %s answered %d: %s", method, url, resp.StatusCode, raw)
		}
	}
	requirePending(t, r, id)
}

// TestPeerIsNeverServedOverTheSocket pins the same on the socket.
func TestPeerIsNeverServedOverTheSocket(t *testing.T) {
	testhome.Isolate(t)
	path := shortSocketPath(t)
	r := newServeRoot(t, append(peerSources(t), cli.WithSocket(cli.SocketConfig{Path: path}))...)
	id := seedPendingPeer(t, r)
	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	for _, p := range peerPaths {
		resp := callSocket(t, path, socket.Request{Path: p, Args: []string{id}})
		require.False(t, resp.Ok, "%v ran over the socket", p)
		assert.Equal(t, socket.CodeNotEnabled, resp.Error.Code, "%v: %s", p, resp.Error.Message)
	}
	requirePending(t, r, id)
}

// TestAliasCommandIsReservedAndWithheld pins the same for kit's alias
// group, which an adopter mounts after cli.New, at the top level or
// under a group of its own: alias add and remove rewrite the tool's
// alias file, and no served caller may. The tree reserves itself, so
// where it is mounted does not matter.
func TestAliasCommandIsReservedAndWithheld(t *testing.T) {
	for name, parent := range map[string]string{"top level": "", "nested under a group": "settings"} {
		t.Run(name, func(t *testing.T) {
			testhome.Isolate(t)
			path := shortSocketPath(t)
			file := filepath.Join(t.TempDir(), "aliases.yaml")
			r := newServeRoot(t, cli.WithSocket(cli.SocketConfig{Path: path}))
			aliasCmd := r.AliasCmd(alias.NewStore(file))
			prefix := []string{}
			if parent != "" {
				group := &cobra.Command{Use: parent, Short: "the adopter's group"}
				group.AddCommand(aliasCmd)
				r.Cmd.AddCommand(group)
				prefix = []string{parent}
			} else {
				r.Cmd.AddCommand(aliasCmd)
			}
			assert.True(t, cmdmeta.InReservedTree(aliasCmd))

			stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
			defer stop()
			for _, verb := range []string{"list", "add", "delete"} {
				p := append(append([]string{}, prefix...), "alias", verb)
				resp := callSocket(t, path, socket.Request{Path: p, Args: []string{"x", "serve"}})
				require.False(t, resp.Ok, "%v ran over the socket", p)
				assert.Equal(t, socket.CodeNotEnabled, resp.Error.Code, "%v", p)
			}
			_, err := os.Stat(file)
			assert.True(t, os.IsNotExist(err), "no served caller wrote the alias file")
		})
	}
}
