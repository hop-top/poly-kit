package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/xdg"
	"hop.top/kit/internal/testsandbox"
)

// TestSandbox_PathsStayOutOfRealHome guards the package's TestMain:
// every per-user path a served root resolves (control socket, socket
// service, config, data, state, cache, runtime) lands in the binary's
// own sandbox, never the developer's home or runtime dir, so two test
// binaries running at once never meet.
func TestSandbox_PathsStayOutOfRealHome(t *testing.T) {
	r := &cli.Root{Config: cli.Config{Name: "tool"}}
	ctl, err := controlSocketPath(r, "")
	require.NoError(t, err)
	sock, err := resolveSocketPath(r, &SocketConfig{})
	require.NoError(t, err)
	paths := []string{ctl, sock}
	for _, f := range []func(string) (string, error){
		xdg.RawConfigDir, xdg.RawDataDir, xdg.RawStateDir, xdg.RawCacheDir, xdg.RuntimeDir,
	} {
		p, err := f("tool")
		require.NoError(t, err)
		paths = append(paths, p)
	}
	testsandbox.RequireSandboxed(t, paths...)
}

// TestSandbox_ControlSocketFitsUnixPathLimit pins that the sandbox's
// runtime dir leaves room under sun_path for a scoped control socket.
func TestSandbox_ControlSocketFitsUnixPathLimit(t *testing.T) {
	r := &cli.Root{Config: cli.Config{Name: "tool"}}
	p, err := controlSocketPath(r, strings.Repeat("s", 16))
	require.NoError(t, err)
	require.LessOrEqual(t, len(p), maxSocketPathLen, "control socket %q", p)
}
