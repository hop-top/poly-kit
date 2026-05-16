package cmdsurface_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/cmdsurface"
)

// fakeRunner is a programmable Runner used by the RPC surface tests.
// Set RunFn / StreamFn to control responses; LastInvocation records
// the most recent Invocation seen by either method. StreamCtxErr
// captures the streamer's ctx.Err() at exit time so cancellation
// propagation can be asserted.
type fakeRunner struct {
	mu             sync.Mutex
	RunFn          func(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error)
	StreamFn       func(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error
	LastInvocation cmdsurface.Invocation
	StreamCtxErr   atomic.Value // error
}

func (r *fakeRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	r.mu.Lock()
	r.LastInvocation = inv
	fn := r.RunFn
	r.mu.Unlock()
	if fn == nil {
		return cmdsurface.Result{Stdout: "ok"}, nil
	}
	return fn(ctx, inv)
}

func (r *fakeRunner) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	r.mu.Lock()
	r.LastInvocation = inv
	fn := r.StreamFn
	r.mu.Unlock()
	defer close(out)
	if fn == nil {
		out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{}, At: time.Now()}
		return nil
	}
	err := fn(ctx, inv, out)
	r.StreamCtxErr.Store(errOrNil(ctx.Err()))
	return err
}

func errOrNil(e error) error {
	if e == nil {
		// Store can't take a typed nil through interface{}; wrap.
		return errSentinel{}
	}
	return e
}

type errSentinel struct{}

func (errSentinel) Error() string { return "" }

// testFixture wires a tree → Bridge → MountRPC → httptest server.
// Callers tweak the Bridge (Expose, custom Policy) before calling
// start.
type testFixture struct {
	t       *testing.T
	root    *cobra.Command
	bridge  *cmdsurface.Bridge
	runner  *fakeRunner
	ts      *httptest.Server
	srvMock *mountServer
}

// mountServer satisfies the rpcServerMount interface MountRPC accepts.
// It is a minimal http.ServeMux wrapper that also tracks interceptors.
type mountServer struct {
	mux         *http.ServeMux
	interceptor []connect.Interceptor
}

func newMountServer() *mountServer {
	return &mountServer{mux: http.NewServeMux()}
}

func (m *mountServer) Handle(path string, h http.Handler) { m.mux.Handle(path, h) }
func (m *mountServer) Interceptors() []connect.Interceptor {
	return m.interceptor
}
func (m *mountServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mux.ServeHTTP(w, r)
}

// newFixture builds a cobra tree with named leaves so each test can
// pick the leaf shape it needs.
//
//	root
//	├── echo            (read-only)
//	├── destroy         (destructive)
//	├── secret          (auth-required)
//	├── confirm         (requires-confirmation)
//	├── hidden-rpc      (no RPC enablement — left out of Expose)
//	└── lines           (stream test target)
func newFixture(t *testing.T) *testFixture {
	t.Helper()
	root := &cobra.Command{Use: "root"}

	root.AddCommand(&cobra.Command{
		Use:  "echo",
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	root.AddCommand(&cobra.Command{
		Use: "destroy",
		Annotations: map[string]string{
			"kit/side-effect": "destructive",
		},
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	root.AddCommand(&cobra.Command{
		Use: "secret",
		Annotations: map[string]string{
			"kit/auth-required": "true",
		},
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	root.AddCommand(&cobra.Command{
		Use: "confirm",
		Annotations: map[string]string{
			"kit/requires-confirmation": "true",
		},
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	root.AddCommand(&cobra.Command{
		Use:  "hidden-rpc",
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	root.AddCommand(&cobra.Command{
		Use:  "lines",
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})

	fr := &fakeRunner{}
	br := cmdsurface.New(root, cmdsurface.WithRunner(fr))
	// Default policy denies destructive on RPC; tests override per case.
	br.Expose("echo", cmdsurface.SurfaceRPC)
	br.Expose("destroy", cmdsurface.SurfaceRPC)
	br.Expose("secret", cmdsurface.SurfaceRPC)
	br.Expose("confirm", cmdsurface.SurfaceRPC)
	br.Expose("lines", cmdsurface.SurfaceRPC)

	return &testFixture{
		t:      t,
		root:   root,
		bridge: br,
		runner: fr,
	}
}

// start mounts the RPC service and stands up a httptest server.
func (f *testFixture) start(opts ...cmdsurface.RPCOption) {
	f.t.Helper()
	f.srvMock = newMountServer()
	if err := cmdsurface.MountRPC(f.bridge, f.srvMock, opts...); err != nil {
		f.t.Fatalf("MountRPC: %v", err)
	}
	f.ts = httptest.NewServer(f.srvMock)
	f.t.Cleanup(f.ts.Close)
}

// unaryClient returns a Connect client for the Invoke procedure.
func (f *testFixture) unaryClient() *connect.Client[cmdsurface.Invocation, cmdsurface.Result] {
	return connect.NewClient[cmdsurface.Invocation, cmdsurface.Result](
		f.ts.Client(),
		f.ts.URL+cmdsurface.RPCInvokeProcedure,
		cmdsurface.RPCClientOptions()...,
	)
}

// streamClient returns a Connect client for the InvokeStream procedure.
func (f *testFixture) streamClient() *connect.Client[cmdsurface.Invocation, cmdsurface.Event] {
	return connect.NewClient[cmdsurface.Invocation, cmdsurface.Event](
		f.ts.Client(),
		f.ts.URL+cmdsurface.RPCInvokeStreamProcedure,
		cmdsurface.RPCClientOptions()...,
	)
}

// --- tests ---

func TestRPCInvoke_HappyPath(t *testing.T) {
	f := newFixture(t)
	f.runner.RunFn = func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
		return cmdsurface.Result{Stdout: "hello", ExitCode: 0}, nil
	}
	f.start()

	resp, err := f.unaryClient().CallUnary(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"echo"}}),
	)
	if err != nil {
		t.Fatalf("CallUnary: %v", err)
	}
	if resp.Msg.Stdout != "hello" {
		t.Errorf("Stdout=%q want=hello", resp.Msg.Stdout)
	}
}

func TestRPCInvoke_UnknownCommand(t *testing.T) {
	f := newFixture(t)
	f.start()

	_, err := f.unaryClient().CallUnary(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"bogus"}}),
	)
	if err == nil {
		t.Fatal("CallUnary: expected error, got nil")
	}
	if got, want := connect.CodeOf(err), connect.CodeNotFound; got != want {
		t.Errorf("code=%v want=%v (err=%v)", got, want, err)
	}
}

func TestRPCInvoke_SurfaceNotEnabled(t *testing.T) {
	f := newFixture(t)
	// hidden-rpc is NOT exposed on RPC.
	f.start()

	_, err := f.unaryClient().CallUnary(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"hidden-rpc"}}),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := connect.CodeOf(err), connect.CodeNotFound; got != want {
		t.Errorf("code=%v want=%v (err=%v)", got, want, err)
	}
}

func TestRPCInvoke_DestructiveBlocked(t *testing.T) {
	f := newFixture(t)
	// Default policy: no destructive on RPC.
	f.start()

	_, err := f.unaryClient().CallUnary(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"destroy"}}),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := connect.CodeOf(err), connect.CodePermissionDenied; got != want {
		t.Errorf("code=%v want=%v (err=%v)", got, want, err)
	}
}

func TestRPCInvoke_DestructiveAllowed(t *testing.T) {
	t.Helper()
	// Custom bridge with permissive policy.
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use: "destroy",
		Annotations: map[string]string{
			"kit/side-effect": "destructive",
		},
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	fr := &fakeRunner{RunFn: func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
		return cmdsurface.Result{Stdout: "boom"}, nil
	}}
	br := cmdsurface.New(root,
		cmdsurface.WithRunner(fr),
		cmdsurface.WithPolicy(cmdsurface.Policy{
			AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceRPC},
		}),
	)
	br.Expose("destroy", cmdsurface.SurfaceRPC)

	srvMock := newMountServer()
	if err := cmdsurface.MountRPC(br, srvMock); err != nil {
		t.Fatalf("MountRPC: %v", err)
	}
	ts := httptest.NewServer(srvMock)
	t.Cleanup(ts.Close)

	client := connect.NewClient[cmdsurface.Invocation, cmdsurface.Result](
		ts.Client(),
		ts.URL+cmdsurface.RPCInvokeProcedure,
		cmdsurface.RPCClientOptions()...,
	)
	resp, err := client.CallUnary(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"destroy"}}),
	)
	if err != nil {
		t.Fatalf("CallUnary: %v", err)
	}
	if resp.Msg.Stdout != "boom" {
		t.Errorf("Stdout=%q want=boom", resp.Msg.Stdout)
	}
}

func TestRPCInvoke_AuthRequiredMissing(t *testing.T) {
	f := newFixture(t)
	f.start()

	_, err := f.unaryClient().CallUnary(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"secret"}}),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := connect.CodeOf(err), connect.CodeUnauthenticated; got != want {
		t.Errorf("code=%v want=%v (err=%v)", got, want, err)
	}
}

func TestRPCInvoke_AuthRequiredPresent(t *testing.T) {
	f := newFixture(t)
	f.start()

	req := connect.NewRequest(&cmdsurface.Invocation{Path: []string{"secret"}})
	req.Header().Set("Authorization", "Bearer xxx")
	if _, err := f.unaryClient().CallUnary(context.Background(), req); err != nil {
		t.Fatalf("CallUnary: %v", err)
	}
}

func TestRPCInvoke_ConfirmationRequiredMissing(t *testing.T) {
	f := newFixture(t)
	f.start()

	_, err := f.unaryClient().CallUnary(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"confirm"}}),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := connect.CodeOf(err), connect.CodeFailedPrecondition; got != want {
		t.Errorf("code=%v want=%v (err=%v)", got, want, err)
	}
}

func TestRPCInvoke_ConfirmationPresent(t *testing.T) {
	f := newFixture(t)
	f.start()

	req := connect.NewRequest(&cmdsurface.Invocation{Path: []string{"confirm"}})
	req.Header().Set("X-Confirm-Token", "yes")
	if _, err := f.unaryClient().CallUnary(context.Background(), req); err != nil {
		t.Fatalf("CallUnary: %v", err)
	}
}

func TestRPCInvoke_ExitCodePreserved(t *testing.T) {
	f := newFixture(t)
	f.runner.RunFn = func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
		return cmdsurface.Result{ExitCode: 2, Stderr: "nope"}, nil
	}
	f.start()

	resp, err := f.unaryClient().CallUnary(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"echo"}}),
	)
	if err != nil {
		t.Fatalf("CallUnary: %v (non-zero ExitCode must not be an error)", err)
	}
	if resp.Msg.ExitCode != 2 {
		t.Errorf("ExitCode=%d want=2", resp.Msg.ExitCode)
	}
}

func TestRPCInvoke_MetaSurfaceForced(t *testing.T) {
	f := newFixture(t)
	f.start()

	req := connect.NewRequest(&cmdsurface.Invocation{
		Path: []string{"echo"},
		Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceCLI}, // wrong surface
	})
	if _, err := f.unaryClient().CallUnary(context.Background(), req); err != nil {
		t.Fatalf("CallUnary: %v", err)
	}
	got := f.runner.LastInvocation.Meta.Surface
	if got != cmdsurface.SurfaceRPC {
		t.Errorf("runner saw Meta.Surface=%q want=%q", got, cmdsurface.SurfaceRPC)
	}
}

func TestRPCInvokeStream_HappyPath(t *testing.T) {
	f := newFixture(t)
	const n = 3
	f.runner.StreamFn = func(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
		for i := 0; i < n; i++ {
			out <- cmdsurface.Event{Kind: "stdout", Data: "line", At: time.Now()}
		}
		out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{}, At: time.Now()}
		return nil
	}
	f.start()

	stream, err := f.streamClient().CallServerStream(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"lines"}}),
	)
	if err != nil {
		t.Fatalf("CallServerStream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	var got []cmdsurface.Event
	for stream.Receive() {
		got = append(got, *stream.Msg())
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(got) != n+1 {
		t.Fatalf("event count=%d want=%d (events=%+v)", len(got), n+1, got)
	}
	if got[n].Kind != "done" {
		t.Errorf("terminal Kind=%q want=done", got[n].Kind)
	}
}

func TestRPCInvokeStream_CtxCancel(t *testing.T) {
	f := newFixture(t)
	streamerCtxObserved := make(chan error, 1)
	started := make(chan struct{})
	f.runner.StreamFn = func(ctx context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
		close(started)
		// Emit one event so the client receives something to confirm
		// the stream is live, then block on ctx.Done.
		out <- cmdsurface.Event{Kind: "stdout", Data: "first", At: time.Now()}
		<-ctx.Done()
		streamerCtxObserved <- ctx.Err()
		return ctx.Err()
	}
	f.start()

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := f.streamClient().CallServerStream(ctx,
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"lines"}}),
	)
	if err != nil {
		t.Fatalf("CallServerStream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	// Receive the first event to ensure the streamer is running.
	if !stream.Receive() {
		t.Fatalf("Receive: %v", stream.Err())
	}
	<-started
	cancel()

	select {
	case got := <-streamerCtxObserved:
		if got == nil {
			t.Errorf("streamer ctx.Err()=nil, expected non-nil after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("streamer goroutine did not observe ctx cancellation")
	}
}

func TestRPCInvokeStream_MetaSurfaceForced(t *testing.T) {
	f := newFixture(t)
	f.runner.StreamFn = func(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
		out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{}, At: time.Now()}
		return nil
	}
	f.start()

	req := connect.NewRequest(&cmdsurface.Invocation{
		Path: []string{"lines"},
		Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceCLI},
	})
	stream, err := f.streamClient().CallServerStream(context.Background(), req)
	if err != nil {
		t.Fatalf("CallServerStream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	for stream.Receive() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if got := f.runner.LastInvocation.Meta.Surface; got != cmdsurface.SurfaceRPC {
		t.Errorf("runner saw Meta.Surface=%q want=%q", got, cmdsurface.SurfaceRPC)
	}
}

func TestMountRPC_NilArgs(t *testing.T) {
	if err := cmdsurface.MountRPC(nil, newMountServer()); err == nil {
		t.Error("MountRPC(nil, srv) = nil error; want error")
	}
	if err := cmdsurface.MountRPC(cmdsurface.New(&cobra.Command{Use: "x"}), nil); err == nil {
		t.Error("MountRPC(b, nil) = nil error; want error")
	}
}

// Sanity: ensure RPCOption + WithRPCInterceptors plumbing compiles
// and runs (the assertion is the interceptor count seen at handler
// time, but we don't peek inside Connect — we only verify the option
// applies via a counter-side-effect).
func TestWithRPCInterceptors_Plumbed(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	ic := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			calls.Add(1)
			return next(ctx, req)
		}
	})
	f.start(cmdsurface.WithRPCInterceptors(ic))

	if _, err := f.unaryClient().CallUnary(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"echo"}}),
	); err != nil {
		t.Fatalf("CallUnary: %v", err)
	}
	if calls.Load() == 0 {
		t.Error("custom interceptor never invoked")
	}
}

// Verify the bridge sentinel-to-Connect mapping fires for an error
// originating below the preflight gate (i.e. a leaf the index says is
// exposed but whose policy changes after mount). We simulate this by
// mounting, then Hide()ing the leaf — the in-memory Leaf is shared
// between index and bridge, so the cache stays consistent and the
// surface check fires through the bridge instead of the index.
func TestRPCInvoke_BridgeSentinelMapping(t *testing.T) {
	f := newFixture(t)
	f.start()
	// After mount: Hide echo on RPC. The index in surface_rpc still
	// points at the same Leaf, whose Enabled map is now updated.
	f.bridge.Hide("echo", cmdsurface.SurfaceRPC)

	_, err := f.unaryClient().CallUnary(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"echo"}}),
	)
	if err == nil {
		t.Fatal("expected error after Hide, got nil")
	}
	if got, want := connect.CodeOf(err), connect.CodeNotFound; got != want {
		t.Errorf("code=%v want=%v", got, want)
	}
	// Confirm errors.Is still surfaces through the wrapped error chain
	// (Connect strips the underlying error; we just exercise the chain
	// on a direct bridge call).
	_, brErr := f.bridge.Invoke(context.Background(),
		cmdsurface.Invocation{
			Path: []string{"echo"},
			Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceRPC},
		},
	)
	if !errors.Is(brErr, cmdsurface.ErrSurfaceNotEnabled) {
		t.Errorf("bridge err=%v want=ErrSurfaceNotEnabled", brErr)
	}
}
