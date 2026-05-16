package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
)

// cloudrunFakeRunner is the Cloud Run adapter's programmable Runner.
// It mirrors recordingRunner / wsFakeRunner but lives in this file to
// avoid colliding with sibling _test packages.
type cloudrunFakeRunner struct {
	mu             sync.Mutex
	lastInvocation Invocation

	RunFn    func(ctx context.Context, inv Invocation) (Result, error)
	StreamFn func(ctx context.Context, inv Invocation, out chan<- Event) error
}

func (r *cloudrunFakeRunner) Run(ctx context.Context, inv Invocation) (Result, error) {
	r.mu.Lock()
	r.lastInvocation = inv
	fn := r.RunFn
	r.mu.Unlock()
	if fn != nil {
		return fn(ctx, inv)
	}
	return Result{Stdout: "ok"}, nil
}

func (r *cloudrunFakeRunner) Stream(ctx context.Context, inv Invocation, out chan<- Event) error {
	r.mu.Lock()
	r.lastInvocation = inv
	fn := r.StreamFn
	r.mu.Unlock()
	defer close(out)
	if fn != nil {
		return fn(ctx, inv, out)
	}
	out <- Event{Kind: "stdout", Data: "ok", At: time.Now()}
	out <- Event{Kind: "done", Data: &Result{Stdout: "ok"}, At: time.Now()}
	return nil
}

// cloudrunTestTree builds the canonical cobra tree shared by the
// Cloud Run adapter tests:
//
//	root
//	├── ping        (read; safe on every default surface)
//	└── slow        (read; used by graceful-shutdown tests via
//	                Handler hook — see cloudrunBlockingHandler)
func cloudrunTestTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use:         "ping",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	})
	root.AddCommand(&cobra.Command{
		Use:         "slow",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	})
	return root
}

// cloudrunNewBridge builds a Bridge whose default-enabled set
// contains every Wave-1/2/3 surface the adapter mounts by default.
// Individual tests narrow the set via Hide / Expose as needed.
func cloudrunNewBridge(t *testing.T, runner Runner) *Bridge {
	t.Helper()
	policy := DefaultPolicy()
	policy.DefaultEnabled = []Surface{
		SurfaceCLI, SurfaceLib,
		SurfaceREST, SurfaceSSE, SurfaceMCP, SurfaceWS,
	}
	return New(cloudrunTestTree(), WithRunner(runner), WithPolicy(policy))
}

// cloudrunReady is a synchronization helper. OnReady closes ch with
// the bound address so tests can wait for the listener and then
// dial it.
type cloudrunReady struct {
	ch   chan string
	once sync.Once
}

func newCloudrunReady() *cloudrunReady { return &cloudrunReady{ch: make(chan string, 1)} }

func (r *cloudrunReady) onReady(addr string) {
	r.once.Do(func() { r.ch <- addr })
}

func (r *cloudrunReady) wait(t *testing.T) string {
	t.Helper()
	select {
	case addr := <-r.ch:
		return addr
	case <-time.After(5 * time.Second):
		t.Fatal("OnReady not called within 5s")
		return ""
	}
}

// cloudrunRun launches runCloudRunCtx in a goroutine with port=0 so
// the OS assigns a free port. Returns the bound address, the run
// goroutine's error channel, and a cancel function that triggers
// graceful shutdown.
func cloudrunRun(t *testing.T, b *Bridge, cfg CloudRunConfig) (addr string, runErr <-chan error, cancel context.CancelFunc) {
	t.Helper()
	ready := newCloudrunReady()
	cfg.OnReady = chainOnReady(cfg.OnReady, ready.onReady)
	if cfg.Port == 0 {
		// Force the resolver to pick an OS-assigned port. resolveCloudRunPort
		// would otherwise consult $PORT or fall back to 8080.
		cfg.Port = pickFreePort(t)
	}

	ctx, cancelFn := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- runCloudRunCtx(ctx, b, cfg) }()

	addr = ready.wait(t)
	return addr, errCh, cancelFn
}

// chainOnReady composes two OnReady callbacks; either may be nil.
func chainOnReady(a, b func(string)) func(string) {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	default:
		return func(s string) {
			a(s)
			b(s)
		}
	}
}

// pickFreePort asks the OS for a port, closes the listener, returns
// the port. There's a TOCTOU window between close and re-bind, but
// it's small enough that the alternative (parsing OnReady) is not
// worth the extra plumbing for tests that need to know the port
// ahead of time.
func pickFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// httpURL turns "127.0.0.1:PORT" into "http://127.0.0.1:PORT".
func httpURL(addr string) string { return "http://" + addr }

// --- tests ---

func TestCloudRunResolvePort_CfgWins(t *testing.T) {
	got, err := resolveCloudRunPort(9090, "8080")
	if err != nil {
		t.Fatalf("resolveCloudRunPort: %v", err)
	}
	if got != 9090 {
		t.Errorf("port=%d want 9090", got)
	}
}

func TestCloudRunResolvePort_EnvWhenCfgZero(t *testing.T) {
	got, err := resolveCloudRunPort(0, "7777")
	if err != nil {
		t.Fatalf("resolveCloudRunPort: %v", err)
	}
	if got != 7777 {
		t.Errorf("port=%d want 7777", got)
	}
}

func TestCloudRunResolvePort_Fallback(t *testing.T) {
	got, err := resolveCloudRunPort(0, "")
	if err != nil {
		t.Fatalf("resolveCloudRunPort: %v", err)
	}
	if got != 8080 {
		t.Errorf("port=%d want 8080", got)
	}
}

func TestCloudRunResolvePort_InvalidEnv(t *testing.T) {
	if _, err := resolveCloudRunPort(0, "notanumber"); err == nil {
		t.Fatal("resolveCloudRunPort: want error, got nil")
	}
}

func TestCloudRun_InvalidEnvPort(t *testing.T) {
	t.Setenv("PORT", "notanumber")
	b := cloudrunNewBridge(t, &cloudrunFakeRunner{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := runCloudRunCtx(ctx, b, CloudRunConfig{}); err == nil {
		t.Fatal("runCloudRunCtx: want error, got nil")
	}
}

func TestCloudRun_NilBridge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := runCloudRunCtx(ctx, nil, CloudRunConfig{Port: 1}); err == nil {
		t.Fatal("runCloudRunCtx(nil bridge): want error, got nil")
	}
}

func TestCloudRun_RESTSurfaceMounts(t *testing.T) {
	runner := &cloudrunFakeRunner{
		RunFn: func(_ context.Context, _ Invocation) (Result, error) {
			return Result{Stdout: "pong"}, nil
		},
	}
	b := cloudrunNewBridge(t, runner)

	addr, errCh, cancel := cloudrunRun(t, b, CloudRunConfig{
		Surfaces: CloudRunSurfaces{REST: true},
	})
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("run err: %v", err)
		}
	}()

	resp, err := http.Post(httpURL(addr)+"/cmd/ping", "application/json",
		strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var got Result
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Stdout != "pong" {
		t.Errorf("Stdout=%q want pong", got.Stdout)
	}
}

func TestCloudRun_SSESurfaceMounts(t *testing.T) {
	b := cloudrunNewBridge(t, &cloudrunFakeRunner{})

	addr, errCh, cancel := cloudrunRun(t, b, CloudRunConfig{
		Surfaces: CloudRunSurfaces{SSE: true},
	})
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("run err: %v", err)
		}
	}()

	resp, err := http.Get(httpURL(addr) + "/cmd/ping/stream")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type=%q want text/event-stream", ct)
	}
}

func TestCloudRun_MCPSurfaceMounts(t *testing.T) {
	b := cloudrunNewBridge(t, &cloudrunFakeRunner{})

	addr, errCh, cancel := cloudrunRun(t, b, CloudRunConfig{
		Surfaces: CloudRunSurfaces{MCP: true},
	})
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("run err: %v", err)
		}
	}()

	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	resp, err := http.Post(httpURL(addr)+"/mcp", "application/json", body)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var env struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The bridge's default MCP set includes ping; tools/list must
	// have at least one entry once the surface is mounted.
	if len(env.Result.Tools) == 0 {
		t.Errorf("tools list empty; want >= 1")
	}
}

func TestCloudRun_WSSurfaceMounts(t *testing.T) {
	b := cloudrunNewBridge(t, &cloudrunFakeRunner{})

	addr, errCh, cancel := cloudrunRun(t, b, CloudRunConfig{
		Surfaces: CloudRunSurfaces{WS: true},
	})
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("run err: %v", err)
		}
	}()

	ctx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	wsURL := "ws://" + addr + "/ws/cmd"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = c.Close(websocket.StatusNormalClosure, "done")
}

func TestCloudRun_NoSurfaces_404OnCmd(t *testing.T) {
	b := cloudrunNewBridge(t, &cloudrunFakeRunner{})

	addr, errCh, cancel := cloudrunRun(t, b, CloudRunConfig{})
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("run err: %v", err)
		}
	}()

	resp, err := http.Get(httpURL(addr) + "/cmd/ping")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d want 404", resp.StatusCode)
	}
}

func TestCloudRun_OnReadyFires(t *testing.T) {
	b := cloudrunNewBridge(t, &cloudrunFakeRunner{})

	var got string
	var mu sync.Mutex
	addr, errCh, cancel := cloudrunRun(t, b, CloudRunConfig{
		OnReady: func(a string) {
			mu.Lock()
			got = a
			mu.Unlock()
		},
	})
	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("run err: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got == "" || got != addr {
		t.Errorf("OnReady addr=%q want %q", got, addr)
	}
}

func TestCloudRun_OnShutdownFires(t *testing.T) {
	b := cloudrunNewBridge(t, &cloudrunFakeRunner{})

	var fired bool
	var mu sync.Mutex
	_, errCh, cancel := cloudrunRun(t, b, CloudRunConfig{
		OnShutdown: func() {
			mu.Lock()
			fired = true
			mu.Unlock()
		},
	})
	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("run err: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !fired {
		t.Error("OnShutdown did not fire")
	}
}

func TestCloudRun_GracefulShutdown_WaitsForInflight(t *testing.T) {
	// Pre-built router so we can mount a handler that blocks long
	// enough to assert "Shutdown waited" but short enough to finish
	// inside the grace window.
	router := api.NewRouter()
	requestStarted := make(chan struct{}, 1)
	requestFinished := make(chan struct{}, 1)
	router.Handle(http.MethodGet, "/slow", func(w http.ResponseWriter, _ *http.Request) {
		requestStarted <- struct{}{}
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("done"))
		requestFinished <- struct{}{}
	})

	b := cloudrunNewBridge(t, &cloudrunFakeRunner{})

	addr, errCh, cancel := cloudrunRun(t, b, CloudRunConfig{
		Router:        router,
		ShutdownGrace: 2 * time.Second,
	})

	// Fire the slow request, wait until the handler is inside the
	// sleep, then trigger shutdown.
	respCh := make(chan *http.Response, 1)
	errReqCh := make(chan error, 1)
	go func() {
		resp, err := http.Get(httpURL(addr) + "/slow")
		if err != nil {
			errReqCh <- err
			return
		}
		respCh <- resp
	}()

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never started")
	}

	cancel() // trigger shutdown

	// The in-flight request must complete cleanly.
	select {
	case resp := <-respCh:
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status=%d want 200; body=%s", resp.StatusCode, body)
		}
	case err := <-errReqCh:
		t.Fatalf("request error: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("request did not complete")
	}

	// The handler must have reached its finished marker before
	// Shutdown returned.
	select {
	case <-requestFinished:
	case <-time.After(1 * time.Second):
		t.Error("handler did not finish before Shutdown")
	}

	if err := <-errCh; err != nil {
		t.Errorf("run err: %v", err)
	}
}

func TestCloudRun_GracefulShutdown_GraceExceeded(t *testing.T) {
	// Block the handler past the grace window: Shutdown should
	// return context.DeadlineExceeded.
	router := api.NewRouter()
	requestStarted := make(chan struct{}, 1)
	hold := make(chan struct{})
	router.Handle(http.MethodGet, "/hang", func(w http.ResponseWriter, r *http.Request) {
		requestStarted <- struct{}{}
		select {
		case <-hold:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	})
	defer close(hold)

	b := cloudrunNewBridge(t, &cloudrunFakeRunner{})

	addr, errCh, cancel := cloudrunRun(t, b, CloudRunConfig{
		Router:        router,
		ShutdownGrace: 100 * time.Millisecond,
	})

	go func() {
		resp, err := http.Get(httpURL(addr) + "/hang")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never started")
	}

	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err=%v want context.DeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runCloudRunCtx did not return")
	}
}

func TestCloudRun_PreBuiltRouter_Reachable(t *testing.T) {
	router := api.NewRouter()
	router.Handle(http.MethodGet, "/custom", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "hello")
	})

	b := cloudrunNewBridge(t, &cloudrunFakeRunner{})

	addr, errCh, cancel := cloudrunRun(t, b, CloudRunConfig{Router: router})
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("run err: %v", err)
		}
	}()

	resp, err := http.Get(httpURL(addr) + "/custom")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		t.Errorf("body=%q want hello", body)
	}
}

func TestCloudRun_DefaultRouter_AppliesMiddleware(t *testing.T) {
	// When Router is nil, the adapter wires RequestID + Recovery.
	// We assert RequestID's effect: every response carries the
	// X-Request-ID header.
	b := cloudrunNewBridge(t, &cloudrunFakeRunner{})

	addr, errCh, cancel := cloudrunRun(t, b, CloudRunConfig{
		Surfaces: CloudRunSurfaces{REST: true},
	})
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("run err: %v", err)
		}
	}()

	resp, err := http.Post(httpURL(addr)+"/cmd/ping", "application/json",
		strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if id := resp.Header.Get("X-Request-ID"); id == "" {
		t.Error("missing X-Request-ID header (RequestID middleware not applied)")
	}
}
