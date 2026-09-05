package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
)

// Every test here goes through Root.Execute — the path that installs
// the confirmation and policy gates — with the arguments an operator
// would type. A Root built with cli.New alone has no gates, so nothing
// below calls a service's handler directly.

// safeBuffer is a goroutine-safe writer for the serve command's stderr,
// which the supervisor's logger writes from its own goroutines.
type safeBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// serveRun is one background `served serve ...` invocation.
type serveRun struct {
	root     *cli.Root
	stderr   *safeBuffer
	stdout   *safeBuffer
	ready    chan serve.EventPayload
	supReady chan struct{}
	errCh    chan error
	cancel   context.CancelFunc
}

// startServe runs `served serve <args>` in the background with a bus
// wired, so readiness is observed the way a subscriber would observe
// it: on kit.serve.service.ready_reported, with the address in the
// payload.
func startServe(t *testing.T, opts options, args ...string) *serveRun {
	t.Helper()
	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	opts.bus = b

	run := &serveRun{
		stderr:   &safeBuffer{},
		stdout:   &safeBuffer{},
		ready:    make(chan serve.EventPayload, 16),
		supReady: make(chan struct{}, 1),
		errCh:    make(chan error, 1),
	}
	b.Subscribe("kit.serve.service.ready_reported", func(_ context.Context, e bus.Event) error {
		if p, ok := e.Payload.(serve.EventPayload); ok {
			run.ready <- p
		}
		return nil
	})
	b.Subscribe("kit.serve.supervisor.ready_reported", func(context.Context, bus.Event) error {
		select {
		case run.supReady <- struct{}{}:
		default:
		}
		return nil
	})

	run.root = newRoot(opts)
	run.root.Cmd.SetErr(run.stderr)
	run.root.Cmd.SetOut(run.stdout)

	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	run.root.SetArgs(append([]string{"serve"}, args...))
	go func() { run.errCh <- run.root.Execute(ctx) }()
	t.Cleanup(func() { _ = run.stop(t) })
	return run
}

// waitReady blocks until service reports ready and returns its payload.
// An empty service returns the next readiness of any service.
func (r *serveRun) waitReady(t *testing.T, service string) serve.EventPayload {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case p := <-r.ready:
			if service == "" || p.Service == service {
				return p
			}
		case err := <-r.errCh:
			r.errCh <- err
			t.Fatalf("serve returned before %q reported ready: %v\n%s", service, err, r.stderr.String())
		case <-deadline:
			t.Fatalf("%q never reported ready\n%s", service, r.stderr.String())
		}
	}
}

// waitSupervisorReady blocks until the aggregate readiness event fires.
func (r *serveRun) waitSupervisorReady(t *testing.T) {
	t.Helper()
	select {
	case <-r.supReady:
	case <-time.After(15 * time.Second):
		t.Fatalf("the supervisor never reported ready\n%s", r.stderr.String())
	}
}

// stop cancels the run and returns what Execute returned.
func (r *serveRun) stop(t *testing.T) error {
	t.Helper()
	r.cancel()
	select {
	case err := <-r.errCh:
		r.errCh <- err
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after cancellation")
		return nil
	}
}

// runToCompletion executes the root with args and returns its error,
// canceling after settle so a supervisor form comes back.
func runToCompletion(t *testing.T, root *cli.Root, args []string, settle time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root.SetArgs(args)
	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute(ctx) }()
	select {
	case err := <-errCh:
		return err
	case <-time.After(settle):
		cancel()
	}
	select {
	case err := <-errCh:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after cancellation")
		return nil
	}
}

func findServe(t *testing.T, root *cli.Root) *cobra.Command {
	t.Helper()
	for _, c := range root.Cmd.Commands() {
		if c.Name() == "serve" {
			return c
		}
	}
	t.Fatal("no serve command on the root")
	return nil
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sv")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// --- HTTP and socket clients -------------------------------------------------

func httpDo(t *testing.T, method, url, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, out
}

type restResult struct {
	ExitCode int             `json:"exit_code"`
	Stdout   string          `json:"stdout"`
	Stderr   string          `json:"stderr"`
	Data     json.RawMessage `json:"data"`
}

func decodeResult(t *testing.T, body []byte) restResult {
	t.Helper()
	var res restResult
	require.NoError(t, json.Unmarshal(body, &res), string(body))
	return res
}

type discovery struct {
	Tool     string `json:"tool"`
	Commands []struct {
		Name      string `json:"name"`
		Invocable bool   `json:"invocable"`
		Reason    string `json:"reason"`
	} `json:"commands"`
}

type verdict struct {
	Invocable bool
	Reason    string
}

func discover(t *testing.T, base string) map[string]verdict {
	t.Helper()
	status, body := httpDo(t, http.MethodGet, base+"/v1/commands", "")
	require.Equal(t, http.StatusOK, status, string(body))
	var doc discovery
	require.NoError(t, json.Unmarshal(body, &doc))
	assert.Equal(t, "served", doc.Tool)
	out := map[string]verdict{}
	for _, c := range doc.Commands {
		out[c.Name] = verdict{c.Invocable, c.Reason}
	}
	return out
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

func items(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var rows []Item
	require.NoError(t, json.Unmarshal(raw, &rows), string(raw))
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}
	return names
}

// --- The serve hierarchy -----------------------------------------------------

func TestServeExistsWithContractFlagsAndChildren(t *testing.T) {
	root := newRoot(options{})
	serveCmd := findServe(t, root)

	for _, flag := range []string{
		"list", "enable", "disable", "ready-timeout", "stop-timeout", "shutdown-timeout",
	} {
		assert.NotNil(t, serveCmd.Flags().Lookup(flag), "contract flag --%s", flag)
	}
	// The api service's own flags reach the parent (the documented
	// supervisor-form exception); --no-auth appears only with Auth.
	addr := serveCmd.Flags().Lookup("addr")
	require.NotNil(t, addr, "--addr")
	assert.Equal(t, cli.DefaultAPIAddr, addr.DefValue, "the api service defaults to loopback")
	assert.Equal(t, "127.0.0.1:8080", cli.DefaultAPIAddr)
	assert.NotNil(t, serveCmd.Flags().Lookup("insecure-remote"), "--insecure-remote")
	assert.Nil(t, serveCmd.Flags().Lookup("no-auth"), "--no-auth is mounted only when Auth is set")
	assert.NotNil(t, serveCmd.Flags().Lookup("socket"), "--socket")

	// The children, in registration order: kit's two, then the adopter's.
	require.NotNil(t, root.ServeRegistry())
	assert.Equal(t, []string{"api", "socket", "heartbeat"}, root.ServeRegistry().Names())
}

func TestServeListNamesEveryService(t *testing.T) {
	root := newRoot(options{})
	var out safeBuffer
	root.Cmd.SetOut(&out)
	require.NoError(t, runToCompletion(t, root, []string{"serve", "--list"}, 5*time.Second))

	got := out.String()
	assert.Regexp(t, `(?m)^api\s`, got)
	assert.Regexp(t, `(?m)^socket\s`, got)
	assert.Regexp(t, `(?m)^heartbeat\s`, got)
	assert.Less(t, strings.Index(got, "api"), strings.Index(got, "socket"))
	assert.Less(t, strings.Index(got, "socket"), strings.Index(got, "heartbeat"),
		"the listing mirrors registration order")
}

// --- Readiness -----------------------------------------------------------------

func TestReadinessReachesTheBusAndTheLog(t *testing.T) {
	run := startServe(t, options{}, "api", "--addr", "127.0.0.1:0")
	p := run.waitReady(t, "api")
	run.waitSupervisorReady(t)

	// The bus payload carries the resolved address: for ":0" it is the
	// only place the bound port is knowable.
	host, port, err := net.SplitHostPort(p.Address)
	require.NoError(t, err, p.Address)
	assert.Equal(t, "127.0.0.1", host)
	assert.NotEqual(t, "0", port)

	// The log counterpart carries the same address under a structured
	// key; nothing prints "Listening on".
	log := run.stderr.String()
	assert.Contains(t, log, "ready_reported")
	assert.Contains(t, log, "service=api")
	assert.Contains(t, log, "address="+p.Address)
	assert.NotContains(t, log, "Listening on")

	assert.NoError(t, run.stop(t), "a signal-initiated stop is a clean stop")
	assert.Contains(t, run.stderr.String(), "stopped")
}

// --- Discovery -----------------------------------------------------------------

func TestDiscoveryDescribesEveryCommandWithItsReason(t *testing.T) {
	run := startServe(t, options{}, "api", "--addr", "127.0.0.1:0")
	base := "http://" + run.waitReady(t, "api").Address
	got := discover(t, base)

	want := map[string]verdict{
		"item list":  {Invocable: true},
		"item add":   {Invocable: true},
		"item purge": {Reason: "unauthorized-destructive"},
		"shell":      {Reason: "interactive"},
		"upgrade":    {Reason: "self-hosting"},
		"serve":      {Reason: "self-hosting"},
		"status":     {Reason: "management-only"},
	}
	for name, v := range want {
		require.Contains(t, got, name, "%s must be described", name)
		assert.Equal(t, v, got[name], name)
	}

	// Nothing else is invocable: what the fixture did not declare
	// callable is not a route.
	for name, v := range got {
		if v.Invocable {
			assert.Contains(t, []string{"item list", "item add"}, name,
				"unexpected invocable command %q", name)
		}
	}
}

// --- Execution over REST -------------------------------------------------------

func TestReadAndWriteRunOverREST(t *testing.T) {
	run := startServe(t, options{}, "api", "--addr", "127.0.0.1:0")
	base := "http://" + run.waitReady(t, "api").Address

	// A read is a GET and, because the command declares a schema,
	// answers in data with stdout empty.
	status, body := httpDo(t, http.MethodGet, base+"/v1/commands/item/list", "")
	require.Equal(t, http.StatusOK, status, string(body))
	res := decodeResult(t, body)
	assert.Equal(t, 0, res.ExitCode)
	assert.Empty(t, res.Stdout)
	assert.Equal(t, []string{"bolt", "nut"}, items(t, res.Data))

	// A write is a POST with args in the body.
	status, body = httpDo(t, http.MethodPost, base+"/v1/commands/item/add", `{"args":["washer"]}`)
	require.Equal(t, http.StatusOK, status, string(body))
	res = decodeResult(t, body)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "added washer\n", res.Stdout)

	status, body = httpDo(t, http.MethodGet, base+"/v1/commands/item/list", "")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, []string{"bolt", "nut", "washer"}, items(t, decodeResult(t, body).Data),
		"the write reached the same state the read reports")
}

func TestDestructiveIsWithheldOverRESTByDefault(t *testing.T) {
	run := startServe(t, options{}, "api", "--addr", "127.0.0.1:0")
	base := "http://" + run.waitReady(t, "api").Address

	// Withheld at mount: no route, and discovery says why.
	status, body := httpDo(t, http.MethodPost, base+"/v1/commands/item/purge", `{}`)
	assert.Equal(t, http.StatusNotFound, status, string(body))
	assert.Equal(t, verdict{Reason: "unauthorized-destructive"}, discover(t, base)["item purge"])
}

func TestDestructiveRunsOverRESTOnceNamedAndConfirmed(t *testing.T) {
	run := startServe(t,
		options{allowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST}},
		"api", "--addr", "127.0.0.1:0")
	base := "http://" + run.waitReady(t, "api").Address

	assert.True(t, discover(t, base)["item purge"].Invocable,
		"naming the surface lifts the transport's ceiling")

	// The command's own confirmation gate still applies, and there is
	// no TTY behind a request: refused by the command, exit 5, 403.
	status, body := httpDo(t, http.MethodPost, base+"/v1/commands/item/purge", `{}`)
	assert.Equal(t, http.StatusForbidden, status, string(body))
	res := decodeResult(t, body)
	assert.Equal(t, output.UnauthorizedError("").ExitCode, res.ExitCode)
	assert.Contains(t, res.Stderr, "--confirm")

	// Confirmed, it runs.
	status, body = httpDo(t, http.MethodPost, base+"/v1/commands/item/purge", `{"flags":{"confirm":"yes"}}`)
	require.Equal(t, http.StatusOK, status, string(body))
	res = decodeResult(t, body)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "purged 2 items\n", res.Stdout)
}

func TestInteractiveAndSelfHostingAreNeverInvocableOverREST(t *testing.T) {
	// Even with every destructive ceiling lifted, these classes stay out.
	run := startServe(t,
		options{allowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST}},
		"api", "--addr", "127.0.0.1:0")
	base := "http://" + run.waitReady(t, "api").Address

	for _, path := range []string{"shell", "upgrade", "serve"} {
		status, body := httpDo(t, http.MethodPost, base+"/v1/commands/"+path, `{}`)
		assert.Equal(t, http.StatusNotFound, status, "%s: %s", path, body)
	}
	got := discover(t, base)
	assert.Equal(t, verdict{Reason: "interactive"}, got["shell"])
	assert.Equal(t, verdict{Reason: "self-hosting"}, got["upgrade"])
	assert.Equal(t, verdict{Reason: "self-hosting"}, got["serve"])
}

// --- Execution over the socket -------------------------------------------------

func TestReadAndWriteRunOverTheSocket(t *testing.T) {
	path := shortSocketPath(t)
	run := startServe(t, options{}, "socket", "--socket", path)
	p := run.waitReady(t, "socket")
	assert.Equal(t, path, p.Address, "readiness carries the resolved socket path")

	resp := callSocket(t, path, socket.Request{Path: []string{"item", "list"}})
	require.True(t, resp.Ok, "%+v", resp.Error)
	require.NotNil(t, resp.Result)
	assert.Equal(t, 0, resp.Result.ExitCode)
	raw, err := json.Marshal(resp.Result.Data)
	require.NoError(t, err)
	assert.Equal(t, []string{"bolt", "nut"}, items(t, raw))

	resp = callSocket(t, path, socket.Request{Path: []string{"item", "add"}, Args: []string{"washer"}})
	require.True(t, resp.Ok, "%+v", resp.Error)
	assert.Equal(t, 0, resp.Result.ExitCode)
	assert.Equal(t, "added washer\n", resp.Result.Stdout)
}

func TestDestructiveIsRefusedOverTheSocketByDefault(t *testing.T) {
	path := shortSocketPath(t)
	run := startServe(t, options{}, "socket", "--socket", path)
	run.waitReady(t, "socket")

	resp := callSocket(t, path, socket.Request{Path: []string{"item", "purge"}})
	require.False(t, resp.Ok)
	require.NotNil(t, resp.Error)
	assert.Equal(t, socket.CodeBlocked, resp.Error.Code)
}

func TestDestructiveRunsOverTheSocketOnceNamedAndConfirmed(t *testing.T) {
	path := shortSocketPath(t)
	run := startServe(t,
		options{allowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceRPC}},
		"socket", "--socket", path)
	run.waitReady(t, "socket")

	resp := callSocket(t, path, socket.Request{Path: []string{"item", "purge"}})
	require.True(t, resp.Ok, "the bridge no longer blocks it: %+v", resp.Error)
	require.NotNil(t, resp.Result)
	assert.Equal(t, output.UnauthorizedError("").ExitCode, resp.Result.ExitCode)
	assert.Contains(t, resp.Result.Stderr, "--confirm")

	resp = callSocket(t, path, socket.Request{
		Path: []string{"item", "purge"}, Flags: map[string]any{"confirm": "yes"},
	})
	require.True(t, resp.Ok)
	assert.Equal(t, 0, resp.Result.ExitCode)
	assert.Equal(t, "purged 2 items\n", resp.Result.Stdout)
}

func TestInteractiveAndSelfHostingAreNeverInvocableOverTheSocket(t *testing.T) {
	path := shortSocketPath(t)
	run := startServe(t,
		options{allowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceRPC}},
		"socket", "--socket", path)
	run.waitReady(t, "socket")

	// Interactive: the socket's bridge admits it as a leaf, then the
	// invocability gate refuses it by name.
	resp := callSocket(t, path, socket.Request{Path: []string{"shell"}})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeNotInvocable, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "interactive")

	// Self-hosting: never a leaf, so the answer is "no such command".
	for _, path2 := range [][]string{{"upgrade"}, {"serve"}} {
		resp = callSocket(t, path, socket.Request{Path: path2})
		require.False(t, resp.Ok, "%v", path2)
		assert.Equal(t, socket.CodeNotFound, resp.Error.Code, "%v", path2)
	}
}

// --- Exposure ------------------------------------------------------------------

func TestUnauthenticatedRemoteServingIsRefused(t *testing.T) {
	root := newRoot(options{})
	err := runToCompletion(t, root, []string{"serve", "api", "--addr", "0.0.0.0:0"}, 5*time.Second)
	require.Error(t, err)

	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, output.CodeUsage, kitErr.Code)
	assert.Equal(t, 2, kitErr.ExitCode, "refused at the configuration gate")
	assert.Contains(t, kitErr.Message, `"0.0.0.0:0" is not a loopback address`)
	for _, remedy := range []string{"APIConfig.Auth", "127.0.0.1", "services.api.insecure_remote"} {
		assert.Contains(t, kitErr.Message, remedy, "the message names every remedy")
	}
}

func TestUnboundedRemoteServingIsRefused(t *testing.T) {
	// --insecure-remote answers who may call. What any caller may run
	// is still unanswered, so the surface stays refused, and the
	// message names its own remedies rather than repeating the
	// authentication ones.
	root := newRoot(options{})
	err := runToCompletion(t, root,
		[]string{"serve", "api", "--addr", "0.0.0.0:0", "--insecure-remote"},
		5*time.Second)
	require.Error(t, err)

	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, output.CodeUsage, kitErr.Code)
	assert.Equal(t, 2, kitErr.ExitCode, "refused at the configuration gate")
	assert.Contains(t, kitErr.Message, "no delegation policy is configured")
	for _, remedy := range []string{"--policy", "127.0.0.1", "services.api.insecure_no_policy"} {
		assert.Contains(t, kitErr.Message, remedy, "the message names every remedy")
	}
}

func TestInsecureRemoteOptInIsHonoredByName(t *testing.T) {
	// Both opt-ins: exposure beyond loopback needs an answer to who
	// may call AND to what any caller may run.
	run := startServe(t, options{}, "api", "--addr", "0.0.0.0:0",
		"--insecure-remote", "--insecure-no-policy")
	p := run.waitReady(t, "api")
	_, port, err := net.SplitHostPort(p.Address)
	require.NoError(t, err)

	status, _ := httpDo(t, http.MethodGet, "http://127.0.0.1:"+port+"/v1/commands/item/list", "")
	assert.Equal(t, http.StatusOK, status)
}

// --- Adopter services ----------------------------------------------------------

func TestAdopterServiceStartsUnderTheSupervisor(t *testing.T) {
	// The selector form starts it even though it is not enabled.
	hb := newHeartbeat()
	run := startServe(t, options{heartbeat: hb}, "heartbeat")
	run.waitReady(t, "heartbeat")
	assert.True(t, hb.wasStarted())
	assert.True(t, hb.Ready())
	require.NoError(t, run.stop(t))
	assert.False(t, hb.Ready(), "Stop ran in the ordered shutdown")

	// The supervisor form starts it beside the api once enabled, and
	// reports aggregate readiness only when both are ready.
	hb = newHeartbeat()
	run = startServe(t, options{heartbeat: hb}, "--enable", "heartbeat", "--addr", "127.0.0.1:0")
	seen := map[string]bool{}
	for len(seen) < 2 {
		p := run.waitReady(t, "")
		seen[p.Service] = true
	}
	assert.Equal(t, map[string]bool{"api": true, "heartbeat": true}, seen)
	run.waitSupervisorReady(t)
	assert.True(t, hb.wasStarted())
}
