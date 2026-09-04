package socket_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
	"hop.top/kit/go/transport/transportsvc"
)

// socketPath returns a short path inside a temp dir. Unix domain
// socket paths are capped near 104 bytes on darwin, and t.TempDir()
// under a long test name can exceed it, so the directory is created
// shallow rather than taken from t.TempDir() verbatim.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sk")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func testRoot() *cobra.Command {
	root := &cobra.Command{Use: "tool"}
	root.AddCommand(
		&cobra.Command{
			Use: "ping",
			RunE: func(cmd *cobra.Command, args []string) error {
				cmd.Print("pong")
				return nil
			},
		},
		&cobra.Command{
			Use:         "nuke",
			Annotations: map[string]string{"kit/side-effect": "destructive"},
			RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("boom"); return nil },
		},
		&cobra.Command{
			Use:    "secret",
			Hidden: true,
			RunE:   func(cmd *cobra.Command, _ []string) error { return nil },
		},
	)
	return root
}

// startSocket brings a socket service up on path and returns it once
// it has reported ready.
func startSocket(t *testing.T, path string, opts ...transportsvc.TransportOption) *transportsvc.TransportService {
	t.Helper()
	opts = append([]transportsvc.TransportOption{transportsvc.Expose("*")}, opts...)
	svc := transportsvc.NewTransportService(
		"socket", testRoot(), cmdsurface.SurfaceRPC, socket.New(path), opts...,
	)

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Start(ctx, func() { ready <- struct{}{} }) }()

	select {
	case <-ready:
	case err := <-errCh:
		cancel()
		t.Fatalf("socket service failed to start: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("socket service never reported ready")
	}

	t.Cleanup(func() {
		_ = svc.Stop(context.Background())
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("socket service did not return after stop")
		}
	})
	return svc
}

// call sends one request and reads one response on its own connection.
func call(t *testing.T, path string, req socket.Request) socket.Response {
	t.Helper()
	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	require.NoError(t, json.NewEncoder(conn).Encode(req))

	var resp socket.Response
	require.NoError(t, json.NewDecoder(conn).Decode(&resp))
	return resp
}

func TestServeInvokesCommandOverRealSocket(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	svc := startSocket(t, path)

	// Readiness carries the bound address, which for a socket is its
	// path — the detail an operator needs to connect.
	assert.Equal(t, path, svc.Addr())

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSocket, "path must be a socket")

	resp := call(t, path, socket.Request{Path: []string{"ping"}})
	require.True(t, resp.Ok, "expected success, got %+v", resp.Error)
	require.NotNil(t, resp.Result)
	assert.Equal(t, 0, resp.Result.ExitCode)
	assert.Contains(t, resp.Result.Stdout, "pong")
}

func TestSocketIsOwnerOnly(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("unix socket permissions are not meaningful on windows")
	}
	path := socketPath(t)
	startSocket(t, path)

	info, err := os.Stat(path)
	require.NoError(t, err)
	// The filesystem permission IS the access control on a unix
	// socket: anything group- or world-reachable would let any local
	// user invoke the tool's commands.
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestSequentialRequestsOnOneConnection(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	startSocket(t, path)

	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(bufio.NewReader(conn))

	for i := 0; i < 3; i++ {
		require.NoError(t, enc.Encode(socket.Request{Path: []string{"ping"}}))
		var resp socket.Response
		require.NoError(t, dec.Decode(&resp))
		require.True(t, resp.Ok)
		assert.Contains(t, resp.Result.Stdout, "pong")
	}
}

func TestUnknownCommandRefusedWithReason(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	startSocket(t, path)

	resp := call(t, path, socket.Request{Path: []string{"nope"}})
	require.False(t, resp.Ok)
	require.NotNil(t, resp.Error)
	assert.Equal(t, socket.CodeNotFound, resp.Error.Code)
	assert.NotEmpty(t, resp.Error.Message)
}

func TestNonInvocableCommandRefusedWithReason(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	startSocket(t, path)

	// A hidden command is excluded by the reflector, so it is
	// reported as unreachable rather than silently missing.
	resp := call(t, path, socket.Request{Path: []string{"secret"}})
	require.False(t, resp.Ok)
	require.NotNil(t, resp.Error)
	assert.Equal(t, socket.CodeNotFound, resp.Error.Code)
}

func TestDestructiveCommandBlockedByPolicy(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	startSocket(t, path)

	resp := call(t, path, socket.Request{Path: []string{"nuke"}})
	require.False(t, resp.Ok)
	require.NotNil(t, resp.Error)
	assert.Equal(t, socket.CodeBlocked, resp.Error.Code)
}

func TestMalformedRequestIsRejected(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	startSocket(t, path)

	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	_, err = conn.Write([]byte("{not json\n"))
	require.NoError(t, err)

	var resp socket.Response
	require.NoError(t, json.NewDecoder(conn).Decode(&resp))
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeInvalid, resp.Error.Code)

	// The connection survives a bad line, so one malformed request
	// does not cost a client its session.
	require.NoError(t, json.NewEncoder(conn).Encode(socket.Request{Path: []string{"ping"}}))
	require.NoError(t, json.NewDecoder(conn).Decode(&resp))
	assert.True(t, resp.Ok)
}

func TestEmptyPathIsRejected(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	startSocket(t, path)

	resp := call(t, path, socket.Request{})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeInvalid, resp.Error.Code)
}

func TestCallerAndTraceTravelIntoMeta(t *testing.T) {
	t.Parallel()
	path := socketPath(t)

	// The seam pins Surface; Caller and TraceID are the caller's
	// claim and must reach Meta so audit sinks can read them.
	var got cmdsurface.Meta
	tr := socket.New(path)
	svc := transportsvc.NewTransportService(
		"socket", testRoot(), cmdsurface.SurfaceRPC, tr,
		transportsvc.Expose("*"),
		transportsvc.WithBridgeOptions(cmdsurface.WithRunner(recorder{&got})),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Start(ctx, func() { ready <- struct{}{} }) }()
	<-ready
	defer func() { _ = svc.Stop(context.Background()); <-errCh }()

	resp := call(t, path, socket.Request{
		Path: []string{"ping"}, Caller: "alice", TraceID: "trace-1",
	})
	require.True(t, resp.Ok)

	assert.Equal(t, "alice", got.Caller)
	assert.Equal(t, "trace-1", got.TraceID)
	assert.Equal(t, cmdsurface.SurfaceRPC, got.Surface, "surface is pinned by the seam")
}

// recorder is a Runner that captures the Meta it was handed.
type recorder struct{ into *cmdsurface.Meta }

func (r recorder) Run(_ context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	*r.into = inv.Meta
	return cmdsurface.Result{}, nil
}

func (r recorder) Stream(context.Context, cmdsurface.Invocation, chan<- cmdsurface.Event) error {
	return errors.New("not implemented")
}

func TestStopUnlinksSocketAndRefusesNewConnections(t *testing.T) {
	t.Parallel()
	path := socketPath(t)

	svc := transportsvc.NewTransportService(
		"socket", testRoot(), cmdsurface.SurfaceRPC, socket.New(path),
		transportsvc.Expose("*"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Start(ctx, func() { ready <- struct{}{} }) }()
	<-ready

	require.NoError(t, svc.Stop(context.Background()))
	select {
	case err := <-errCh:
		require.NoError(t, err, "a stopped socket is a clean stop")
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Stop")
	}

	// The socket file is gone, so the next start is not blocked by a
	// leftover, and a client gets a connection error rather than a
	// hang against a dead path.
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "socket file must be unlinked on stop")

	_, dialErr := net.Dial("unix", path)
	assert.Error(t, dialErr)
}

func TestBindReclaimsStaleSocket(t *testing.T) {
	t.Parallel()
	path := socketPath(t)

	// A socket file left by a crashed process: the file exists but
	// nothing is listening, so a restart must reclaim it rather than
	// refuse to start. SetUnlinkOnClose(false) reproduces exactly
	// that, since a clean Close would remove the file itself.
	stale, err := net.Listen("unix", path)
	require.NoError(t, err)
	ul, ok := stale.(*net.UnixListener)
	require.True(t, ok)
	ul.SetUnlinkOnClose(false)
	require.NoError(t, stale.Close())
	require.FileExists(t, path, "stale socket file should survive the close")

	svc := startSocket(t, path)
	assert.Equal(t, path, svc.Addr())

	resp := call(t, path, socket.Request{Path: []string{"ping"}})
	assert.True(t, resp.Ok)
}

func TestBindRefusesLiveSocket(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	startSocket(t, path)

	// A second service on the same path must refuse rather than
	// silently steal traffic from the running one.
	second := socket.New(path)
	_, err := second.Bind(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already in use")
}

func TestBindRefusesNonSocketPath(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	require.NoError(t, os.WriteFile(path, []byte("regular file"), 0o600))

	_, err := socket.New(path).Bind(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a socket")

	// The file is left alone rather than clobbered.
	b, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, "regular file", string(b))
}

func TestBindRejectsEmptyPath(t *testing.T) {
	t.Parallel()
	_, err := socket.New("").Bind(context.Background())
	require.Error(t, err)
}

func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	tr := socket.New(socketPath(t))
	_, err := tr.Bind(context.Background())
	require.NoError(t, err)

	require.NoError(t, tr.Close(context.Background()))
	require.NoError(t, tr.Close(context.Background()), "second Close must be a no-op")
}

func TestServeBeforeBindFails(t *testing.T) {
	t.Parallel()
	err := socket.New(socketPath(t)).Serve(context.Background(), nil)
	require.Error(t, err)
}
