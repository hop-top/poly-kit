package cli_test

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
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

// peerSink records what the socket's bridge audits.
type peerSink struct {
	mu   sync.Mutex
	invs []cmdsurface.Invocation
	errs []error
}

func (s *peerSink) Emit(_ context.Context, inv cmdsurface.Invocation, _ cmdsurface.Result, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invs = append(s.invs, inv)
	s.errs = append(s.errs, err)
	return nil
}

func (s *peerSink) last(t *testing.T) (cmdsurface.Invocation, error) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.invs, "nothing was audited")
	return s.invs[len(s.invs)-1], s.errs[len(s.errs)-1]
}

// peerRoot is socketRoot with an auth-required leaf and an audit sink.
func peerRoot(t *testing.T, cfg cli.SocketConfig, sink *peerSink) *cli.Root {
	t.Helper()
	r := newServeRoot(t, cli.WithSocket(cfg),
		cli.WithAuditSinks(cmdsurface.SinkSpec{Sink: sink, OnOK: true, OnError: true}))
	r.Cmd.AddCommand(&cobra.Command{
		Use:         "open",
		Annotations: map[string]string{"kit/auth-required": "true"},
		RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("opened"); return nil },
	})
	return r
}

func skipWithoutPeerCreds(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "android", "darwin", "freebsd":
	default:
		t.Skipf("no peer credentials on %s", runtime.GOOS)
	}
}

// services.socket.auth.mode: peer names the caller by the kernel's
// account of the connection. The auth-required leaf runs for it as a
// verified caller, and the code Auth is not consulted: the mode
// selects the verifier.
func TestServeSocketPeerAuthAdmitsTheSameUID(t *testing.T) {
	skipWithoutPeerCreds(t)
	path := shortSocketPath(t)
	sink := &peerSink{}
	r := peerRoot(t, cli.SocketConfig{
		Path: path,
		Auth: func(context.Context, net.Conn, socket.Request) (socket.Identity, error) {
			return socket.Identity{}, errors.New("the code authenticator must not run under auth.mode: peer")
		},
	}, sink)
	r.Viper.Set("services.socket.auth.mode", "peer")
	r.Viper.Set("services.socket.auth.peer.require_same_uid", true)

	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	resp := callSocket(t, path, socket.Request{Path: []string{"open"}, Caller: "mallory"})
	require.True(t, resp.Ok, "%+v", resp.Error)
	assert.Contains(t, resp.Result.Stdout, "opened")

	inv, err := sink.last(t)
	require.NoError(t, err)
	uid := strconv.Itoa(os.Getuid())
	assert.Equal(t, "uid:"+uid, inv.Meta.Caller)
	assert.Equal(t, cmdsurface.EstablishedVerified, inv.Meta.Established)
	assert.Equal(t, uid, inv.Meta.Extra[socket.ExtraPeerUID])
}

// The shared default reaches the socket like any block: an
// services.all.auth.mode of peer selects the peer authenticator.
func TestServeSocketPeerAuthFromTheSharedDefault(t *testing.T) {
	skipWithoutPeerCreds(t)
	path := shortSocketPath(t)
	sink := &peerSink{}
	r := peerRoot(t, cli.SocketConfig{Path: path}, sink)
	r.Viper.Set("services.all.auth.mode", "peer")

	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	resp := callSocket(t, path, socket.Request{Path: []string{"open"}, Caller: "mallory"})
	require.True(t, resp.Ok, "%+v", resp.Error)
	inv, _ := sink.last(t)
	assert.Equal(t, "uid:"+strconv.Itoa(os.Getuid()), inv.Meta.Caller)
}

// Every socket auth misconfiguration is a usage error at exit 2 that
// names the key, caught before anything binds.
func TestServeSocketAuthRefusals(t *testing.T) {
	cases := []struct {
		name string
		keys map[string]any
		want string
	}{
		{"mtls on the socket", map[string]any{"services.socket.auth.mode": "mtls"},
			`services.socket.auth.mode: "mtls" needs an HTTP listener, and the socket service has none`},
		{"unknown mode", map[string]any{"services.socket.auth.mode": "kerberos"},
			`services.socket.auth.mode: unknown mode "kerberos"; the socket service supports "peer"`},
		{"unknown shared mode", map[string]any{"services.all.auth.mode": "kerberos"},
			`services.all.auth.mode: unknown mode "kerberos"`},
		{"peer keys without the mode", map[string]any{"services.socket.auth.peer.require_same_uid": true},
			`services.socket.auth.peer.require_same_uid: set, but services.socket.auth.mode is not "peer"`},
		{"shared peer keys without the mode", map[string]any{"services.all.auth.peer.resolve_names": true},
			`services.all.auth.peer.resolve_names: set, but services.socket.auth.mode is not "peer"`},
		{"not a bool", map[string]any{
			"services.socket.auth.mode":                  "peer",
			"services.socket.auth.peer.require_same_uid": "sometimes",
		}, "services.socket.auth.peer.require_same_uid:"},
		{"unknown peer key", map[string]any{
			"services.socket.auth.mode":            "peer",
			"services.socket.auth.peer.allow_root": true,
		}, `unknown key "allow_root"`},
	}
	for _, tc := range cases {
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

// services.all.auth.mode: mtls is the HTTP listeners' default; the
// socket does not read it and serves as it would without it.
func TestServeSocketIgnoresSharedMTLS(t *testing.T) {
	path := shortSocketPath(t)
	sink := &peerSink{}
	r := peerRoot(t, cli.SocketConfig{Path: path}, sink)
	r.Viper.Set("services.all.auth.mode", "mtls")

	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	resp := callSocket(t, path, socket.Request{Path: []string{"open"}, Caller: "daemon"})
	require.True(t, resp.Ok, "%+v", resp.Error)
	inv, _ := sink.last(t)
	assert.Equal(t, "daemon", inv.Meta.Caller, "no authenticator: the claim is provenance")
	assert.Equal(t, cmdsurface.EstablishedTransport, inv.Meta.Established)
}
