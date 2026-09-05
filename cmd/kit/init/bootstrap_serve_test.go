// Zero-wiring gate for the cli-go template: the rendered tier-3 project
// is a kit root that serves its own commands with no mounting code.
//
// The test renders cli-go through the real bootstrap path, points the
// rendered module at this checkout of kit, compiles it, and drives the
// binary: `serve --list`, `serve api` on loopback, discovery, a read
// over REST, the destructive ceiling, the unauthenticated-remote
// refusal, and the socket through the config file. Every assertion is
// against the built binary, so an exit code is the process's own.
//
// Unlike TestBootstrap_CLIGo_Builds this test does not skip on a build
// failure: a template that does not compile is exactly the defect it
// exists to catch. The module cache already holds every dependency of
// kit itself, and the rendered go.sum is seeded from kit's, so no
// network is needed.
package kitinit

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// destructiveFixture is the command an adopter adds to the rendered
// project. It follows the template's own convention — one file, one
// init — and exists so the test can observe the root's default policy
// on a destructive leaf the template itself does not ship.
const destructiveFixture = `package cmd

import (
	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
)

func init() {
	cmd := &cobra.Command{
		Use:   "nuke",
		Short: "Destroy everything",
		Long:  "Destroy everything. Exists to prove the serve policy withholds it.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("destroyed")
			return nil
		},
	}
	cli.SetSideEffect(cmd, cli.SideEffectDestructive)
	cli.SetIdempotency(cmd, cli.IdempotencyNo)
	cli.SetTopLevelVerb(cmd)
	root.Cmd.AddCommand(cmd)
}
`

// kitRepoRoot locates this checkout of kit from the test file, before
// runBootstrapFor chdirs into a temp dir.
func kitRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// buildRenderedCLIGo renders cli-go, points hop.top/kit at repoRoot,
// adds the destructive fixture, and compiles the binary. It returns
// the binary path and the project dir.
func buildRenderedCLIGo(t *testing.T) (bin, project string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	repoRoot := kitRepoRoot(t)
	target, _ := runBootstrapFor(t, "cli-go")

	// The rendered go.mod pins the kit release that carries the
	// template; this checkout IS that release, so point the module at
	// it. Seeding go.sum from kit's own keeps the build off the network.
	edit := exec.Command("go", "mod", "edit", "-replace", "hop.top/kit="+repoRoot)
	edit.Dir = target
	out, err := edit.CombinedOutput()
	require.NoError(t, err, "go mod edit: %s", out)
	sum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(target, "go.sum"), sum, 0o644))

	require.NoError(t, os.WriteFile(
		filepath.Join(target, "cmd", "zz_nuke.go"), []byte(destructiveFixture), 0o644))

	bin = filepath.Join(target, "bin", "demo")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".")
	build.Dir = target
	build.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err = build.CombinedOutput()
	require.NoError(t, err, "the rendered cli-go project must compile:\n%s", out)
	return bin, target
}

// runRendered runs the rendered binary to completion with an isolated
// HOME, so no config file of the host leaks in.
func runRendered(t *testing.T, bin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = isolatedEnv(t)
	var o, e strings.Builder
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	code := 0
	if err != nil {
		var xe *exec.ExitError
		require.ErrorAs(t, err, &xe, "unexpected run error: %v\nstderr: %s", err, e.String())
		code = xe.ExitCode()
	}
	return o.String(), e.String(), code
}

func isolatedEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	return append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_RUNTIME_DIR="+filepath.Join(home, "run"),
		"NO_COLOR=1",
	)
}

var readyAddr = regexp.MustCompile(`service=api.*address=(\S+)|address=(\S+).*service=api`)

// serveRendered starts `<bin> serve <args>` in the background and waits
// for the api service's readiness line on stderr, returning the bound
// address and a stop func that sends SIGINT and reports the exit code.
func serveRendered(t *testing.T, bin string, args ...string) (addr string, stop func() int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, append([]string{"serve"}, args...)...)
	cmd.Env = isolatedEnv(t)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGINT) }
	stderr, err := cmd.StderrPipe()
	require.NoError(t, err)
	cmd.Stdout = io.Discard
	require.NoError(t, cmd.Start())

	// The startup line lives in the lifecycle trace, not in a scraped
	// string: the ready event's log counterpart carries the resolved
	// address under a structured key.
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	deadline := time.After(20 * time.Second)
	var trace []string
	for addr == "" {
		select {
		case line, ok := <-lines:
			if !ok {
				cancel()
				_ = cmd.Wait()
				t.Fatalf("serve exited before the api reported ready:\n%s", strings.Join(trace, "\n"))
			}
			trace = append(trace, line)
			if !strings.Contains(line, "ready_reported") {
				continue
			}
			if m := readyAddr.FindStringSubmatch(line); m != nil {
				addr = m[1] + m[2]
			}
		case <-deadline:
			cancel()
			_ = cmd.Wait()
			t.Fatalf("api never reported ready:\n%s", strings.Join(trace, "\n"))
		}
	}
	// Keep draining so the child never blocks on a full pipe.
	go func() {
		for range lines {
		}
	}()
	return addr, func() int {
		cancel()
		// Wait reports the canceled context, not the exit status, when
		// the child exits after Cancel ran; the status is on the state.
		_ = cmd.Wait()
		return cmd.ProcessState.ExitCode()
	}
}

func httpGet(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // loopback test server
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

func httpPost(t *testing.T, url, body string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body)) //nolint:gosec // loopback
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, out
}

type discoveryDoc struct {
	Tool     string `json:"tool"`
	Commands []struct {
		Name      string `json:"name"`
		Invocable bool   `json:"invocable"`
		Reason    string `json:"reason"`
	} `json:"commands"`
}

func TestBootstrap_CLIGo_ServesItsCommandsWithoutWiring(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs the rendered project; skip under -short")
	}
	bin, project := buildRenderedCLIGo(t)

	t.Run("cli still answers with kit flags", func(t *testing.T) {
		stdout, stderr, code := runRendered(t, bin, "hello", "Ada", "--format", "json")
		require.Equal(t, 0, code, stderr)
		var got []map[string]any
		require.NoError(t, json.Unmarshal([]byte(stdout), &got), stdout)
		require.Len(t, got, 1)
		assert.Equal(t, "Ada", got[0]["name"])
		assert.Equal(t, "Hello, Ada!", got[0]["message"])

		stdout, _, code = runRendered(t, bin, "hello")
		require.Equal(t, 0, code)
		assert.Contains(t, stdout, "Hello, world!", "the default rendering is the table")
	})

	t.Run("serve --list names api and socket", func(t *testing.T) {
		stdout, stderr, code := runRendered(t, bin, "serve", "--list")
		require.Equal(t, 0, code, stderr)
		assert.Regexp(t, `(?m)^api\s`, stdout)
		assert.Regexp(t, `(?m)^socket\s`, stdout)
		assert.Less(t, strings.Index(stdout, "api"), strings.Index(stdout, "socket"),
			"registration order: the template registers api before socket")
	})

	t.Run("serve --help carries the contract's flags", func(t *testing.T) {
		stdout, stderr, _ := runRendered(t, bin, "serve", "--help")
		help := stdout + stderr
		for _, flag := range []string{
			"--list", "--enable", "--disable", "--ready-timeout", "--stop-timeout",
			"--shutdown-timeout", "--addr", "--insecure-remote", "--socket",
		} {
			assert.Contains(t, help, flag)
		}
		assert.Contains(t, help, "Services: api, socket")
	})

	t.Run("serve api on loopback projects the tree", func(t *testing.T) {
		addr, stop := serveRendered(t, bin, "api", "--addr", "127.0.0.1:0")
		host, _, err := net.SplitHostPort(addr)
		require.NoError(t, err)
		assert.Equal(t, "127.0.0.1", host, "the api service binds loopback")
		base := "http://" + addr

		status, body := httpGet(t, base+"/v1/commands")
		require.Equal(t, http.StatusOK, status, string(body))
		var doc discoveryDoc
		require.NoError(t, json.Unmarshal(body, &doc))
		assert.Equal(t, "demo", doc.Tool)
		byName := map[string]struct {
			Invocable bool
			Reason    string
		}{}
		for _, c := range doc.Commands {
			byName[c.Name] = struct {
				Invocable bool
				Reason    string
			}{c.Invocable, c.Reason}
		}
		require.Contains(t, byName, "hello")
		assert.True(t, byName["hello"].Invocable, "the sample read command is invocable")
		require.Contains(t, byName, "nuke", "a withheld command is still described")
		assert.False(t, byName["nuke"].Invocable)
		assert.Equal(t, "unauthorized-destructive", byName["nuke"].Reason)
		require.Contains(t, byName, "serve")
		assert.Equal(t, "self-hosting", byName["serve"].Reason)
		require.Contains(t, byName, "status")
		assert.Equal(t, "management-only", byName["status"].Reason)

		// A read command runs over REST and answers in data, because
		// the sample declares its output schema.
		status, body = httpGet(t, base+"/v1/commands/hello?arg=Ada")
		require.Equal(t, http.StatusOK, status, string(body))
		var res struct {
			ExitCode int              `json:"exit_code"`
			Data     []map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(body, &res), string(body))
		assert.Equal(t, 0, res.ExitCode)
		require.Len(t, res.Data, 1)
		assert.Equal(t, "Hello, Ada!", res.Data[0]["message"])

		// The destructive command has no route: withheld at mount.
		status, body = httpPost(t, base+"/v1/commands/nuke", `{}`)
		assert.Equal(t, http.StatusNotFound, status, string(body))

		// The OpenAPI floor spec is served without any configuration.
		status, _ = httpGet(t, base+"/openapi.json")
		assert.Equal(t, http.StatusOK, status)

		assert.Equal(t, 0, stop(), "a signal-initiated stop is a clean stop")
	})

	t.Run("unauthenticated remote serving is refused at exit 2", func(t *testing.T) {
		_, stderr, code := runRendered(t, bin, "serve", "api", "--addr", "0.0.0.0:0")
		assert.Equal(t, 2, code, stderr)
		assert.Contains(t, stderr, "not a loopback address")
		assert.Contains(t, stderr, "insecure_remote")
	})

	t.Run("config file reaches the socket service", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("unix sockets")
		}
		dir, err := os.MkdirTemp("", "cs")
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		sock := filepath.Join(dir, "s.sock")
		cfg := filepath.Join(project, "demo.yaml")
		require.NoError(t, os.WriteFile(cfg,
			[]byte(fmt.Sprintf("services:\n  socket:\n    path: %s\n", sock)), 0o644))

		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, bin, "-c", cfg, "serve", "socket")
		cmd.Env = isolatedEnv(t)
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGINT) }
		var stderr strings.Builder
		cmd.Stderr = &stderr
		require.NoError(t, cmd.Start())
		defer func() {
			cancel()
			_ = cmd.Wait()
		}()

		var conn net.Conn
		deadline := time.Now().Add(20 * time.Second)
		for {
			conn, err = net.Dial("unix", sock)
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("socket never came up at the configured path:\n%s", stderr.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
		defer func() { _ = conn.Close() }()

		require.NoError(t, json.NewEncoder(conn).Encode(map[string]any{
			"path": []string{"hello"}, "args": []string{"Ada"},
		}))
		var resp struct {
			Ok     bool `json:"ok"`
			Result struct {
				ExitCode int              `json:"exit_code"`
				Data     []map[string]any `json:"data"`
			} `json:"result"`
		}
		require.NoError(t, json.NewDecoder(conn).Decode(&resp))
		require.True(t, resp.Ok)
		assert.Equal(t, 0, resp.Result.ExitCode)
		require.Len(t, resp.Result.Data, 1)
		assert.Equal(t, "Hello, Ada!", resp.Result.Data[0]["message"])
	})
}
