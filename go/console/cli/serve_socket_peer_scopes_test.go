package cli_test

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
)

// scopedPeerRoot is peerRoot with a leaf that declares a scope under
// kit/permissions.
func scopedPeerRoot(t *testing.T, cfg cli.SocketConfig, sink *peerSink) *cli.Root {
	t.Helper()
	r := peerRoot(t, cfg, sink)
	r.Cmd.AddCommand(&cobra.Command{
		Use:         "rotate",
		Annotations: map[string]string{"kit/permissions": "items:admin"},
		RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("rotated"); return nil },
	})
	return r
}

// Without a scope source, a peer-authenticated caller holds no scopes:
// a kit/permissions leaf is refused as insufficient scope.
func TestServeSocketPeerHoldsNoScopesByDefault(t *testing.T) {
	skipWithoutPeerCreds(t)
	path := shortSocketPath(t)
	sink := &peerSink{}
	r := scopedPeerRoot(t, cli.SocketConfig{Path: path}, sink)
	r.Viper.Set("services.socket.auth.mode", "peer")

	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	resp := callSocket(t, path, socket.Request{Path: []string{"rotate"}})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeDenied, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "cmdsurface: insufficient scope")
	_, err := sink.last(t)
	assert.ErrorIs(t, err, cmdsurface.ErrInsufficientScope)
}

// rotateOver serves r's socket and calls rotate over it, returning the
// response and what the bridge audited.
func rotateOver(t *testing.T, r *cli.Root, args []string, path string, sink *peerSink) (socket.Response, cmdsurface.Invocation, error) {
	t.Helper()
	stop := serveInBackground(t, r, args, path)
	defer stop()
	resp := callSocket(t, path, socket.Request{Path: []string{"rotate"}})
	inv, err := sink.last(t)
	return resp, inv, err
}

// services.socket.auth.peer.scopes grants every admitted peer its
// scopes, from every configuration source: a leaf naming one of them
// runs, and the scopes reach the gate as a token's would.
func TestServeSocketPeerScopesFromConfig(t *testing.T) {
	skipWithoutPeerCreds(t)
	cases := []struct {
		name string
		set  func(t *testing.T, r *cli.Root)
		args []string
	}{
		{name: "config list", set: func(_ *testing.T, r *cli.Root) {
			r.Viper.Set("services.socket.auth.peer.scopes", []any{"items:read", "items:admin"})
		}},
		{name: "services.all default", set: func(_ *testing.T, r *cli.Root) {
			r.Viper.Set("services.all.auth.peer.scopes", []string{"items:read", "items:admin"})
		}},
		{name: "environment", set: func(t *testing.T, _ *cli.Root) {
			t.Setenv("TEST_SERVICES_SOCKET_AUTH_PEER_SCOPES", "items:read,items:admin")
		}},
		{name: "-c", args: []string{"-c", "services.socket.auth.peer.scopes=items:read items:admin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := shortSocketPath(t)
			sink := &peerSink{}
			r := scopedPeerRoot(t, cli.SocketConfig{Path: path}, sink)
			r.Viper.Set("services.socket.auth.mode", "peer")
			if tc.set != nil {
				tc.set(t, r)
			}
			resp, inv, err := rotateOver(t, r, append(tc.args, "serve", "socket"), path, sink)
			require.True(t, resp.Ok, "%+v", resp.Error)
			assert.Contains(t, resp.Result.Stdout, "rotated")
			require.NoError(t, err)
			assert.Equal(t, cmdsurface.EstablishedVerified, inv.Meta.Established)
			assert.Equal(t, "items:read,items:admin", inv.Meta.Extra["scopes"])
			assert.Equal(t, strconv.Itoa(os.Getuid()), inv.Meta.Extra[socket.ExtraPeerUID],
				"the scopes sit beside the peer credentials")
		})
	}
}

// SocketConfig.PeerScopes maps the kernel's account of the peer to
// its scopes: granted to this uid, the leaf runs; granted to another
// uid only, it is refused as insufficient scope.
func TestServeSocketPeerScopesFromTheCodeHook(t *testing.T) {
	skipWithoutPeerCreds(t)
	me := uint32(os.Getuid()) //nolint:gosec // a uid is non-negative on every platform the tests run on
	for _, tc := range []struct {
		name  string
		grant uint32
		ok    bool
	}{
		{"granted to this uid", me, true},
		{"granted to another uid only", me + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := shortSocketPath(t)
			sink := &peerSink{}
			r := scopedPeerRoot(t, cli.SocketConfig{
				Path: path,
				PeerScopes: func(c socket.PeerCred) []string {
					if c.UID == tc.grant {
						return []string{"items:admin"}
					}
					return nil
				},
			}, sink)
			r.Viper.Set("services.socket.auth.mode", "peer")

			resp, inv, err := rotateOver(t, r, []string{"serve", "socket"}, path, sink)
			if tc.ok {
				require.True(t, resp.Ok, "%+v", resp.Error)
				require.NoError(t, err)
				assert.Equal(t, "items:admin", inv.Meta.Extra["scopes"])
				return
			}
			require.False(t, resp.Ok)
			assert.Equal(t, socket.CodeDenied, resp.Error.Code)
			assert.Contains(t, resp.Error.Message, "cmdsurface: insufficient scope")
			assert.ErrorIs(t, err, cmdsurface.ErrInsufficientScope)
			assert.NotContains(t, inv.Meta.Extra, "scopes")
		})
	}
}

// A configured list replaces the code hook rather than joining it: the
// operator's list is every peer's scopes, and the hook is not asked.
func TestServeSocketPeerScopesConfigReplacesTheHook(t *testing.T) {
	skipWithoutPeerCreds(t)
	for _, tc := range []struct {
		name   string
		config any
		ok     bool
		scopes string
	}{
		{"the list grants what the leaf needs", []string{"items:admin"}, true, "items:admin"},
		{"the list withholds what the hook would grant", []string{"items:read"}, false, "items:read"},
		{"an empty list grants nothing", []string{}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := shortSocketPath(t)
			sink := &peerSink{}
			asked := false
			r := scopedPeerRoot(t, cli.SocketConfig{
				Path: path,
				PeerScopes: func(socket.PeerCred) []string {
					asked = true
					return []string{"items:admin", "items:write"}
				},
			}, sink)
			r.Viper.Set("services.socket.auth.mode", "peer")
			r.Viper.Set("services.socket.auth.peer.scopes", tc.config)

			resp, inv, err := rotateOver(t, r, []string{"serve", "socket"}, path, sink)
			assert.False(t, asked, "a configured list replaces the hook")
			assert.Equal(t, tc.scopes, inv.Meta.Extra["scopes"])
			if tc.ok {
				require.True(t, resp.Ok, "%+v", resp.Error)
				require.NoError(t, err)
				return
			}
			require.False(t, resp.Ok)
			assert.ErrorIs(t, err, cmdsurface.ErrInsufficientScope)
		})
	}
}

// auth.peer.scopes is the socket's: set under any other service, the
// adopter's included, serve refuses it at exit 2, naming the key,
// before anything binds. A value that is not a list of names, and the
// key under another mode, are refused the same way.
func TestServeSocketPeerScopesRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys map[string]any
		want string
	}{
		{"under the api service", map[string]any{"services.api.auth.peer.scopes": []string{"items:admin"}},
			"services.api.auth.peer: only the socket service applies auth.peer; remove it, or set it under services.socket.auth.peer"},
		{"under the mcp service", map[string]any{"services.mcp.auth.peer.scopes": "items:admin"},
			"services.mcp.auth.peer: only the socket service applies auth.peer"},
		{"under an adopter service", map[string]any{"services.worker.auth.peer.scopes": "items:admin"},
			"services.worker.auth.peer: only the socket service applies auth.peer"},
		{"without the mode", map[string]any{"services.socket.auth.peer.scopes": []string{"items:admin"}},
			`services.socket.auth.peer.scopes: set, but services.socket.auth.mode is not "peer"`},
		{"a map where the list belongs", map[string]any{
			"services.socket.auth.mode":        "peer",
			"services.socket.auth.peer.scopes": map[string]any{"items": "admin"},
		}, "services.socket.auth.peer.scopes: must be a list of scope names"},
		{"a non-string entry", map[string]any{
			"services.socket.auth.mode":        "peer",
			"services.socket.auth.peer.scopes": []any{"items:read", 7},
		}, "services.socket.auth.peer.scopes: must be a list of scope names"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := shortSocketPath(t)
			r := socketRoot(t, cli.SocketConfig{Path: path})
			for k, v := range tc.keys {
				r.Viper.Set(k, v)
			}
			err := runServeArgs(t, r, []string{"serve", "socket"}, 2*time.Second)
			require.Error(t, err)
			var oe *output.Error
			require.ErrorAs(t, err, &oe)
			assert.Equal(t, 2, oe.ExitCode)
			assert.Contains(t, err.Error(), tc.want)
			_, statErr := os.Stat(path)
			assert.True(t, os.IsNotExist(statErr), "a refused configuration must not bind")
		})
	}
}
