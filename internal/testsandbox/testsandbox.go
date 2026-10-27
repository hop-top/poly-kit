// Package testsandbox runs a test binary away from the developer's own
// home: HOME, every per-user XDG base and the runtime dir point at one
// fresh temp dir for the whole binary, so concurrent runs of a package
// (parallel worktrees, a CI matrix) never share a control socket, a
// lock, an identity or a database, and nothing lands in the real home.
package testsandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// perUserVars are the variables that root a per-user path: HOME and
// every XDG base a kit root reads or writes.
var perUserVars = []string{
	"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME",
	"XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
}

// sandbox records what Main replaced; installed is false until Main
// runs.
var sandbox struct {
	sync.Mutex
	installed bool
	root      string
	orig      []string
}

// Main runs m with HOME, every per-user XDG base and XDG_RUNTIME_DIR
// pointed at one fresh temp dir, removed afterwards, and returns m's
// exit code. Call it from TestMain: os.Exit(testsandbox.Main(m)).
//
// The dir comes from os.MkdirTemp with a short prefix, not
// t.TempDir(): a unix socket path is bounded by sun_path (104 bytes
// on darwin), and a test-named temp dir plus
// <tool>/control-<scope>.sock overruns it. Each binary gets its own
// dir, so two runs at once never meet.
func Main(m *testing.M) int {
	root, err := os.MkdirTemp("", "kt")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testsandbox:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(root) }()

	var orig []string
	for _, k := range perUserVars {
		if v := os.Getenv(k); v != "" {
			orig = append(orig, v)
		}
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		orig = append(orig, h)
	}
	for _, k := range perUserVars {
		dir := filepath.Join(root, shortName(k))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			fmt.Fprintln(os.Stderr, "testsandbox:", err)
			return 1
		}
		if err := os.Setenv(k, dir); err != nil {
			fmt.Fprintln(os.Stderr, "testsandbox:", err)
			return 1
		}
	}
	sandbox.Lock()
	sandbox.installed, sandbox.root, sandbox.orig = true, root, orig
	sandbox.Unlock()
	return m.Run()
}

// shortName names k's dir under the sandbox root; the runtime dir is
// one letter to leave sun_path room for tool and scope.
func shortName(k string) string {
	if k == "XDG_RUNTIME_DIR" {
		return "r"
	}
	return strings.ToLower(k)
}

// RequireSandboxed fails t unless Main is installed and every one of
// paths lies in the sandbox, or at least outside every per-user root
// the process started with (its HOME or any XDG base). A test that
// would touch the developer's real home, config or runtime dir fails
// instead of colliding with another run.
func RequireSandboxed(t testing.TB, paths ...string) {
	t.Helper()
	sandbox.Lock()
	installed, root, orig := sandbox.installed, sandbox.root, sandbox.orig
	sandbox.Unlock()
	if !installed {
		t.Fatal("testsandbox.Main is not installed: add `func TestMain(m *testing.M) { os.Exit(testsandbox.Main(m)) }`")
	}
	for _, p := range paths {
		if within(root, p) {
			continue
		}
		for _, r := range orig {
			if within(r, p) {
				t.Errorf("%s resolves under the real per-user dir %s", p, r)
				break
			}
		}
	}
}

// within reports whether p is root or lies beneath it.
func within(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
