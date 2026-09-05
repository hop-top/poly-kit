package cli

import (
	"context"
	"encoding/json"
	"errors"
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

	"hop.top/kit/go/console/cli/policy"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
)

// auditRecorder is the recording sink the tests read the audit trail
// from.
type auditRecorder struct {
	mu   sync.Mutex
	invs []cmdsurface.Invocation
	res  []cmdsurface.Result
	errs []error
}

func (a *auditRecorder) Emit(_ context.Context, inv cmdsurface.Invocation, res cmdsurface.Result, err error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.invs = append(a.invs, inv)
	a.res = append(a.res, res)
	a.errs = append(a.errs, err)
	return nil
}

func (a *auditRecorder) spec() cmdsurface.SinkSpec {
	return cmdsurface.SinkSpec{Sink: a, OnOK: true, OnError: true}
}

// last returns the most recent record, failing when there is none.
func (a *auditRecorder) last(t *testing.T) (cmdsurface.Invocation, cmdsurface.Result, error) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	require.NotEmpty(t, a.invs, "expected an audit record")
	n := len(a.invs) - 1
	return a.invs[n], a.res[n], a.errs[n]
}

func (a *auditRecorder) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.invs)
}

// blockingRunner records the ctx it ran under and blocks until that
// ctx is canceled, so a test can prove a disconnect reached it.
type blockingRunner struct {
	seen   chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	ctxErr error
}

func newBlockingRunner() *blockingRunner {
	return &blockingRunner{seen: make(chan struct{}, 1), done: make(chan struct{}, 1)}
}

func (b *blockingRunner) Run(ctx context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
	select {
	case b.seen <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
	}
	b.mu.Lock()
	b.ctxErr = ctx.Err()
	b.mu.Unlock()
	select {
	case b.done <- struct{}{}:
	default:
	}
	return cmdsurface.Result{}, nil
}

func (b *blockingRunner) Stream(context.Context, cmdsurface.Invocation, chan<- cmdsurface.Event) error {
	return errors.New("not streamed")
}

// authRoot builds a root with one command per rule under test.
func authRoot(t *testing.T, opts ...func(*Root)) *Root {
	t.Helper()
	r := New(Config{Name: "tool", Version: "1.0.0", DisableValidate: true}, opts...)
	r.Cmd.SetErr(io.Discard)

	r.Cmd.AddCommand(&cobra.Command{
		Use:         "list",
		Short:       "list things",
		RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("listed"); return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	})
	r.Cmd.AddCommand(&cobra.Command{
		Use:         "add",
		Short:       "add a thing",
		RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("added"); return nil },
		Annotations: map[string]string{"kit/side-effect": "write"},
	})
	r.Cmd.AddCommand(&cobra.Command{
		Use:   "purge",
		Short: "purge the store",
		RunE:  func(cmd *cobra.Command, _ []string) error { cmd.Print("purged"); return nil },
		Annotations: map[string]string{
			"kit/side-effect":           "destructive-shared",
			"kit/requires-confirmation": "true",
		},
	})
	admin := &cobra.Command{Use: "admin", Short: "admin things"}
	admin.AddCommand(&cobra.Command{
		Use:   "reset",
		Short: "reset state",
		RunE:  func(cmd *cobra.Command, _ []string) error { cmd.Print("reset"); return nil },
		Annotations: map[string]string{
			"kit/side-effect": "write-shared",
			"kit/permissions": "admin",
		},
	})
	r.Cmd.AddCommand(admin)
	return r
}

// runServeExpect executes args and returns the error, canceling once the
// run has settled so a serving form returns.
func runServeExpect(t *testing.T, r *Root, args []string, settle time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.SetArgs(args)
	errCh := make(chan error, 1)
	go func() { errCh <- r.Execute(ctx) }()
	select {
	case err := <-errCh:
		return err
	case <-time.After(settle):
		cancel()
	}
	select {
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after cancellation")
		return nil
	}
}

// apiSvc returns the registered api service.
func apiSvc(t *testing.T, r *Root) *apiService {
	t.Helper()
	svc, ok := r.serveReg.Lookup(APIServiceName)
	require.True(t, ok)
	a, ok := svc.(*apiService)
	require.True(t, ok)
	return a
}

// serveAPI starts the api service through the real serve path and
// returns its base URL and a stop func.
func serveAPI(t *testing.T, r *Root, args ...string) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.SetArgs(append([]string{"serve", "api"}, args...))
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

// serveSocket starts the socket service through the real serve path.
func serveSocket(t *testing.T, r *Root, path string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.SetArgs([]string{"serve", "socket"})
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
		time.Sleep(10 * time.Millisecond)
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

func tmpSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ca")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func socketCall(t *testing.T, path string, req socket.Request) socket.Response {
	t.Helper()
	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	require.NoError(t, json.NewEncoder(conn).Encode(req))
	var resp socket.Response
	require.NoError(t, json.NewDecoder(conn).Decode(&resp))
	return resp
}

func get(t *testing.T, url string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, body
}

func postJSON(t *testing.T, url, body string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, b
}

func usageErr(t *testing.T, err error) *output.Error {
	t.Helper()
	require.Error(t, err)
	var oe *output.Error
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, output.CodeUsage, oe.Code)
	assert.Equal(t, 2, oe.ExitCode)
	return oe
}

// bearer is an AuthFunc that accepts "Bearer alice" and "Bearer bob".
func bearer(scopes map[string][]string) api.AuthFunc {
	return func(r *http.Request) (any, error) {
		who := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if who == "" || who == r.Header.Get("Authorization") {
			return nil, errors.New("missing bearer token")
		}
		return api.Claims{Subject: who, Tenant: "acme", Scopes: scopes[who]}, nil
	}
}

// scopeGate is the permission gate the guide documents: every scope a
// leaf declares under kit/permissions must be among the caller's.
func scopeGate(_ context.Context, meta cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
	have := map[string]bool{}
	for _, s := range strings.Split(meta.Extra["scopes"], ",") {
		have[s] = true
	}
	for _, need := range leaf.Class.Permissions {
		if !have[need] {
			return cmdsurface.PermissionDecision{Reason: "missing scope " + need}
		}
	}
	return cmdsurface.PermissionDecision{Allowed: true}
}

// --- Loopback default and the exposure gate ------------------------

func TestAPIDefaultAddrIsLoopback(t *testing.T) {
	r := authRoot(t, WithAPI(APIConfig{}))
	assert.Equal(t, "127.0.0.1:8080", DefaultAPIAddr)
	assert.Equal(t, DefaultAPIAddr, r.apiCfg.Addr)
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "serve" {
			assert.Equal(t, DefaultAPIAddr, c.Flags().Lookup("addr").DefValue,
				"--addr defaults to the loopback address")
			require.NotNil(t, c.Flags().Lookup(insecureRemoteFlag),
				"--insecure-remote is registered beside --addr")
		}
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8080":  true,
		"127.0.0.1:0":     true,
		"127.1.2.3:80":    true,
		"[::1]:8080":      true,
		"[::1%lo0]:8080":  true,
		"localhost:8080":  true,
		"LOCALHOST:8080":  true,
		":8080":           false,
		":0":              false,
		"0.0.0.0:8080":    false,
		"[::]:8080":       false,
		"10.0.0.5:8080":   false,
		"example.com:443": false,
		"not-an-address":  false,
	} {
		assert.Equal(t, want, isLoopbackAddr(addr), addr)
	}
}

const refusalNoAuth = `service "api": addr: "0.0.0.0:0" is not a loopback address and the api service has no authentication; ` +
	`set APIConfig.Auth, listen on 127.0.0.1, or set services.api.insecure_remote: true (or --insecure-remote) to serve unauthenticated beyond loopback`

func TestNonLoopbackWithoutAuthIsRefusedAtValidate(t *testing.T) {
	r := authRoot(t, WithAPI(APIConfig{Addr: "0.0.0.0:0"}))

	err := runServeExpect(t, r, []string{"serve", "api"}, 2*time.Second)
	oe := usageErr(t, err)
	assert.Equal(t, refusalNoAuth, oe.Message)
	assert.Empty(t, apiSvc(t, r).Addr(), "nothing may bind when validation refuses")

	// The supervisor form validates the same way, and a bare port is
	// every interface.
	r = authRoot(t, WithAPI(APIConfig{Addr: ":0"}))
	oe = usageErr(t, runServeExpect(t, r, []string{"serve"}, 2*time.Second))
	assert.Contains(t, oe.Message, `addr: ":0" is not a loopback address`)

	// --addr on the command line is refused the same way: the flag
	// cannot widen what the config could not.
	r = authRoot(t, WithAPI(APIConfig{}))
	oe = usageErr(t, runServeExpect(t, r, []string{"serve", "api", "--addr", "0.0.0.0:0"}, 2*time.Second))
	assert.Equal(t, refusalNoAuth, oe.Message)
}

func TestLoopbackWithoutAuthServes(t *testing.T) {
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	base, stop := serveAPI(t, r)
	defer stop()
	resp, body := get(t, base+"/v1/commands/list", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, string(body))
}

func TestNoAuthCannotWidenExposure(t *testing.T) {
	// --no-auth on a non-loopback address is refused, with a message
	// that names the flag as the cause.
	r := authRoot(t, WithAPI(APIConfig{Addr: "0.0.0.0:0", Auth: bearer(nil)}))
	oe := usageErr(t, runServeExpect(t, r, []string{"serve", "api", "--no-auth"}, 2*time.Second))
	assert.Equal(t,
		`service "api": addr: "0.0.0.0:0" is not a loopback address and --no-auth disables authentication; `+
			`drop --no-auth, listen on 127.0.0.1, or set services.api.insecure_remote: true (or --insecure-remote) to serve unauthenticated beyond loopback`,
		oe.Message)

	// --no-auth on loopback keeps working as it always did.
	r = authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0", Auth: bearer(nil)}))
	base, stop := serveAPI(t, r, "--no-auth")
	resp, _ := get(t, base+"/v1/commands/list", nil)
	stop()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "no auth on loopback is permitted")

	// --no-auth beyond loopback needs the opt-in, and then it serves.
	r = authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0", Auth: bearer(nil)}))
	base, stop = serveAPI(t, r, "--no-auth", "--addr", "0.0.0.0:0", "--insecure-remote")
	resp, _ = get(t, strings.Replace(base, "0.0.0.0", "127.0.0.1", 1)+"/v1/commands/list", nil)
	stop()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAuthPermitsAnyAddress(t *testing.T) {
	// A tool that already sets Auth keeps working on any address.
	r := authRoot(t, WithAPI(APIConfig{Addr: "0.0.0.0:0", Auth: bearer(nil)}))
	base, stop := serveAPI(t, r)
	defer stop()
	resp, _ := get(t, strings.Replace(base, "0.0.0.0", "127.0.0.1", 1)+"/v1/commands/list",
		map[string]string{"Authorization": "Bearer alice"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestInsecureRemoteOptIn(t *testing.T) {
	// Each of the three sources permits an unauthenticated
	// non-loopback listener.
	cases := map[string]func(*Root) []string{
		"flag": func(*Root) []string { return []string{"--insecure-remote"} },
		"config key": func(r *Root) []string {
			r.Viper.Set("services.api.insecure_remote", true)
			return nil
		},
		"APIConfig": func(r *Root) []string {
			r.apiCfg.InsecureRemote = true
			return nil
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			r := authRoot(t, WithAPI(APIConfig{Addr: "0.0.0.0:0"}))
			args := arrange(r)
			base, stop := serveAPI(t, r, args...)
			defer stop()
			resp, _ := get(t, strings.Replace(base, "0.0.0.0", "127.0.0.1", 1)+"/v1/commands/list", nil)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}

	t.Run("config key false overrides the code default", func(t *testing.T) {
		r := authRoot(t, WithAPI(APIConfig{Addr: "0.0.0.0:0", InsecureRemote: true}))
		r.Viper.Set("services.api.insecure_remote", false)
		oe := usageErr(t, runServeExpect(t, r, []string{"serve", "api"}, 2*time.Second))
		assert.Equal(t, refusalNoAuth, oe.Message)
	})
	t.Run("flag overrides a config key", func(t *testing.T) {
		r := authRoot(t, WithAPI(APIConfig{Addr: "0.0.0.0:0"}))
		r.Viper.Set("services.api.insecure_remote", false)
		_, stop := serveAPI(t, r, "--insecure-remote")
		stop()
	})
}

// --- Context propagation ---------------------------------------------

func TestRESTClaimsAndHeadersReachMetaAndAudit(t *testing.T) {
	rec := &auditRecorder{}
	r := authRoot(
		t,
		WithAPI(APIConfig{Addr: "127.0.0.1:0", Auth: bearer(map[string][]string{"alice": {"admin"}})}),
		WithAuditSinks(rec.spec()),
	)
	base, stop := serveAPI(t, r)
	defer stop()

	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	resp, body := get(t, base+"/v1/commands/list", map[string]string{
		"Authorization":   "Bearer alice",
		"X-Request-ID":    "req-42",
		"Traceparent":     traceparent,
		"Idempotency-Key": "idem-42",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	assert.Equal(t, "req-42", resp.Header.Get("X-Request-ID"), "the request id is echoed")

	inv, res, err := rec.last(t)
	assert.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, []string{"list"}, inv.Path)
	assert.Equal(t, cmdsurface.SurfaceREST, inv.Meta.Surface)
	assert.Equal(t, "alice", inv.Meta.Caller, "principal from the claims")
	assert.Equal(t, "acme", inv.Meta.Tenant, "tenant from the claims")
	assert.Equal(t, "req-42", inv.Meta.RequestID)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", inv.Meta.TraceID)
	assert.Equal(t, "idem-42", inv.Meta.IdempotencyKey)
	assert.Equal(t, "admin", inv.Meta.Extra["scopes"])
	assert.NotEmpty(t, inv.Meta.Extra["remote_addr"])
	assert.False(t, inv.Meta.RequestedAt.IsZero())

	// A request id is issued when the caller sends none.
	resp, _ = get(t, base+"/v1/commands/list", map[string]string{"Authorization": "Bearer alice"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	inv, _, _ = rec.last(t)
	assert.NotEmpty(t, inv.Meta.RequestID)
	assert.Equal(t, resp.Header.Get("X-Request-ID"), inv.Meta.RequestID)

	// An unauthenticated call is refused before the command, and the
	// refusal is in the same trail with the same handles.
	before := rec.count()
	resp, body = get(t, base+"/v1/commands/list", map[string]string{"X-Request-ID": "req-43", "X-Trace-ID": "t-43"})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, string(body))
	assert.Equal(t, before+1, rec.count(), "the auth refusal is audited")
	inv, _, err = rec.last(t)
	assert.ErrorIs(t, err, cmdsurface.ErrAuthRefused)
	assert.Contains(t, err.Error(), "missing bearer token")
	assert.Equal(t, []string{"list"}, inv.Path, "the addressed command is recorded")
	assert.Equal(t, "req-43", inv.Meta.RequestID)
	assert.Equal(t, "t-43", inv.Meta.TraceID)
	assert.Equal(t, cmdsurface.SurfaceREST, inv.Meta.Surface)
}

func TestSocketProvenanceReachesMetaAndAudit(t *testing.T) {
	rec := &auditRecorder{}
	path := tmpSocket(t)
	r := authRoot(t, WithSocket(SocketConfig{Path: path}), WithAuditSinks(rec.spec()))
	stop := serveSocket(t, r, path)
	defer stop()

	resp := socketCall(t, path, socket.Request{
		Path: []string{"list"}, Caller: "daemon", Tenant: "acme",
		RequestID: "req-s", TraceID: "trace-s", IdempotencyKey: "idem-s",
	})
	require.True(t, resp.Ok, "%+v", resp.Error)

	inv, res, err := rec.last(t)
	assert.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, cmdsurface.SurfaceRPC, inv.Meta.Surface)
	assert.Equal(t, "daemon", inv.Meta.Caller, "claimed, recorded as provenance")
	assert.Equal(t, "acme", inv.Meta.Tenant)
	assert.Equal(t, "req-s", inv.Meta.RequestID)
	assert.Equal(t, "trace-s", inv.Meta.TraceID)
	assert.Equal(t, "idem-s", inv.Meta.IdempotencyKey)
}

func TestSocketAuthenticatorRefusalIsAudited(t *testing.T) {
	rec := &auditRecorder{}
	path := tmpSocket(t)
	r := authRoot(t, WithSocket(SocketConfig{
		Path: path,
		Auth: func(_ context.Context, _ net.Conn, req socket.Request) (socket.Identity, error) {
			if req.Flags["token"] != "ok" {
				return socket.Identity{}, errors.New("bad token")
			}
			return socket.Identity{Principal: "alice", Tenant: "acme"}, nil
		},
	}), WithAuditSinks(rec.spec()))
	stop := serveSocket(t, r, path)
	defer stop()

	resp := socketCall(t, path, socket.Request{Path: []string{"list"}, Caller: "mallory", RequestID: "req-a"})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeUnauthenticated, resp.Error.Code)
	inv, _, err := rec.last(t)
	assert.ErrorIs(t, err, cmdsurface.ErrAuthRefused)
	assert.Equal(t, "req-a", inv.Meta.RequestID)
	assert.Equal(t, cmdsurface.SurfaceRPC, inv.Meta.Surface)

	resp = socketCall(t, path, socket.Request{Path: []string{"list"}, Caller: "mallory", Flags: map[string]any{"token": "ok"}})
	require.True(t, resp.Ok, "%+v", resp.Error)
	inv, _, _ = rec.last(t)
	assert.Equal(t, "alice", inv.Meta.Caller, "the verified identity replaces the claim")
}

// --- Central permission enforcement ------------------------------------

func TestRESTPermissionDeniedIs403AndDiscoveryUnchanged(t *testing.T) {
	rec := &auditRecorder{}
	r := authRoot(
		t,
		WithAPI(APIConfig{Addr: "127.0.0.1:0", Auth: bearer(map[string][]string{
			"alice": {"admin"}, "bob": {"widgets:read"},
		})}),
		WithPermission(scopeGate),
		WithAuditSinks(rec.spec()),
	)
	base, stop := serveAPI(t, r)
	defer stop()

	// bob lacks the scope admin reset declares: 403 with the reason.
	resp, body := postJSON(t, base+"/v1/commands/admin/reset", `{}`, map[string]string{"Authorization": "Bearer bob"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, string(body))
	var ae api.APIError
	require.NoError(t, json.Unmarshal(body, &ae))
	assert.Equal(t, api.CodePermissionDenied, ae.Code)
	assert.Equal(t,
		"api: permission denied: cmdsurface: permission denied: admin reset on rest: missing scope admin",
		ae.Message)
	inv, _, err := rec.last(t)
	assert.ErrorIs(t, err, cmdsurface.ErrPermissionDenied)
	assert.Equal(t, "bob", inv.Meta.Caller)
	assert.Equal(t, []string{"admin", "reset"}, inv.Path)

	// alice has it: the command runs.
	resp, body = postJSON(t, base+"/v1/commands/admin/reset", `{}`, map[string]string{"Authorization": "Bearer alice"})
	assert.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	assert.Contains(t, string(body), "reset")

	// A caller-specific verdict cannot be pre-computed: discovery
	// keeps listing the command as invocable, for bob as for alice.
	for _, who := range []string{"alice", "bob"} {
		resp, body = get(t, base+"/v1/commands", map[string]string{"Authorization": "Bearer " + who})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var doc api.DiscoveryDocument
		require.NoError(t, json.Unmarshal(body, &doc))
		for _, e := range doc.Commands {
			if e.Name == "admin reset" {
				assert.True(t, e.Invocable, "%s: caller-specific denial must not hide the command", who)
				assert.Empty(t, e.Reason)
			}
		}
	}

	// The gate is not a REST rule: a command without kit/permissions
	// is untouched.
	resp, _ = get(t, base+"/v1/commands/list", map[string]string{"Authorization": "Bearer bob"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestCallerIndependentDenialIsReflectedInDiscovery(t *testing.T) {
	r := authRoot(
		t,
		WithAPI(APIConfig{Addr: "127.0.0.1:0"}),
		WithPermission(func(_ context.Context, _ cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
			if strings.HasPrefix(leaf.PathKey(), "admin ") {
				return cmdsurface.PermissionDecision{Reason: "admin commands are closed here", CallerIndependent: true}
			}
			return cmdsurface.PermissionDecision{Allowed: true}
		}),
	)
	base, stop := serveAPI(t, r)
	defer stop()

	_, body := get(t, base+"/v1/commands", nil)
	var doc api.DiscoveryDocument
	require.NoError(t, json.Unmarshal(body, &doc))
	var found bool
	for _, e := range doc.Commands {
		if e.Name == "admin reset" {
			found = true
			assert.False(t, e.Invocable)
			assert.Equal(t, cmdsurface.ReasonPermissionDenied, e.Reason)
			assert.Empty(t, e.Route, "a withheld command advertises no route")
		}
	}
	require.True(t, found)
	assert.Contains(t, doc.Reasons, "permission-denied")

	// Not mounted, exactly like an interactive or withheld command:
	// the route is absent, and the reason is discovery's to give.
	resp, _ := postJSON(t, base+"/v1/commands/admin/reset", `{}`, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPolicyEngineDeniesForEveryoneOnEverySurface(t *testing.T) {
	strict := policy.Policy{Allow: map[policy.SideEffect][]string{policy.SideEffectWrite: {}}}
	loader := func(name string) (policy.Policy, error) {
		if name != "strict" {
			return policy.Policy{}, errors.New("unknown policy " + name)
		}
		return strict, nil
	}
	path := tmpSocket(t)
	rec := &auditRecorder{}
	r := authRoot(
		t,
		WithAPI(APIConfig{Addr: "127.0.0.1:0"}),
		WithSocket(SocketConfig{Path: path}),
		WithPolicy(loader),
		WithAuditSinks(rec.spec()),
	)
	base, stop := serveAPI(t, r, "--policy", "strict")
	defer stop()

	// REST: the write command is withheld at mount for everyone, with
	// the bridge's reason; the read command is untouched.
	_, body := get(t, base+"/v1/commands", nil)
	var doc api.DiscoveryDocument
	require.NoError(t, json.Unmarshal(body, &doc))
	entries := map[string]api.DiscoveryEntry{}
	for _, e := range doc.Commands {
		entries[e.Name] = e
	}
	assert.False(t, entries["add"].Invocable)
	assert.Equal(t, cmdsurface.ReasonPermissionDenied, entries["add"].Reason)
	assert.True(t, entries["list"].Invocable)
	resp, _ := get(t, base+"/v1/commands/list", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestPolicyEngineDeniesOnTheSocket(t *testing.T) {
	strict := policy.Policy{Allow: map[policy.SideEffect][]string{policy.SideEffectWrite: {}}}
	path := tmpSocket(t)
	rec := &auditRecorder{}
	r := authRoot(
		t,
		WithSocket(SocketConfig{Path: path}),
		WithPolicy(func(string) (policy.Policy, error) { return strict, nil }),
		WithAuditSinks(rec.spec()),
	)
	ctx, cancel := context.WithCancel(context.Background())
	r.SetArgs([]string{"serve", "socket", "--policy", "strict"})
	errCh := make(chan error, 1)
	go func() { errCh <- r.Execute(ctx) }()
	defer func() {
		cancel()
		<-errCh
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if conn, err := net.Dial("unix", path); err == nil {
			_ = conn.Close()
			break
		}
		require.False(t, time.Now().After(deadline), "socket never came up")
		time.Sleep(10 * time.Millisecond)
	}

	resp := socketCall(t, path, socket.Request{Path: []string{"add"}, Caller: "anyone"})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeDenied, resp.Error.Code)
	assert.Equal(t,
		"cmdsurface: permission denied: add on rpc: policy: write not allowed for add",
		resp.Error.Message)
	inv, _, err := rec.last(t)
	assert.ErrorIs(t, err, cmdsurface.ErrPermissionDenied)
	assert.Equal(t, "anyone", inv.Meta.Caller)

	resp = socketCall(t, path, socket.Request{Path: []string{"list"}})
	require.True(t, resp.Ok, "a read command is outside the refused class")
}

func TestPolicyWithoutLoaderIsAConfigError(t *testing.T) {
	// The serve command is itself policy-gated, so a --policy no
	// loader can resolve is refused before any service validates:
	// exit 2, naming the missing wiring.
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	oe := usageErr(t, runServeExpect(t, r, []string{"serve", "api", "--policy", "strict"}, 2*time.Second))
	assert.Contains(t, oe.Message, "--policy is set but no policy loader is wired")
	assert.Empty(t, apiSvc(t, r).Addr(), "nothing may bind")
}

func TestConfirmationRefusalIsAudited(t *testing.T) {
	rec := &auditRecorder{}
	r := authRoot(
		t,
		WithAPI(APIConfig{
			Addr:   "127.0.0.1:0",
			Policy: cmdsurface.Policy{AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST}},
		}),
		WithAuditSinks(rec.spec()),
	)
	base, stop := serveAPI(t, r)
	defer stop()

	// The confirmation gate is the command's own: the refusal is an
	// exit code, and the audit record carries it as the verdict.
	resp, body := postJSON(t, base+"/v1/commands/purge", `{}`, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Contains(t, string(body), "--confirm=no (or non-TTY default)")
	inv, res, err := rec.last(t)
	assert.NoError(t, err, "the bridge did not refuse; the command did")
	assert.Equal(t, output.UnauthorizedError("").ExitCode, res.ExitCode)
	assert.Equal(t, []string{"purge"}, inv.Path)

	resp, body = postJSON(t, base+"/v1/commands/purge", `{"flags":{"confirm":"yes"}}`, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	_, res, _ = rec.last(t)
	assert.Equal(t, 0, res.ExitCode)
}

// --- Cancellation ---------------------------------------------------------

func TestRESTClientDisconnectCancelsInvocation(t *testing.T) {
	runner := newBlockingRunner()
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	r.serveAuth.bridgeOpts = []cmdsurface.Option{cmdsurface.WithRunner(runner)}
	base, stop := serveAPI(t, r)
	defer stop()

	conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	require.NoError(t, err)
	_, err = io.WriteString(conn, "GET /v1/commands/list HTTP/1.1\r\nHost: localhost\r\n\r\n")
	require.NoError(t, err)

	select {
	case <-runner.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner never received the invocation")
	}
	require.NoError(t, conn.Close())

	select {
	case <-runner.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the invocation context was not canceled after the client disconnected")
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	assert.ErrorIs(t, runner.ctxErr, context.Canceled)
}

func TestSocketClientDisconnectCancelsInvocation(t *testing.T) {
	runner := newBlockingRunner()
	path := tmpSocket(t)
	r := authRoot(t, WithSocket(SocketConfig{Path: path}))
	r.serveAuth.bridgeOpts = []cmdsurface.Option{cmdsurface.WithRunner(runner)}
	stop := serveSocket(t, r, path)
	defer stop()

	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(conn).Encode(socket.Request{Path: []string{"list"}}))

	select {
	case <-runner.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner never received the invocation")
	}
	require.NoError(t, conn.Close())

	select {
	case <-runner.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the invocation context was not canceled after the client disconnected")
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	assert.ErrorIs(t, runner.ctxErr, context.Canceled)
}

// --- Invocability -------------------------------------------------------

func TestInteractiveCommandRefusedCentrallyOnBothTransports(t *testing.T) {
	shell := func() *cobra.Command {
		return &cobra.Command{
			Use:         "shell",
			Short:       "interactive shell",
			RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("never"); return nil },
			Annotations: map[string]string{"kit/side-effect": "interactive"},
		}
	}

	// Socket: the whole tree is exposed, so the leaf is reachable and
	// the bridge's gate answers with its own code and the reason.
	rec := &auditRecorder{}
	path := tmpSocket(t)
	r := authRoot(t, WithSocket(SocketConfig{Path: path}), WithAuditSinks(rec.spec()))
	r.Cmd.AddCommand(shell())
	stop := serveSocket(t, r, path)
	resp := socketCall(t, path, socket.Request{Path: []string{"shell"}, RequestID: "req-i"})
	stop()
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeNotInvocable, resp.Error.Code)
	assert.Equal(t,
		"cmdsurface: command not invocable through a runner: shell on rpc is interactive (interactive: requires a terminal and a human)",
		resp.Error.Message)
	inv, _, err := rec.last(t)
	assert.ErrorIs(t, err, cmdsurface.ErrNotInvocable)
	assert.Equal(t, "req-i", inv.Meta.RequestID)

	// REST: withheld at mount, so the route is absent and discovery
	// carries the reason.
	r = authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	r.Cmd.AddCommand(shell())
	base, stop := serveAPI(t, r)
	defer stop()
	resp2, _ := postJSON(t, base+"/v1/commands/shell", `{}`, nil)
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
	_, body := get(t, base+"/v1/commands", nil)
	var doc api.DiscoveryDocument
	require.NoError(t, json.Unmarshal(body, &doc))
	var found bool
	for _, e := range doc.Commands {
		if e.Name == "shell" {
			found = true
			assert.False(t, e.Invocable)
			assert.Equal(t, "interactive", e.Reason)
		}
	}
	require.True(t, found)
}
