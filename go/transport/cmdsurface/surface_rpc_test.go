package cmdsurface_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
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

// start mounts the RPC service and stands up an h2c-capable httptest
// server, so every wire protocol (gRPC included) reaches it.
func (f *testFixture) start(opts ...cmdsurface.RPCOption) {
	f.t.Helper()
	f.srvMock = newMountServer()
	if err := cmdsurface.MountRPC(f.bridge, f.srvMock, opts...); err != nil {
		f.t.Fatalf("MountRPC: %v", err)
	}
	f.ts = newH2CServer(f.t, f.srvMock)
}

// client returns a generated Commands client speaking protocol p.
func (f *testFixture) client(p wireProtocol) cmdsurfacev1connect.CommandsClient {
	return cmdsurfacev1connect.NewCommandsClient(h2cClient(), f.ts.URL, p.opts...)
}

// wireProtocol is one of the encodings a Connect handler serves.
type wireProtocol struct {
	name        string
	opts        []connect.ClientOption
	contentType string // request Content-Type the protocol sends (unary)
	http2       bool   // protocol requires HTTP/2
}

// wireProtocols is every protocol the generated handler must answer:
// Connect with binary proto and with JSON, native gRPC, gRPC-Web.
var wireProtocols = []wireProtocol{
	{name: "connect+proto", contentType: "application/proto"},
	{name: "connect+json", opts: []connect.ClientOption{connect.WithProtoJSON()}, contentType: "application/json"},
	{name: "grpc", opts: []connect.ClientOption{connect.WithGRPC()}, contentType: "application/grpc", http2: true},
	{name: "grpc-web", opts: []connect.ClientOption{connect.WithGRPCWeb()}, contentType: "application/grpc-web+proto"},
}

// forEachProtocol runs fn once per wire protocol as a subtest.
func forEachProtocol(t *testing.T, fn func(t *testing.T, p wireProtocol)) {
	t.Helper()
	for _, p := range wireProtocols {
		t.Run(p.name, func(t *testing.T) { fn(t, p) })
	}
}

// newH2CServer serves h over HTTP/1.1 and unencrypted HTTP/2 (prior
// knowledge), the transport native gRPC clients use without TLS.
func newH2CServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(h)
	ts.Config.Protocols = new(http.Protocols)
	ts.Config.Protocols.SetHTTP1(true)
	ts.Config.Protocols.SetUnencryptedHTTP2(true)
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

// h2cClient speaks unencrypted HTTP/2 with prior knowledge only.
func h2cClient() *http.Client {
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	return &http.Client{Transport: &http.Transport{Protocols: p}}
}

// invocation builds a request for the leaf at path.
func invocation(path ...string) *connect.Request[cmdsurfacev1.Invocation] {
	return connect.NewRequest(&cmdsurfacev1.Invocation{Path: path})
}

// --- tests ---

func TestRPCInvoke_HappyPath(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.runner.RunFn = func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{Stdout: "hello", ExitCode: 0}, nil
		}
		f.start()

		resp, err := f.client(p).Invoke(context.Background(), invocation("echo"))
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if resp.Msg.GetStdout() != "hello" {
			t.Errorf("Stdout=%q want=hello", resp.Msg.GetStdout())
		}
		if resp.Msg.ExitCode == nil || resp.Msg.GetExitCode() != 0 {
			t.Errorf("ExitCode=%v want set to 0", resp.Msg.ExitCode)
		}
	})
}

// TestRPC_WireProtocolsReachHandler proves each client really spoke its
// protocol: the handler saw the protocol's Content-Type, and gRPC
// arrived over HTTP/2.
func TestRPC_WireProtocolsReachHandler(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		srv := newMountServer()
		if err := cmdsurface.MountRPC(f.bridge, srv); err != nil {
			t.Fatalf("MountRPC: %v", err)
		}
		type seen struct {
			contentType string
			protoMajor  int
		}
		got := make(chan seen, 1)
		ts := newH2CServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got <- seen{r.Header.Get("Content-Type"), r.ProtoMajor}
			srv.ServeHTTP(w, r)
		}))

		client := cmdsurfacev1connect.NewCommandsClient(h2cClient(), ts.URL, p.opts...)
		if _, err := client.Invoke(context.Background(), invocation("echo")); err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		s := <-got
		if s.contentType != p.contentType {
			t.Errorf("Content-Type=%q want=%q", s.contentType, p.contentType)
		}
		if p.http2 && s.protoMajor != 2 {
			t.Errorf("HTTP/%d want HTTP/2", s.protoMajor)
		}
	})
}

func TestRPCInvoke_UnknownCommand(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.start()

		_, err := f.client(p).Invoke(context.Background(), invocation("bogus"))
		if got, want := connect.CodeOf(err), connect.CodeNotFound; err == nil || got != want {
			t.Errorf("code=%v want=%v (err=%v)", got, want, err)
		}
	})
}

func TestRPCInvoke_EmptyPath(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.start()

		_, err := f.client(p).Invoke(context.Background(), invocation())
		if got, want := connect.CodeOf(err), connect.CodeInvalidArgument; err == nil || got != want {
			t.Errorf("code=%v want=%v (err=%v)", got, want, err)
		}
	})
}

func TestRPCInvoke_SurfaceNotEnabled(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		// hidden-rpc is NOT exposed on RPC.
		f.start()

		_, err := f.client(p).Invoke(context.Background(), invocation("hidden-rpc"))
		if got, want := connect.CodeOf(err), connect.CodeNotFound; err == nil || got != want {
			t.Errorf("code=%v want=%v (err=%v)", got, want, err)
		}
	})
}

func TestRPCInvoke_DestructiveBlocked(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		// Default policy: no destructive on RPC.
		f.start()

		_, err := f.client(p).Invoke(context.Background(), invocation("destroy"))
		if got, want := connect.CodeOf(err), connect.CodePermissionDenied; err == nil || got != want {
			t.Errorf("code=%v want=%v (err=%v)", got, want, err)
		}
	})
}

func TestRPCInvoke_DestructiveAllowed(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
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
		ts := newH2CServer(t, srvMock)

		client := cmdsurfacev1connect.NewCommandsClient(h2cClient(), ts.URL, p.opts...)
		resp, err := client.Invoke(context.Background(), invocation("destroy"))
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if resp.Msg.GetStdout() != "boom" {
			t.Errorf("Stdout=%q want=boom", resp.Msg.GetStdout())
		}
	})
}

func TestRPCInvoke_AuthRequiredMissing(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.start()

		_, err := f.client(p).Invoke(context.Background(), invocation("secret"))
		if got, want := connect.CodeOf(err), connect.CodeUnauthenticated; err == nil || got != want {
			t.Errorf("code=%v want=%v (err=%v)", got, want, err)
		}
	})
}

func TestRPCInvoke_AuthRequiredPresent(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.start()

		req := invocation("secret")
		req.Header().Set("Authorization", "Bearer xxx")
		if _, err := f.client(p).Invoke(context.Background(), req); err != nil {
			t.Fatalf("Invoke: %v", err)
		}
	})
}

func TestRPCInvoke_AuthRequiredCallerSubstitutes(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.start()

		req := connect.NewRequest(&cmdsurfacev1.Invocation{
			Path: []string{"secret"},
			Meta: &cmdsurfacev1.Meta{Caller: "svc:ci"},
		})
		if _, err := f.client(p).Invoke(context.Background(), req); err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if got := f.runner.LastInvocation.Meta.Caller; got != "svc:ci" {
			t.Errorf("runner saw Meta.Caller=%q want=svc:ci", got)
		}
	})
}

func TestRPCInvoke_ConfirmationRequiredMissing(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.start()

		_, err := f.client(p).Invoke(context.Background(), invocation("confirm"))
		if got, want := connect.CodeOf(err), connect.CodeFailedPrecondition; err == nil || got != want {
			t.Errorf("code=%v want=%v (err=%v)", got, want, err)
		}
	})
}

func TestRPCInvoke_ConfirmationPresent(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.start()

		req := invocation("confirm")
		req.Header().Set("X-Confirm-Token", "yes")
		if _, err := f.client(p).Invoke(context.Background(), req); err != nil {
			t.Fatalf("Invoke: %v", err)
		}
	})
}

func TestRPCInvoke_ExitCodePreserved(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.runner.RunFn = func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{ExitCode: 2, Stderr: "nope"}, nil
		}
		f.start()

		resp, err := f.client(p).Invoke(context.Background(), invocation("echo"))
		if err != nil {
			t.Fatalf("Invoke: %v (non-zero ExitCode must not be an error)", err)
		}
		if resp.Msg.GetExitCode() != 2 || resp.Msg.GetStderr() != "nope" {
			t.Errorf("ExitCode=%d Stderr=%q want=2 nope", resp.Msg.GetExitCode(), resp.Msg.GetStderr())
		}
	})
}

// TestRPCInvoke_InvocationDecoded checks every Invocation field
// crosses the wire into the runner's Go Invocation.
func TestRPCInvoke_InvocationDecoded(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.start()

		flags, err := structpb.NewStruct(map[string]any{"count": 3, "name": "x", "on": true})
		if err != nil {
			t.Fatal(err)
		}
		req := connect.NewRequest(&cmdsurfacev1.Invocation{
			Path:  []string{"echo"},
			Args:  []string{"a", "b"},
			Flags: flags,
			Meta: &cmdsurfacev1.Meta{
				Caller: "u1", Tenant: "t1", RequestId: "r1", TraceId: "tr1",
				IdempotencyKey: "k1", Extra: map[string]string{"x": "y"},
			},
		})
		if _, err := f.client(p).Invoke(context.Background(), req); err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		got := f.runner.LastInvocation
		want := cmdsurface.Invocation{
			Path:  []string{"echo"},
			Args:  []string{"a", "b"},
			Flags: map[string]any{"count": float64(3), "name": "x", "on": true},
			Meta: cmdsurface.Meta{
				Caller: "u1", Tenant: "t1", Surface: cmdsurface.SurfaceRPC,
				RequestID: "r1", TraceID: "tr1", IdempotencyKey: "k1",
				Extra: map[string]string{"x": "y"},
			},
		}
		if got.Meta.RequestedAt.IsZero() {
			t.Error("Meta.RequestedAt not stamped")
		}
		got.Meta.RequestedAt = time.Time{}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("runner saw\n  %#v\nwant\n  %#v", got, want)
		}
	})
}

// TestRPCInvoke_DataDigitsPreserved checks structured data keeps the
// digits the command wrote: numbers a double reads back unchanged stay
// numbers, others travel as strings, and data_json is exact.
func TestRPCInvoke_DataDigitsPreserved(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.runner.RunFn = func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{Data: map[string]any{
				"id":    json.Number("12345678901234567890"),
				"price": json.Number("1.10"),
				"n":     json.Number("42"),
				"tags":  []any{"a", json.Number("0.1")},
				"html":  "<b>&</b>",
				"none":  nil,
			}}, nil
		}
		f.start()

		resp, err := f.client(p).Invoke(context.Background(), invocation("echo"))
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		fields := resp.Msg.GetData().GetStructValue().GetFields()
		if got := fields["id"].GetStringValue(); got != "12345678901234567890" {
			t.Errorf("id=%v want string 12345678901234567890", fields["id"])
		}
		if got, ok := fields["price"].GetKind().(*structpb.Value_NumberValue); !ok || got.NumberValue != 1.1 {
			t.Errorf("price=%v want number 1.1", fields["price"])
		}
		if got := fields["n"].GetNumberValue(); got != 42 {
			t.Errorf("n=%v want number 42", fields["n"])
		}
		if got := fields["tags"].GetListValue().GetValues()[1].GetNumberValue(); got != 0.1 {
			t.Errorf("tags[1]=%v want number 0.1", got)
		}
		if _, ok := fields["none"].GetKind().(*structpb.Value_NullValue); !ok {
			t.Errorf("none=%v want null", fields["none"])
		}
		const wantJSON = `{"html":"<b>&</b>","id":12345678901234567890,"n":42,"none":null,"price":1.10,"tags":["a",0.1]}`
		if got := resp.Msg.GetDataJson(); got != wantJSON {
			t.Errorf("data_json=%s\nwant      %s", got, wantJSON)
		}
	})
}

func TestRPCInvoke_MetaSurfaceForced(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.start()

		req := connect.NewRequest(&cmdsurfacev1.Invocation{
			Path: []string{"echo"},
			Meta: &cmdsurfacev1.Meta{Surface: string(cmdsurface.SurfaceCLI)}, // wrong surface
		})
		if _, err := f.client(p).Invoke(context.Background(), req); err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if got := f.runner.LastInvocation.Meta.Surface; got != cmdsurface.SurfaceRPC {
			t.Errorf("runner saw Meta.Surface=%q want=%q", got, cmdsurface.SurfaceRPC)
		}
	})
}

func TestRPCInvokeStream_HappyPath(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		const n = 3
		f.runner.StreamFn = func(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
			for i := 0; i < n; i++ {
				out <- cmdsurface.Event{Kind: "stdout", Data: "line", At: time.Now()}
			}
			out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{ExitCode: 3, Stdout: "line\n"}, At: time.Now()}
			return nil
		}
		f.start()

		stream, err := f.client(p).InvokeStream(context.Background(), invocation("lines"))
		if err != nil {
			t.Fatalf("InvokeStream: %v", err)
		}
		t.Cleanup(func() { _ = stream.Close() })

		var got []*cmdsurfacev1.Event
		for stream.Receive() {
			got = append(got, stream.Msg())
		}
		if err := stream.Err(); err != nil {
			t.Fatalf("Receive: %v", err)
		}
		if len(got) != n+1 {
			t.Fatalf("event count=%d want=%d (events=%v)", len(got), n+1, got)
		}
		if got[0].GetKind() != "stdout" || got[0].GetData().GetStringValue() != "line" || got[0].GetAt() == nil {
			t.Errorf("first event=%v want stdout line with at", got[0])
		}
		done := got[n]
		if done.GetKind() != "done" {
			t.Errorf("terminal Kind=%q want=done", done.GetKind())
		}
		if done.GetResult().GetExitCode() != 3 || done.GetResult().GetStdout() != "line\n" {
			t.Errorf("done result=%v want exit 3 stdout line", done.GetResult())
		}
		// JSON clients read the result from data, as before the proto.
		if ec := done.GetData().GetStructValue().GetFields()["exit_code"].GetNumberValue(); ec != 3 {
			t.Errorf("done data exit_code=%v want 3", ec)
		}
	})
}

func TestRPCInvokeStream_GatesMapped(t *testing.T) {
	cases := []struct {
		leaf string
		code connect.Code
	}{
		{"bogus", connect.CodeNotFound},
		{"hidden-rpc", connect.CodeNotFound},
		{"destroy", connect.CodePermissionDenied},
		{"secret", connect.CodeUnauthenticated},
		{"confirm", connect.CodeFailedPrecondition},
	}
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.start()
		for _, tc := range cases {
			stream, err := f.client(p).InvokeStream(context.Background(), invocation(tc.leaf))
			if err == nil {
				for stream.Receive() {
				}
				err = stream.Err()
				_ = stream.Close()
			}
			if got := connect.CodeOf(err); err == nil || got != tc.code {
				t.Errorf("%s: code=%v want=%v (err=%v)", tc.leaf, got, tc.code, err)
			}
		}
	})
}

func TestRPCInvokeStream_CtxCancel(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
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
		stream, err := f.client(p).InvokeStream(ctx, invocation("lines"))
		if err != nil {
			t.Fatalf("InvokeStream: %v", err)
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
	})
}

func TestRPCInvokeStream_MetaSurfaceForced(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.runner.StreamFn = func(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
			out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{}, At: time.Now()}
			return nil
		}
		f.start()

		req := connect.NewRequest(&cmdsurfacev1.Invocation{
			Path: []string{"lines"},
			Meta: &cmdsurfacev1.Meta{Surface: string(cmdsurface.SurfaceCLI)},
		})
		stream, err := f.client(p).InvokeStream(context.Background(), req)
		if err != nil {
			t.Fatalf("InvokeStream: %v", err)
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
	})
}

func TestMountRPC_NilArgs(t *testing.T) {
	if err := cmdsurface.MountRPC(nil, newMountServer()); err == nil {
		t.Error("MountRPC(nil, srv) = nil error; want error")
	}
	if err := cmdsurface.MountRPC(cmdsurface.New(&cobra.Command{Use: "x"}), nil); err == nil {
		t.Error("MountRPC(b, nil) = nil error; want error")
	}
}

// TestMountRPC_ServerInterceptorsApplied checks MountRPC runs the
// server's own Interceptors() as well as WithRPCInterceptors ones.
func TestMountRPC_ServerInterceptorsApplied(t *testing.T) {
	f := newFixture(t)
	f.srvMock = newMountServer()
	var server, extra atomic.Int32
	counter := func(n *atomic.Int32) connect.Interceptor {
		return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				n.Add(1)
				return next(ctx, req)
			}
		})
	}
	f.srvMock.interceptor = []connect.Interceptor{counter(&server)}
	if err := cmdsurface.MountRPC(f.bridge, f.srvMock, cmdsurface.WithRPCInterceptors(counter(&extra))); err != nil {
		t.Fatalf("MountRPC: %v", err)
	}
	f.ts = newH2CServer(t, f.srvMock)

	if _, err := f.client(wireProtocols[0]).Invoke(context.Background(), invocation("echo")); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if server.Load() != 1 || extra.Load() != 1 {
		t.Errorf("interceptor calls server=%d extra=%d want 1 and 1", server.Load(), extra.Load())
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

	if _, err := f.client(wireProtocols[0]).Invoke(context.Background(), invocation("echo")); err != nil {
		t.Fatalf("Invoke: %v", err)
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

	_, err := f.client(wireProtocols[0]).Invoke(context.Background(), invocation("echo"))
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
