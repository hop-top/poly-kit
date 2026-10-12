package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/console/output"
)

// isolateRuntime points the control socket at a fresh, short runtime
// directory so concurrent test binaries never meet one another's.
func isolateRuntime(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kr")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	return dir
}

// child returns parent's direct child named name, or nil.
func child(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// run executes args through Execute and returns stdout, stderr, the
// exit code (0 on success) and the error.
func run(t *testing.T, r *Root, args ...string) (string, string, int, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	r.SetArgs(args)
	err := r.Execute(context.Background())
	code := 0
	if err != nil {
		code = 1
		var ce *output.Error
		if errors.As(err, &ce) {
			code = ce.ExitCode
		}
	}
	return stdout.String(), stderr.String(), code, err
}

func TestService_GroupShape(t *testing.T) {
	r := authRoot(t, WithAPI(APIConfig{}), WithStatus(StatusConfig{}))

	svc := child(r.Cmd, ServiceCommandName)
	require.NotNil(t, svc)
	assert.Same(t, r.ServiceCommand(), svc)
	for _, leaf := range []string{"start", "list", "stop", "restart", "status"} {
		assert.NotNil(t, child(svc, leaf), "service %s", leaf)
	}
	assert.Same(t, r.ServeCommand(), child(svc, "start"))
	assert.Equal(t, "start [service]", r.ServeCommand().Use)
	assert.True(t, r.IsReserved(ServiceCommandName))

	// Root status stays the tool's status, beside service status.
	assert.NotNil(t, child(r.Cmd, "status"))

	// The deprecated alias is hidden and the only root `serve`.
	alias := child(r.Cmd, "serve")
	require.NotNil(t, alias)
	assert.True(t, alias.Hidden)
	assert.Same(t, r.serveAlias, alias)
}

func TestService_NoRegistryNoGroup(t *testing.T) {
	r := New(Config{Name: "tool", Version: "1.0.0", DisableValidate: true})
	assert.Nil(t, r.ServiceCommand())
	assert.Nil(t, r.ServeCommand())
	assert.Nil(t, child(r.Cmd, ServiceCommandName))
	assert.Nil(t, child(r.Cmd, "serve"))
}

func TestService_FlagsLandOnStartAndAreSharedWithAlias(t *testing.T) {
	r := authRoot(t, WithAPI(APIConfig{}), WithSocket(SocketConfig{}))
	start := r.ServeCommand()
	for _, name := range []string{"addr", "socket", insecureRemoteFlag, insecureNoPolicyFlag,
		"enable", "disable", "ready-timeout", "stop-timeout", "shutdown-timeout"} {
		f := start.Flags().Lookup(name)
		require.NotNil(t, f, "--%s on service start", name)
		assert.Same(t, f, r.serveAlias.Flags().Lookup(name), "--%s shared with serve", name)
	}
	assert.Nil(t, start.Flags().Lookup("list"), "service list replaces --list")
	assert.NotNil(t, r.serveAlias.Flags().Lookup("list"), "the alias keeps --list")
}

func TestService_FlagsRegisteredAfterNewReachTheAlias(t *testing.T) {
	r := authRoot(t, WithAPI(APIConfig{}))
	r.ServeCommand().Flags().String("late", "", "registered after New")
	require.NoError(t, r.Prepare())
	assert.NotNil(t, r.serveAlias.Flags().Lookup("late"))
}

func TestService_WithheldFromServedSurfaces(t *testing.T) {
	r := authRoot(t, WithAPI(APIConfig{}))

	// Without the reserved lookup only their own mark withholds them:
	// neither start, stop nor restart is named serve.
	tree := cmdreflect.Reflect(r.Cmd)
	for _, path := range []string{"service start", "service stop", "service restart", "serve"} {
		d := tree.Lookup(path)
		require.NotNil(t, d, path)
		assert.True(t, d.Surface.SelfHosting, "%s is self-hosting", path)
		assert.False(t, d.Invocable, path)
	}

	base, stop := serveAPI(t, r)
	defer stop()
	entries := listing(t, base, nil)
	for _, name := range []string{"service start", "service stop", "service restart", "service status", "service list"} {
		e, ok := entries[name]
		require.True(t, ok, "%s listed with a reason", name)
		assert.False(t, e.Invocable, name)
	}
	for _, leaf := range []string{"stop", "restart"} {
		resp, body := postJSON(t, base+"/v1/commands/service/"+leaf, `{}`, nil)
		assert.NotEqual(t, http.StatusOK, resp.StatusCode, string(body))
	}
}

func TestService_PolicyOnStartReachesServedCalls(t *testing.T) {
	strict := policy.Policy{
		Name:  "strict",
		Allow: map[policy.SideEffect][]string{policy.SideEffectWrite: {}},
	}
	for name, args := range map[string][]string{
		"service start": nil,
		"serve alias":   {"serve"},
	} {
		t.Run(name, func(t *testing.T) {
			r := authRoot(t,
				WithAPI(APIConfig{Addr: "127.0.0.1:0"}),
				WithPolicy(func(string) (policy.Policy, error) { return strict, nil }),
			)
			var base string
			var stop func()
			if args == nil {
				// Starting is exempt from the policy it names; a
				// policy refusing writes would otherwise refuse the
				// start itself (exit 5).
				base, stop = serveAPI(t, r, "--policy", "strict")
			} else {
				base, stop = serveAt(t, r, []string{"serve", "api", "--policy", "strict"})
			}
			defer stop()

			resp, body := get(t, base+"/v1/commands/list", nil)
			assert.Equal(t, http.StatusOK, resp.StatusCode, string(body))
			// The write the policy refuses everyone is refused: not
			// mounted at all, and listed with the reason.
			resp, body = postJSON(t, base+"/v1/commands/add", `{}`, nil)
			assert.NotEqual(t, http.StatusOK, resp.StatusCode, string(body))
			assert.NotContains(t, string(body), "added")
			add, ok := listing(t, base, nil)["add"]
			require.True(t, ok)
			assert.False(t, add.Invocable)
			assert.NotEmpty(t, add.Reason)
		})
	}
}

func TestService_PolicyExemptionIsByIdentity(t *testing.T) {
	r := authRoot(t, WithAPI(APIConfig{}))
	assert.True(t, r.startsServices(r.ServeCommand()))
	assert.True(t, r.startsServices(r.serveAlias))
	for _, leaf := range []string{"list", "stop", "status"} {
		assert.False(t, r.startsServices(child(r.ServiceCommand(), leaf)), leaf)
	}

	// No registry: an adopter's own root serve keeps the exemption.
	bare := New(Config{Name: "tool", Version: "1.0.0", DisableValidate: true})
	own := &cobra.Command{Use: "serve", Short: "s", RunE: func(*cobra.Command, []string) error { return nil }}
	bare.Cmd.AddCommand(own)
	assert.True(t, bare.startsServices(own))
}

func TestService_ValidatesAndAcceptsAdopterLeaves(t *testing.T) {
	isolateRuntime(t)
	r := New(Config{Name: "tool", Version: "1.0.0", Short: "tool", EnforceValidate: true},
		WithAPI(APIConfig{}), WithStatus(StatusConfig{}))
	ran := false
	setup := &cobra.Command{
		Use:   "setup",
		Short: "Install the service unit",
		Long:  "Install the service unit for the current user.",
		RunE:  func(*cobra.Command, []string) error { ran = true; return nil },
	}
	SetSideEffect(setup, SideEffectWriteLocal)
	SetIdempotency(setup, IdempotencyYes)
	r.ServiceCommand().AddCommand(setup)

	require.NoError(t, r.Validate(), "kit's service group plus an adopter leaf validates")
	_, _, code, err := run(t, r, "service", "setup")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.True(t, ran)

	// The adopter's leaf is a management command like kit's.
	tree := cmdreflect.Reflect(r.Cmd, cmdreflect.WithReserved(r))
	d := tree.Lookup("service setup")
	require.NotNil(t, d)
	assert.True(t, d.Surface.Reserved)
	assert.False(t, d.Invocable)
}

func TestService_UnknownLeafIsUsageError(t *testing.T) {
	r := authRoot(t, WithAPI(APIConfig{}))
	_, _, code, err := run(t, r, "service", "nosuch")
	require.Error(t, err)
	assert.Equal(t, 2, code)
}

func TestService_ListReplacesServeList(t *testing.T) {
	r := authRoot(t, WithAPI(APIConfig{}))
	out, _, code, err := run(t, r, "service", "list", "--format", "json")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	var rows []serveListRow
	require.NoError(t, json.Unmarshal([]byte(out), &rows), out)
	require.Len(t, rows, 1)
	assert.Equal(t, APIServiceName, rows[0].Service)

	// The deprecated spelling still lists, with a notice on stderr only.
	r = authRoot(t, WithAPI(APIConfig{}))
	aliasOut, aliasErr, code, err := run(t, r, "serve", "--list", "--format", "json")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.JSONEq(t, out, aliasOut, "same listing, stdout untouched by the notice")
	assert.Contains(t, aliasErr, "DEPRECATION")
	assert.Contains(t, aliasErr, "tool service start")
}

func TestService_AliasRunsServeWithNotice(t *testing.T) {
	r := authRoot(t, WithAPI(APIConfig{}))
	out, stderr, code, err := run(t, r, "serve", "--dry-run", "--format", "json")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "service:api", "the alias resolves the same run")
	assert.Contains(t, stderr, "DEPRECATION")
}

func TestService_StatusAndStopWithoutAnInstance(t *testing.T) {
	isolateRuntime(t)
	r := authRoot(t, WithAPI(APIConfig{}))
	_, _, code, err := run(t, r, "service", "status")
	require.Error(t, err)
	assert.Equal(t, output.ExitNotFound, code, "not running is NOT_FOUND")
	assert.Contains(t, err.Error(), "tool is not running")

	r = authRoot(t, WithAPI(APIConfig{}))
	out, _, code, err := run(t, r, "service", "stop", "--format", "json")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.JSONEq(t, `{"result":"not_running"}`, out)
}

func TestService_StaleSocketIsNotRunningAndReplaced(t *testing.T) {
	dir := isolateRuntime(t)
	path := filepath.Join(dir, "tool", controlSocketName)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())
	_, statErr := os.Stat(path)
	require.NoError(t, statErr, "a stale socket file is left behind")

	r := authRoot(t, WithAPI(APIConfig{}))
	_, _, code, _ := run(t, r, "service", "status")
	assert.Equal(t, output.ExitNotFound, code)

	// A new instance takes the path over and answers on it.
	d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	_, stop := serveAPI(t, d)
	defer stop()
	rows := instanceStatusRows(t)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].Ready)
}

// instanceStatusRows runs `service status --format json` on a fresh root.
func instanceStatusRows(t *testing.T) []serviceStatusRow {
	t.Helper()
	r := authRoot(t, WithAPI(APIConfig{}))
	out, _, code, err := run(t, r, "service", "status", "--format", "json")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	var rows []serviceStatusRow
	require.NoError(t, json.Unmarshal([]byte(out), &rows), out)
	return rows
}

func TestService_StatusAndStopReachTheRunningInstance(t *testing.T) {
	isolateRuntime(t)
	d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.SetArgs([]string{"service", "start", "api"})
	errCh := make(chan error, 1)
	go func() { errCh <- d.Execute(ctx) }()
	a := apiSvc(t, d)
	require.Eventually(t, func() bool { return a.Ready() && a.Addr() != "" },
		10*time.Second, 10*time.Millisecond)

	rows := instanceStatusRows(t)
	require.Len(t, rows, 1)
	assert.Equal(t, APIServiceName, rows[0].Service)
	assert.True(t, rows[0].Ready)
	assert.Equal(t, a.Addr(), rows[0].Addr)
	assert.Equal(t, os.Getpid(), rows[0].PID)

	// stop drains the run, which returns cleanly, and reports it.
	c := authRoot(t, WithAPI(APIConfig{}))
	out, _, code, err := run(t, c, "service", "stop", "--format", "json")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.JSONEq(t, `{"result":"stopped"}`, out)
	select {
	case err := <-errCh:
		assert.NoError(t, err, "a stop request is a clean shutdown")
	case <-time.After(5 * time.Second):
		t.Fatal("the instance did not stop")
	}
	assert.False(t, a.Ready())

	// Gone means not running, and the socket is unlinked.
	s := authRoot(t, WithAPI(APIConfig{}))
	_, _, code, _ = run(t, s, "service", "status")
	assert.Equal(t, output.ExitNotFound, code)
	path, err := controlSocketPath(s)
	require.NoError(t, err)
	_, statErr := os.Stat(path)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "control socket unlinked")
}

func TestService_SecondInstanceKeepsTheFirstsControlSocket(t *testing.T) {
	isolateRuntime(t)
	first := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	_, stopFirst := serveAPI(t, first)
	defer stopFirst()

	second := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	stderr := &lockedStringWriter{b: &strings.Builder{}}
	second.Cmd.SetErr(stderr)
	_, stopSecond := serveAPI(t, second)
	defer stopSecond()
	assert.Contains(t, stderr.String(), "service control socket unavailable",
		"the second instance serves, and says it has no control channel")

	rows := instanceStatusRows(t)
	require.Len(t, rows, 1)
	assert.Equal(t, apiSvc(t, first).Addr(), rows[0].Addr, "status still reaches the first")
}

func TestService_ControlSocketIsOwnerOnly(t *testing.T) {
	isolateRuntime(t)
	d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	_, stop := serveAPI(t, d)
	defer stop()
	path, err := controlSocketPath(d)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// serveAt starts the api service through args and returns its base URL
// and a stop func.
func serveAt(t *testing.T, r *Root, args []string) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.SetArgs(args)
	errCh := make(chan error, 1)
	go func() { errCh <- r.Execute(ctx) }()

	a := apiSvc(t, r)
	deadline := time.Now().Add(10 * time.Second)
	for !a.Ready() || a.Addr() == "" {
		select {
		case err := <-errCh:
			cancel()
			t.Fatalf("serve returned before the api was ready: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("api never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return "http://" + a.Addr(), func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("serve did not return after cancellation")
		}
	}
}

// lockedStringWriter serializes writes from the supervisor's goroutines.
type lockedStringWriter struct {
	mu sync.Mutex
	b  *strings.Builder
}

func (l *lockedStringWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedStringWriter) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
