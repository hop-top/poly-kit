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
	"hop.top/kit/go/transport/cmdsurface"
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

// The socket has no HTTP listener: an HTTP-plane block set for it
// would act on nothing, so it is refused at exit 2 rather than
// ignored. Invocation-plane blocks and services.all defaults stay
// accepted.
func TestServeSocketRefusesHTTPOnlyBlocks(t *testing.T) {
	for _, key := range []string{
		"services.socket.body_limit.max_bytes",
		"services.socket.health.enabled",
		"services.socket.host_check.enabled",
		"services.socket.origin_check.enabled",
		"services.socket.security_headers.enabled",
		"services.socket.compression.enabled",
		"services.socket.metrics.scrape.enabled",
		"services.socket.tls.cert_file",
		"services.socket.tls.acme.domains",
		"services.socket.auth.mtls.ca_file",
	} {
		t.Run(key, func(t *testing.T) {
			r := socketRoot(t, cli.SocketConfig{Path: shortSocketPath(t)})
			r.Viper.Set(key, 1)
			err := runServeArgs(t, r, []string{"serve", "socket"}, 2*time.Second)
			require.Error(t, err)
			var oe *output.Error
			require.ErrorAs(t, err, &oe)
			assert.Equal(t, 2, oe.ExitCode)
			assert.Contains(t, err.Error(), "no HTTP listener")
		})
	}

	r := socketRoot(t, cli.SocketConfig{Path: shortSocketPath(t)})
	r.Viper.Set("services.all.body_limit.max_bytes", 2048)
	r.Viper.Set("services.socket.audit.redact.patterns", []string{"tok_[a-z]+"})
	err := runServeArgs(t, r, []string{"serve", "socket"}, 2*time.Second)
	assert.NoError(t, err, "a shared default and an invocation-plane block are accepted")
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

// TestServeSocketExposeNarrowsTheTree pins SocketConfig.Expose as a
// narrowing: a non-empty Expose reaches only what it names, and Hide
// then carves exceptions out of it, in that order. An empty Expose
// reaches the whole tree (TestServeSocketSelectorStartsAndInvokes).
func TestServeSocketExposeNarrowsTheTree(t *testing.T) {
	cases := []struct {
		name    string
		cfg     cli.SocketConfig
		reached []string
		refused []string
	}{
		{
			name:    "expose names one leaf",
			cfg:     cli.SocketConfig{Expose: []string{"item list"}},
			reached: []string{"item list"},
			refused: []string{"item add", "ping"},
		},
		{
			name:    "hide applies after expose",
			cfg:     cli.SocketConfig{Expose: []string{"item *"}, Hide: []string{"item add"}},
			reached: []string{"item list"},
			refused: []string{"item add", "ping"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := shortSocketPath(t)
			tc.cfg.Path = path
			r := socketRoot(t, tc.cfg)
			item := &cobra.Command{Use: "item"}
			for _, name := range []string{"list", "add"} {
				item.AddCommand(&cobra.Command{
					Use:  name,
					RunE: func(cmd *cobra.Command, _ []string) error { return nil },
				})
			}
			r.Cmd.AddCommand(item)

			stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
			defer stop()

			for _, leaf := range tc.reached {
				resp := callSocket(t, path, socket.Request{Path: strings.Fields(leaf)})
				assert.True(t, resp.Ok, "%s: expected success, got %+v", leaf, resp.Error)
			}
			for _, leaf := range tc.refused {
				resp := callSocket(t, path, socket.Request{Path: strings.Fields(leaf)})
				if assert.False(t, resp.Ok, "%s: reached, want refused", leaf) {
					assert.Equal(t, socket.CodeNotEnabled, resp.Error.Code, leaf)
				}
			}
		})
	}
}

// destructiveRoot builds a root with a destructive command, so the
// policy path can be exercised end to end over a real socket.
func destructiveRoot(t *testing.T, cfg cli.SocketConfig) *cli.Root {
	t.Helper()
	r := newServeRoot(t, cli.WithSocket(cfg))
	r.Cmd.AddCommand(
		&cobra.Command{
			Use:  "ping",
			RunE: func(cmd *cobra.Command, _ []string) error { cmd.Print("pong"); return nil },
		},
		&cobra.Command{
			Use:         "nuke",
			Annotations: map[string]string{"kit/side-effect": "destructive"},
			RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("destroyed"); return nil },
		},
	)
	return r
}

func TestSocketRefusesDestructiveByDefault(t *testing.T) {
	path := shortSocketPath(t)
	r := destructiveRoot(t, cli.SocketConfig{Path: path})

	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	// The zero Policy resolves like DefaultPolicy: destructive
	// commands are refused on this surface, and the caller is told
	// which command on which surface rather than getting a bare
	// failure.
	resp := callSocket(t, path, socket.Request{Path: []string{"nuke"}})
	require.False(t, resp.Ok)
	require.NotNil(t, resp.Error)
	assert.Equal(t, socket.CodeBlocked, resp.Error.Code)
	assert.Equal(t,
		"cmdsurface: destructive command blocked on this surface: nuke on socket",
		resp.Error.Message)
}

func TestSocketPolicyNamingRPCDoesNotWidenTheSocket(t *testing.T) {
	path := shortSocketPath(t)
	r := destructiveRoot(t, cli.SocketConfig{
		Path: path,
		Policy: cmdsurface.Policy{
			AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceRPC},
		},
	})

	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	// rpc is ConnectRPC, a network transport. The socket is its own
	// surface, so a grant on rpc leaves the socket's ceiling in place.
	resp := callSocket(t, path, socket.Request{
		Path:  []string{"nuke"},
		Flags: map[string]any{"confirm": "yes"},
	})
	require.False(t, resp.Ok, "a grant on rpc must not reach the socket")
	require.NotNil(t, resp.Error)
	assert.Equal(t, socket.CodeBlocked, resp.Error.Code)
	assert.Equal(t,
		"cmdsurface: destructive command blocked on this surface: nuke on socket",
		resp.Error.Message)
}

func TestSocketPermitsDestructiveWhenPolicyNamesTheSurface(t *testing.T) {
	path := shortSocketPath(t)
	r := destructiveRoot(t, cli.SocketConfig{
		Path: path,
		Policy: cmdsurface.Policy{
			AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceSocket},
		},
	})

	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	// Naming the surface lifts the transport's destructive ceiling,
	// but kit's own confirmation gate still applies: over a socket
	// there is no TTY, so an unconfirmed destructive command is
	// refused by the command itself rather than by the bridge.
	resp := callSocket(t, path, socket.Request{Path: []string{"nuke"}})
	require.True(t, resp.Ok, "the bridge must no longer block it")
	require.NotNil(t, resp.Result)
	// The refusal carries the taxonomy's UNAUTHORIZED code, not a
	// flattened 1: a caller branching on the exit code sees the same
	// number it would from a shell.
	assert.Equal(t, output.UnauthorizedError("").ExitCode, resp.Result.ExitCode)
	assert.Contains(t, resp.Result.Stderr, "--confirm=no (or non-TTY default)")

	// Passing the confirmation the command asks for completes it.
	resp = callSocket(t, path, socket.Request{
		Path:  []string{"nuke"},
		Flags: map[string]any{"confirm": "yes"},
	})
	require.True(t, resp.Ok)
	require.NotNil(t, resp.Result)
	assert.Equal(t, 0, resp.Result.ExitCode)
	assert.Contains(t, resp.Result.Stdout, "destroyed")
}

func TestSocketPolicyDoesNotWidenOtherSurfaces(t *testing.T) {
	t.Parallel()
	// Permitting destructive commands on the socket's surface must
	// not make them reachable over MCP, REST, or anything else: a
	// local owner-only channel is a different trust context from a
	// network one.
	p := cmdsurface.Policy{
		AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceSocket},
	}
	destructive := cmdsurface.SafetyClass{Destructive: true}

	assert.True(t, p.Allowed(destructive, cmdsurface.SurfaceSocket),
		"the named surface is permitted")
	for _, s := range cmdsurface.AllSurfaces() {
		if s == cmdsurface.SurfaceSocket || s == cmdsurface.SurfaceCLI || s == cmdsurface.SurfaceLib {
			// CLI and Lib are local-runtime surfaces the gate always
			// allows, independent of this policy.
			continue
		}
		assert.False(t, p.Allowed(destructive, s),
			"naming socket must not widen %s", s)
	}
}

func TestSocketZeroPolicyMatchesDefaultPolicy(t *testing.T) {
	t.Parallel()
	// The Policy field's zero value must change nothing for an
	// adopter who never sets it.
	var zero cmdsurface.Policy
	def := cmdsurface.DefaultPolicy()

	for _, cls := range []cmdsurface.SafetyClass{{}, {Destructive: true}} {
		for _, s := range cmdsurface.AllSurfaces() {
			assert.Equal(t, def.Allowed(cls, s), zero.Allowed(cls, s),
				"zero Policy must match DefaultPolicy for destructive=%v on %s",
				cls.Destructive, s)
		}
	}
}
