// Zero-wiring gate for the cli-go template: the rendered tier-3 project
// is a kit root that serves its own commands with no mounting code.
//
// The test renders cli-go through the real bootstrap path, points the
// rendered module at this checkout of kit, compiles it, and drives the
// binary: `serve --list`, `serve api` on loopback, discovery, a read
// over REST, the destructive ceiling, the unauthenticated-remote
// refusal, the socket through the config file, the mcp service over
// stdio and HTTP, and the rpc service answering the README's call.
// Every assertion is against the built binary, so
// an exit code is the process's own.
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
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// readyAddrOf matches one service's readiness line and captures the
// address it carries, in either field order.
func readyAddrOf(service string) *regexp.Regexp {
	return regexp.MustCompile(`service=` + service + `\b.*address=(\S+)|address=(\S+).*service=` + service + `\b`)
}

// serveRendered starts `<bin> serve <args>` in the background and waits
// for the api service's readiness line on stderr, returning the bound
// address and a stop func that sends SIGINT and reports the exit code.
func serveRendered(t *testing.T, bin string, args ...string) (addr string, stop func() int) {
	t.Helper()
	return serveRenderedUntil(t, bin, "api", args...)
}

// serveRenderedUntil is serveRendered waiting on the named service's
// readiness line instead of the api's.
func serveRenderedUntil(t *testing.T, bin, service string, args ...string) (addr string, stop func() int) {
	t.Helper()
	readyAddr := readyAddrOf(service)
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
				t.Fatalf("serve exited before %s reported ready:\n%s", service, strings.Join(trace, "\n"))
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
			t.Fatalf("%s never reported ready:\n%s", service, strings.Join(trace, "\n"))
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

// readmeBlock returns the rendered README's text between the end of
// start and the next occurrence of end.
func readmeBlock(t *testing.T, project, start, end string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(project, "README.md"))
	require.NoError(t, err)
	_, after, found := strings.Cut(string(body), start)
	require.True(t, found, "README has no %q", start)
	block, _, found := strings.Cut(after, end)
	require.True(t, found, "README block after %q is unterminated", start)
	return block
}

// renderedMCPTools lists the tool names a session offers.
func renderedMCPTools(ctx context.Context, t *testing.T, sess *mcp.ClientSession) []string {
	t.Helper()
	res, err := sess.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// lockedBuffer is a strings.Builder safe for a child's stderr copier
// and the test to share.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
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

	t.Run("spec coverage is 100% out of the box", func(t *testing.T) {
		// Every command the scaffold ships, and the fixture an adopter
		// adds by copying it, declares its side-effect class, so the
		// CI gate the template advertises passes on a fresh project.
		stdout, stderr, code := runRendered(t, bin, "spec", "coverage", "--min", "100")
		require.Equal(t, 0, code, "spec coverage --min 100 must pass:\n%s\n%s", stdout, stderr)
		var rep struct {
			Total       int  `json:"total"`
			Annotated   int  `json:"annotated"`
			Unannotated int  `json:"unannotated"`
			Measurable  bool `json:"measurable"`
		}
		require.NoError(t, json.Unmarshal([]byte(stdout), &rep), stdout)
		assert.True(t, rep.Measurable)
		assert.Positive(t, rep.Total)
		assert.Equal(t, rep.Total, rep.Annotated)
		assert.Zero(t, rep.Unannotated)
	})

	t.Run("serve --list names api, socket, mcp and rpc", func(t *testing.T) {
		stdout, stderr, code := runRendered(t, bin, "serve", "--list")
		require.Equal(t, 0, code, stderr)
		// The README prints this table; only api is enabled out of the
		// box, and nothing is ready when asked from the shell.
		assert.Regexp(t, `(?m)^api\s+true\s+true\s+false\s*$`, stdout)
		assert.Regexp(t, `(?m)^socket\s+false\s+false\s+false\s*$`, stdout)
		assert.Regexp(t, `(?m)^mcp\s+false\s+false\s+false\s*$`, stdout)
		assert.Regexp(t, `(?m)^rpc\s+false\s+false\s+false\s*$`, stdout)
		assert.Less(t, strings.Index(stdout, "api"), strings.Index(stdout, "socket"),
			"registration order: the template registers api before socket")
		assert.Less(t, strings.Index(stdout, "socket"), strings.Index(stdout, "mcp"),
			"registration order: the template registers socket before mcp")
		assert.Less(t, strings.Index(stdout, "mcp"), strings.Index(stdout, "rpc"),
			"registration order: the template registers mcp before rpc")

		// The README shows a fresh project's table verbatim.
		assert.Equal(t, readmeBlock(t, project, "$ demo serve --list\n", "```"), stdout,
			"README's serve --list table must be what the binary prints")
	})

	t.Run("serve --help carries the contract's flags", func(t *testing.T) {
		stdout, stderr, _ := runRendered(t, bin, "serve", "--help")
		help := stdout + stderr
		for _, flag := range []string{
			"--list", "--enable", "--disable", "--ready-timeout", "--stop-timeout",
			"--shutdown-timeout", "--addr", "--insecure-remote", "--socket",
			"--stdio", "--mcp-addr", "--rpc-addr",
		} {
			assert.Contains(t, help, flag)
		}
		assert.Contains(t, help, "Services: api, socket, mcp, rpc")
	})

	t.Run("serve mcp --stdio answers a spawning host", func(t *testing.T) {
		// What the README's desktop-host snippet does: spawn the tool,
		// speak MCP on its stdin and stdout, close stdin to end. The
		// SDK client rejects any stdout line that is not a protocol
		// message, so the session working at all proves stdout is
		// clean.
		// Spawn exactly what the README's host entry names, with the
		// built binary standing in for the command on PATH.
		var hosts struct {
			MCPServers map[string]struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"mcpServers"`
		}
		snippet := readmeBlock(t, project, "```json\n{\n  \"mcpServers\"", "```")
		require.NoError(t, json.Unmarshal([]byte(`{
  "mcpServers"`+snippet), &hosts), snippet)
		entry, ok := hosts.MCPServers["demo"]
		require.True(t, ok, "the README's host entry is keyed by the tool's name")
		assert.Equal(t, "demo", entry.Command)
		require.Equal(t, []string{"serve", "mcp", "--stdio"}, entry.Args)

		// A host that never gets an answer must fail the test, not hang
		// it until the suite's deadline.
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()

		cmd := exec.Command(bin, entry.Args...)
		cmd.Env = isolatedEnv(t)
		var stderr lockedBuffer
		cmd.Stderr = &stderr
		client := mcp.NewClient(&mcp.Implementation{Name: "desktop-host", Version: "0"}, nil)
		sess, err := client.Connect(ctx,
			&mcp.CommandTransport{Command: cmd, TerminateDuration: 10 * time.Second}, nil)
		require.NoError(t, err, stderr.String())

		tools := renderedMCPTools(ctx, t, sess)
		assert.Contains(t, tools, "hello", "every invocable command is a tool")
		for _, withheld := range []string{"nuke", "serve", "status"} {
			assert.NotContains(t, tools, withheld,
				"the tool list carries only what may run: %s is withheld", withheld)
		}

		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "hello", Arguments: map[string]any{}})
		require.NoError(t, err)
		require.False(t, res.IsError)
		data, err := json.Marshal(res.StructuredContent)
		require.NoError(t, err)
		assert.Contains(t, string(data), "Hello, world!",
			"the sample declares its schema, so it answers in structured content")

		require.NoError(t, sess.Close())
		require.NotNil(t, cmd.ProcessState)
		assert.Equal(t, 0, cmd.ProcessState.ExitCode(),
			"end of input is a clean stop:\n%s", stderr.String())
		assert.Contains(t, stderr.String(), "service=mcp", "the lifecycle trace goes to stderr")
	})

	t.Run("serve mcp over HTTP binds its own loopback listener", func(t *testing.T) {
		endpoint, stop := serveRenderedUntil(t, bin, "mcp", "mcp", "--mcp-addr", "127.0.0.1:0")
		assert.Regexp(t, `^http://127\.0\.0\.1:\d+/mcp$`, endpoint)

		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		client := mcp.NewClient(&mcp.Implementation{Name: "http-host", Version: "0"}, nil)
		sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
		require.NoError(t, err)
		assert.Contains(t, renderedMCPTools(ctx, t, sess), "hello")
		_ = sess.Close()

		assert.Equal(t, 0, stop(), "a signal-initiated stop is a clean stop")
	})

	t.Run("unauthenticated remote mcp is refused at exit 2", func(t *testing.T) {
		_, stderr, code := runRendered(t, bin, "serve", "mcp", "--mcp-addr", "0.0.0.0:0")
		assert.Equal(t, 2, code, stderr)
		assert.Contains(t, stderr, "services.mcp.insecure_remote")
	})

	t.Run("serve rpc answers the README's call on its own loopback listener", func(t *testing.T) {
		// The README's curl: its procedure path and body, and the
		// reply it shows, against the built binary.
		section := readmeBlock(t, project, "### Call it over gRPC\n", "\n## ")
		curl := regexp.MustCompile(`(?s)demo serve rpc\ncurl -s http://127\.0\.0\.1:8082(/\S+).*?-d '([^']*)'`).
			FindStringSubmatch(section)
		require.Len(t, curl, 3, "README has no serve rpc + curl example:\n%s", section)
		procedure, payload := curl[1], curl[2]
		assert.Equal(t, "/cmdsurface.v1.Commands/Invoke", procedure)
		shown := regexp.MustCompile("(?s)```json\n(.*?)\n```").FindStringSubmatch(section)
		require.Len(t, shown, 2, "README shows no reply for the call")

		base, stop := serveRenderedUntil(t, bin, "rpc", "rpc", "--rpc-addr", "127.0.0.1:0")
		require.Regexp(t, `^http://127\.0\.0\.1:\d+$`, base, "the rpc service binds loopback")

		status, body := httpPost(t, base+procedure, payload)
		require.Equal(t, http.StatusOK, status, string(body))
		assert.JSONEq(t, shown[1], string(body), "README's reply must be what the binary answers")

		// A withheld destructive command is refused before it runs,
		// as the README says.
		status, body = httpPost(t, base+procedure, `{"path":["nuke"]}`)
		assert.Equal(t, http.StatusForbidden, status, string(body))
		assert.Contains(t, string(body), `"permission_denied"`)

		assert.Equal(t, 0, stop(), "a signal-initiated stop is a clean stop")
	})

	t.Run("unauthenticated remote rpc is refused at exit 2", func(t *testing.T) {
		_, stderr, code := runRendered(t, bin, "serve", "rpc", "--rpc-addr", "0.0.0.0:0")
		assert.Equal(t, 2, code, stderr)
		assert.Contains(t, stderr, "services.rpc.insecure_remote")
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

		// AGENTS.md tells an agent to read route and method off
		// discovery and never to derive them, and that both are
		// absent exactly when a command has no route.
		var shaped struct {
			Commands []struct {
				Name             string `json:"name"`
				Invocable        bool   `json:"invocable"`
				Method           string `json:"method"`
				Route            string `json:"route"`
				SideEffectSource string `json:"side_effect_source"`
			} `json:"commands"`
			ExitStatus []struct {
				ExitCode int `json:"exit_code"`
				Status   int `json:"status"`
			} `json:"exit_status"`
		}
		require.NoError(t, json.Unmarshal(body, &shaped))
		for _, c := range shaped.Commands {
			if c.Name == "hello" {
				assert.Equal(t, "declared", c.SideEffectSource,
					"the sample declares its class, and discovery says so")
			}
			if c.Invocable {
				assert.NotEmpty(t, c.Route, "%s is invocable, so it has a route", c.Name)
				assert.NotEmpty(t, c.Method, "%s is invocable, so it has a method", c.Name)
				continue
			}
			assert.Empty(t, c.Route, "%s is withheld, so it has no route", c.Name)
			assert.Empty(t, c.Method, "%s is withheld, so it has no method", c.Name)
		}
		// The exit-code table the fragment reproduces is published by
		// the tool itself, so an agent never has to trust the doc.
		mapping := map[int]int{}
		for _, p := range shaped.ExitStatus {
			mapping[p.ExitCode] = p.Status
		}
		// 7 CONSENT_REFUSED shares 403 with UNAUTHORIZED (re-send the
		// same call with confirmation); 70 PREREQUISITE is 503, not
		// 500, because a declared dependency is unreachable and the
		// identical call succeeds once an operator repairs it.
		assert.Equal(t, map[int]int{
			0: 200, 1: 500, 2: 400, 3: 404, 4: 409, 5: 403, 6: 503, 7: 403,
			64: 429, 65: 422, 70: 503,
		}, mapping, "the fragment's exit-code table must be the tool's own")

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

		// AGENTS.md tells an agent that method follows side-effect and
		// to take it from discovery rather than deriving it: hello is
		// a read, so it is GET-only and a POST is refused outright.
		status, body = httpPost(t, base+"/v1/commands/hello", `{}`)
		assert.Equal(t, http.StatusMethodNotAllowed, status, string(body))

		// And that an undeclared *query* parameter is dropped rather
		// than refused — the trap the fragment names explicitly, since
		// a misspelled flag on a GET runs the command without it.
		status, body = httpGet(t, base+"/v1/commands/hello?nosuchflag=x&arg=Ada")
		require.Equal(t, http.StatusOK, status, string(body))
		assert.Contains(t, string(body), "Hello, Ada!",
			"an undeclared query parameter is dropped, not refused")

		assert.Equal(t, 0, stop(), "a signal-initiated stop is a clean stop")
	})

	t.Run("serve api applies the scaffold's serve defaults", func(t *testing.T) {
		// cmd/root.go sets middleware defaults under services.all. On a
		// loopback bind kit alone leaves the rate limit off, so a 429
		// here is the scaffold's default and nothing else.
		addr, stop := serveRendered(t, bin, "api", "--addr", "127.0.0.1:0")
		base := "http://" + addr

		status, body := httpGet(t, base+"/healthz")
		assert.Equal(t, http.StatusOK, status, "health routes are on: %s", body)

		status, body = httpPost(t, base+"/v1/commands/hello", strings.Repeat("x", 1<<20+1))
		assert.Equal(t, http.StatusRequestEntityTooLarge, status, string(body))
		assert.Contains(t, string(body), "body_too_large")

		// The read tier holds 60 tokens and refills 10 a second, so a
		// tight loop drains it well inside the bound.
		limited := false
		for i := 0; i < 400 && !limited; i++ {
			resp, err := http.Get(base + "/v1/commands/hello") //nolint:gosec // loopback test server
			require.NoError(t, err)
			out, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			require.NoError(t, err)
			if resp.StatusCode == http.StatusTooManyRequests {
				limited = true
				assert.Contains(t, string(out), "rate_limited")
				assert.NotEmpty(t, resp.Header.Get("Retry-After"))
			}
		}
		assert.True(t, limited, "the rate limit is on for a loopback bind")

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

		// AGENTS.md documents the socket's refusal envelope and its
		// wire codes. Assert the two an agent is most likely to meet
		// on this tree, plus the rule that an unknown flag is NOT a
		// wire error but an exit code inside a successful envelope.
		var wire struct {
			Ok    bool `json:"ok"`
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			Result *struct {
				ExitCode int `json:"exit_code"`
			} `json:"result"`
		}
		ask := func(req map[string]any) {
			t.Helper()
			c, err := net.Dial("unix", sock)
			require.NoError(t, err)
			defer func() { _ = c.Close() }()
			require.NoError(t, json.NewEncoder(c).Encode(req))
			wire.Ok, wire.Error, wire.Result = false, nil, nil
			require.NoError(t, json.NewDecoder(c).Decode(&wire))
		}

		// A withheld destructive command is BLOCKED, not NOT_FOUND:
		// the fragment tells an agent to read that as "ask a human".
		ask(map[string]any{"path": []string{"nuke"}})
		require.False(t, wire.Ok)
		require.NotNil(t, wire.Error)
		assert.Equal(t, "BLOCKED", wire.Error.Code)

		// serve is self-hosting, and over the socket that reads as
		// NOT_FOUND rather than NOT_INVOCABLE.
		ask(map[string]any{"path": []string{"serve"}})
		require.False(t, wire.Ok)
		require.NotNil(t, wire.Error)
		assert.Equal(t, "NOT_FOUND", wire.Error.Code)

		// An empty path is INVALID, the one code an agent can fix and
		// retry.
		ask(map[string]any{"path": []string{}})
		require.False(t, wire.Ok)
		require.NotNil(t, wire.Error)
		assert.Equal(t, "INVALID", wire.Error.Code)

		// An unknown flag rides inside a successful envelope with a
		// usage exit code, so an agent must check exit_code and not
		// only ok.
		ask(map[string]any{"path": []string{"hello"}, "flags": map[string]any{"nosuchflag": "x"}})
		require.True(t, wire.Ok, "an unknown flag is not a wire error")
		require.NotNil(t, wire.Result)
		assert.Equal(t, 2, wire.Result.ExitCode)
	})
}
