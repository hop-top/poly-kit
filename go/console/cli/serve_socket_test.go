package cli_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/socket"
)

// shortSocketPath returns a socket path short enough for the
// platform's sockaddr_un limit.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cs")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// socketRoot builds a root with the socket service and one leaf
// command to invoke over it.
func socketRoot(t *testing.T, cfg cli.SocketConfig) *cli.Root {
	t.Helper()
	r := newServeRoot(t, cli.WithSocket(cfg))
	r.Cmd.AddCommand(&cobra.Command{
		Use: "ping",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Print("pong")
			return nil
		},
	})
	return r
}

// serveInBackground runs args until the socket answers, then returns
// a stop func. It fails the test if the service never comes up.
func serveInBackground(t *testing.T, r *cli.Root, args []string, path string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.SetArgs(args)
	errCh := make(chan error, 1)
	go func() { errCh <- r.Execute(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if conn, err := net.Dial("unix", path); err == nil {
			_ = conn.Close()
			break
		}
		select {
		case err := <-errCh:
			cancel()
			t.Fatalf("serve returned before the socket was reachable: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("socket never became reachable")
		}
		time.Sleep(20 * time.Millisecond)
	}

	return func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("serve did not return after cancellation")
		}
	}
}

func callSocket(t *testing.T, path string, req socket.Request) socket.Response {
	t.Helper()
	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	require.NoError(t, json.NewEncoder(conn).Encode(req))
	var resp socket.Response
	require.NoError(t, json.NewDecoder(conn).Decode(&resp))
	return resp
}

func TestServeSocketSelectorStartsAndInvokes(t *testing.T) {
	path := shortSocketPath(t)
	r := socketRoot(t, cli.SocketConfig{Path: path})

	// The selector form overrides enablement: the socket service is
	// registered disabled, and naming it starts it anyway.
	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	resp := callSocket(t, path, socket.Request{Path: []string{"ping"}})
	require.True(t, resp.Ok, "expected success, got %+v", resp.Error)
	assert.Contains(t, resp.Result.Stdout, "pong")
}

func TestServeSocketFlagOverridesConfiguredPath(t *testing.T) {
	configured := shortSocketPath(t)
	flagPath := shortSocketPath(t)
	r := socketRoot(t, cli.SocketConfig{Path: configured})

	stop := serveInBackground(t, r,
		[]string{"serve", "socket", "--socket", flagPath}, flagPath)
	defer stop()

	// --socket wins over SocketConfig.Path.
	resp := callSocket(t, flagPath, socket.Request{Path: []string{"ping"}})
	assert.True(t, resp.Ok)

	_, err := os.Stat(configured)
	assert.True(t, os.IsNotExist(err), "the configured path must be unused")
}

func TestServeSocketDisabledUnderSupervisorForm(t *testing.T) {
	path := shortSocketPath(t)
	r := socketRoot(t, cli.SocketConfig{Path: path})

	// Registered but not enabled, and nothing else is configured, so
	// the supervisor form resolves to zero services: a usage error,
	// not a clean exit.
	err := runServeArgs(t, r, []string{"serve"}, 2*time.Second)
	require.Error(t, err)

	var oe *output.Error
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, 2, oe.ExitCode)

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "a disabled service must not bind")
}

func TestServeSocketRejectsOverlongPath(t *testing.T) {
	// A path past sockaddr_un's limit is a configuration error caught
	// by the Validate gate before anything binds, at the contract's
	// exit code 2, rather than a kernel "invalid argument" at start.
	long := filepath.Join(os.TempDir(), strings.Repeat("d", 120), "s.sock")
	r := socketRoot(t, cli.SocketConfig{Path: long})

	err := runServeArgs(t, r, []string{"serve", "socket"}, 2*time.Second)
	require.Error(t, err)

	var oe *output.Error
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, 2, oe.ExitCode)
	assert.Contains(t, err.Error(), "path")
}

func TestServeSocketRejectsPathUnderAFile(t *testing.T) {
	dir, err := os.MkdirTemp("", "cs")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	regular := filepath.Join(dir, "afile")
	require.NoError(t, os.WriteFile(regular, []byte("x"), 0o600))

	r := socketRoot(t, cli.SocketConfig{Path: filepath.Join(regular, "s.sock")})
	err = runServeArgs(t, r, []string{"serve", "socket"}, 2*time.Second)
	require.Error(t, err)

	var oe *output.Error
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, 2, oe.ExitCode)
}

func TestServeSocketAppearsInListing(t *testing.T) {
	r := socketRoot(t, cli.SocketConfig{Path: shortSocketPath(t)})

	var out strings.Builder
	r.Cmd.SetOut(&out)
	r.SetArgs([]string{"serve", "--list"})
	require.NoError(t, r.Execute(context.Background()))

	// The identifier is the CLI word an operator types, so it must be
	// discoverable without reading the source.
	assert.Contains(t, out.String(), "socket")
}

func TestServeSocketRefusesUnexposedAndUnknownCommands(t *testing.T) {
	path := shortSocketPath(t)
	r := socketRoot(t, cli.SocketConfig{Path: path, Hide: []string{"ping"}})

	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	// Hidden from this surface: exists, but not reachable here.
	resp := callSocket(t, path, socket.Request{Path: []string{"ping"}})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeNotEnabled, resp.Error.Code)

	// No such command at all is a different answer.
	resp = callSocket(t, path, socket.Request{Path: []string{"nosuch"}})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeNotFound, resp.Error.Code)
}
