package socket_test

import (
	"context"
	"errors"
	"net"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
	"hop.top/kit/go/transport/transportsvc"
)

// peerCredPlatform reports whether this platform reads peer
// credentials; the tests that need the kernel's answer skip elsewhere.
func peerCredPlatform() bool {
	switch runtime.GOOS {
	case "linux", "android", "darwin", "freebsd":
		return true
	}
	return false
}

func requirePeerCreds(t *testing.T) {
	t.Helper()
	if !peerCredPlatform() {
		t.Skipf("no peer credentials on %s", runtime.GOOS)
	}
}

func myUID() uint32 { return uint32(os.Getuid()) } //nolint:gosec // a uid is non-negative on every platform the tests run on

// The kernel's answer about a real connection is this process: the
// test dials itself.
func TestPeerCredentialsDescribeTheConnectingProcess(t *testing.T) {
	t.Parallel()
	requirePeerCreds(t)
	path := socketPath(t)
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	client, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	server, err := ln.Accept()
	require.NoError(t, err)
	defer func() { _ = server.Close() }()

	cred, err := socket.PeerCredentials(server)
	require.NoError(t, err)
	assert.Equal(t, myUID(), cred.UID)
	assert.Equal(t, uint32(os.Getegid()), cred.GID) //nolint:gosec // as above
	if runtime.GOOS != "freebsd" {
		assert.Equal(t, os.Getpid(), cred.PID)
	}
}

// A connection the kernel cannot be asked about is an error, never a
// zero-valued peer that would read as root.
func TestPeerCredentialsRefuseAConnWithoutADescriptor(t *testing.T) {
	t.Parallel()
	requirePeerCreds(t)
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	_, err := socket.PeerCredentials(a)
	require.Error(t, err)
}

// Over a real socket, a peer of the server's own uid is admitted with
// require_same_uid on, as uid:<n>, verified, with its credentials
// recorded; the auth-required leaf runs for it.
func TestPeerAuthenticatorAdmitsTheSameUID(t *testing.T) {
	t.Parallel()
	requirePeerCreds(t)
	auth, err := socket.NewPeerAuthenticator(socket.PeerAuthConfig{RequireSameUID: true})
	require.NoError(t, err)
	path := socketPath(t)
	runner := newRecordingRunner(false)
	startAuthRequiredSocket(t, path, auth, runner)

	resp := call(t, path, socket.Request{Path: []string{"open"}, Caller: "mallory"})
	require.True(t, resp.Ok, "%+v", resp.Error)
	got := runner.invocation().Meta
	uid := strconv.FormatUint(uint64(myUID()), 10)
	assert.Equal(t, "uid:"+uid, got.Caller, "the kernel's answer replaces the claim")
	assert.Empty(t, got.Tenant)
	assert.Equal(t, cmdsurface.EstablishedVerified, got.Established)
	assert.Equal(t, uid, got.Extra[socket.ExtraPeerUID])
	assert.Equal(t, strconv.Itoa(os.Getegid()), got.Extra[socket.ExtraPeerGID])
	if runtime.GOOS != "freebsd" {
		assert.Equal(t, strconv.Itoa(os.Getpid()), got.Extra[socket.ExtraPeerPID])
	}
}

// resolve_names makes the principal the user name.
func TestPeerAuthenticatorResolvesTheUserName(t *testing.T) {
	t.Parallel()
	requirePeerCreds(t)
	me, err := user.Current()
	if err != nil || me.Username == "" {
		t.Skip("this process's user has no name to resolve")
	}
	auth, err := socket.NewPeerAuthenticator(socket.PeerAuthConfig{ResolveNames: true})
	require.NoError(t, err)
	path := socketPath(t)
	runner := newRecordingRunner(false)
	startAuthRequiredSocket(t, path, auth, runner)

	resp := call(t, path, socket.Request{Path: []string{"open"}})
	require.True(t, resp.Ok, "%+v", resp.Error)
	assert.Equal(t, me.Username, runner.invocation().Meta.Caller)
}

// A peer of another uid is refused under require_same_uid, never
// reaches the command, and the refusal is audited with the peer's
// credentials. The other uid is injected: a test cannot switch users.
func TestPeerAuthenticatorRefusesAnotherUID(t *testing.T) {
	t.Parallel()
	requirePeerCreds(t)
	other := myUID() + 1
	auth, err := socket.NewPeerAuthenticatorWithCreds(socket.PeerAuthConfig{RequireSameUID: true},
		func(net.Conn) (socket.PeerCred, error) { return socket.PeerCred{UID: other, GID: 20, PID: 4242}, nil })
	require.NoError(t, err)

	path := socketPath(t)
	runner := newRecordingRunner(false)
	rec := &sinkRecorder{}
	startSocketWith(t, path, auth, transportsvc.WithBridgeOptions(
		cmdsurface.WithRunner(runner),
		cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: rec, OnError: true, OnOK: true}),
	))

	resp := call(t, path, socket.Request{Path: []string{"ping"}, RequestID: "req-p"})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeUnauthenticated, resp.Error.Code)
	assert.Equal(t,
		"peer uid "+strconv.FormatUint(uint64(other), 10)+" is not the server's uid "+strconv.FormatUint(uint64(myUID()), 10),
		resp.Error.Message)
	assert.Empty(t, runner.invocation().Path, "a refused peer never reaches the runner")

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.invs, 1)
	assert.ErrorIs(t, rec.errs[0], cmdsurface.ErrAuthRefused)
	assert.Equal(t, "req-p", rec.invs[0].Meta.RequestID)
	assert.Equal(t, strconv.FormatUint(uint64(other), 10), rec.invs[0].Meta.Extra[socket.ExtraPeerUID],
		"the audit record says which peer was refused")
	assert.Equal(t, "4242", rec.invs[0].Meta.Extra[socket.ExtraPeerPID])
}

// Without require_same_uid another uid is admitted under its own name:
// the mode identifies callers, the option restricts them.
func TestPeerAuthenticatorNamesAnotherUIDWithoutTheRestriction(t *testing.T) {
	t.Parallel()
	requirePeerCreds(t)
	auth, err := socket.NewPeerAuthenticatorWithCreds(socket.PeerAuthConfig{},
		func(net.Conn) (socket.PeerCred, error) { return socket.PeerCred{UID: 4040, GID: 20}, nil })
	require.NoError(t, err)
	id, err := auth(context.Background(), nil, socket.Request{})
	require.NoError(t, err)
	assert.Equal(t, "uid:4040", id.Principal)
	assert.Equal(t, map[string]string{socket.ExtraPeerUID: "4040", socket.ExtraPeerGID: "20"}, id.Extra,
		"no pid entry when the platform reported none")
}

// A uid with no user entry keeps the uid form under resolve_names.
func TestPeerAuthenticatorKeepsTheUIDFormWithoutAUserEntry(t *testing.T) {
	t.Parallel()
	requirePeerCreds(t)
	const unnamed = 2147480000
	if _, err := user.LookupId(strconv.Itoa(unnamed)); err == nil {
		t.Skip("the probe uid has a user entry here")
	}
	auth, err := socket.NewPeerAuthenticatorWithCreds(socket.PeerAuthConfig{ResolveNames: true},
		func(net.Conn) (socket.PeerCred, error) { return socket.PeerCred{UID: unnamed}, nil })
	require.NoError(t, err)
	id, err := auth(context.Background(), nil, socket.Request{})
	require.NoError(t, err)
	assert.Equal(t, "uid:"+strconv.Itoa(unnamed), id.Principal)
}

// A peer the kernel cannot describe is refused.
func TestPeerAuthenticatorRefusesUnreadableCredentials(t *testing.T) {
	t.Parallel()
	requirePeerCreds(t)
	auth, err := socket.NewPeerAuthenticatorWithCreds(socket.PeerAuthConfig{},
		func(net.Conn) (socket.PeerCred, error) { return socket.PeerCred{}, errors.New("no descriptor") })
	require.NoError(t, err)
	id, err := auth(context.Background(), nil, socket.Request{})
	require.EqualError(t, err, "no descriptor")
	assert.Empty(t, id.Principal)
}

// Where the kernel has no peer credentials, the authenticator cannot
// be built: a configuration asking for it fails at start.
func TestPeerAuthenticatorUnsupportedPlatform(t *testing.T) {
	t.Parallel()
	if peerCredPlatform() {
		t.Skip("peer credentials are supported here")
	}
	_, err := socket.NewPeerAuthenticator(socket.PeerAuthConfig{})
	require.ErrorIs(t, err, socket.ErrPeerCredUnsupported)
}

// Scopes maps an admitted peer's credentials to its scopes: they
// become the identity's, trimmed and deduplicated, and a peer the
// function grants nothing holds none.
func TestPeerAuthenticatorScopesFromTheCredentials(t *testing.T) {
	t.Parallel()
	requirePeerCreds(t)
	uid := uint32(4040)
	cfg := socket.PeerAuthConfig{Scopes: func(c socket.PeerCred) []string {
		if c.UID == 4040 {
			return []string{" items:admin", "items:read", "", "items:admin"}
		}
		return nil
	}}
	creds := func(net.Conn) (socket.PeerCred, error) { return socket.PeerCred{UID: uid, GID: 20}, nil }
	auth, err := socket.NewPeerAuthenticatorWithCreds(cfg, creds)
	require.NoError(t, err)

	id, err := auth(context.Background(), nil, socket.Request{})
	require.NoError(t, err)
	assert.Equal(t, []string{"items:admin", "items:read"}, id.Scopes)

	uid = 4041
	id, err = auth(context.Background(), nil, socket.Request{})
	require.NoError(t, err)
	assert.Empty(t, id.Scopes)
}

// A refused peer is not asked about, and holds no scopes.
func TestPeerAuthenticatorScopesNotAskedForARefusedPeer(t *testing.T) {
	t.Parallel()
	requirePeerCreds(t)
	asked := false
	cfg := socket.PeerAuthConfig{RequireSameUID: true, Scopes: func(socket.PeerCred) []string {
		asked = true
		return []string{"items:admin"}
	}}
	auth, err := socket.NewPeerAuthenticatorWithCreds(cfg,
		func(net.Conn) (socket.PeerCred, error) { return socket.PeerCred{UID: myUID() + 1}, nil })
	require.NoError(t, err)
	id, err := auth(context.Background(), nil, socket.Request{})
	require.Error(t, err)
	assert.Empty(t, id.Scopes)
	assert.False(t, asked)
}
