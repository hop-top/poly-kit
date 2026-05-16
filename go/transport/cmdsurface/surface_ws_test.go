package cmdsurface_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// wsFakeRunner is the WS surface's programmable Runner. RunFn and
// StreamFn provide responses; LastInvocation records the most recent
// Invocation, and StreamCtxObserved fires when the streamer detects
// ctx cancellation so disconnect/cancel tests can assert propagation.
type wsFakeRunner struct {
	mu             sync.Mutex
	RunFn          func(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error)
	StreamFn       func(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error
	LastInvocation cmdsurface.Invocation

	StreamCtxObserved chan error
	Started           chan struct{}
}

func newWSFakeRunner() *wsFakeRunner {
	return &wsFakeRunner{
		StreamCtxObserved: make(chan error, 4),
		Started:           make(chan struct{}, 4),
	}
}

func (r *wsFakeRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	r.mu.Lock()
	r.LastInvocation = inv
	fn := r.RunFn
	r.mu.Unlock()
	if fn == nil {
		return cmdsurface.Result{Stdout: "ok"}, nil
	}
	return fn(ctx, inv)
}

func (r *wsFakeRunner) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	r.mu.Lock()
	r.LastInvocation = inv
	fn := r.StreamFn
	r.mu.Unlock()
	defer close(out)
	if fn == nil {
		out <- cmdsurface.Event{Kind: "stdout", Data: "hello", At: time.Now()}
		out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{Stdout: "hello"}, At: time.Now()}
		return nil
	}
	return fn(ctx, inv, out)
}

// wsTestTree builds a cobra root with the leaves the WS tests need.
//
//	root
//	├── hello                       (read-only)
//	├── destroy                     (destructive)
//	├── secret                      (auth-required)
//	├── confirm                     (requires-confirmation)
//	├── hidden                      (NOT exposed on WS)
//	└── lines                       (streaming target; read-only)
func wsTestTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use:  "hello",
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
		Use:  "hidden",
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	root.AddCommand(&cobra.Command{
		Use:  "lines",
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})
	return root
}

// wsTestFixture wires tree → bridge → MountWS → httptest server.
type wsTestFixture struct {
	t      *testing.T
	bridge *cmdsurface.Bridge
	runner *wsFakeRunner
	srv    *httptest.Server
	wsURL  string
}

// newWSFixture creates a fixture with the default policy (no
// destructive on WS). Callers pass options to MountWS.
func newWSFixture(t *testing.T, exposed []string, opts ...cmdsurface.WSOption) *wsTestFixture {
	t.Helper()
	return newWSFixtureWithPolicy(t, exposed, cmdsurface.Policy{
		DefaultEnabled: []cmdsurface.Surface{cmdsurface.SurfaceCLI, cmdsurface.SurfaceLib},
	}, opts...)
}

func newWSFixtureWithPolicy(
	t *testing.T,
	exposed []string,
	policy cmdsurface.Policy,
	opts ...cmdsurface.WSOption,
) *wsTestFixture {
	t.Helper()
	runner := newWSFakeRunner()
	br := cmdsurface.New(
		wsTestTree(),
		cmdsurface.WithRunner(runner),
		cmdsurface.WithPolicy(policy),
	)
	for _, e := range exposed {
		br.Expose(e, cmdsurface.SurfaceWS)
	}

	r := api.NewRouter()
	if err := cmdsurface.MountWS(br, r, opts...); err != nil {
		t.Fatalf("MountWS: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return &wsTestFixture{
		t:      t,
		bridge: br,
		runner: runner,
		srv:    srv,
		wsURL:  "ws" + srv.URL[len("http"):],
	}
}

// dial opens a WS connection to the surface's default path. Optional
// HTTP headers cover the auth / confirmation pre-upgrade gates.
func (f *wsTestFixture) dial(t *testing.T, path string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return websocket.Dial(ctx, f.wsURL+path, &websocket.DialOptions{HTTPHeader: headers})
}

// dialOK is dial that asserts a successful upgrade.
func (f *wsTestFixture) dialOK(t *testing.T, path string, headers http.Header) *websocket.Conn {
	t.Helper()
	c, _, err := f.dial(t, path, headers)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	return c
}

// wsWriteJSON sends a JSON-encoded frame; wsReadJSON receives one.
type wsRawFrame struct {
	Op         string                 `json:"op"`
	ID         string                 `json:"id,omitempty"`
	Invocation *cmdsurface.Invocation `json:"invocation,omitempty"`
	Event      *cmdsurface.Event      `json:"event,omitempty"`
	Result     *cmdsurface.Result     `json:"result,omitempty"`
	Error      *wsRawError            `json:"error,omitempty"`
}

type wsRawError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func wsWriteJSON(t *testing.T, ctx context.Context, c *websocket.Conn, frame wsRawFrame) {
	t.Helper()
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func wsReadJSON(t *testing.T, ctx context.Context, c *websocket.Conn) wsRawFrame {
	t.Helper()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var f wsRawFrame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, data)
	}
	return f
}

// --- tests ---

func TestWSSurface_HappyPath(t *testing.T) {
	f := newWSFixture(t, []string{"hello"})
	f.runner.StreamFn = func(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
		out <- cmdsurface.Event{Kind: "stdout", Data: "hello", At: time.Now()}
		out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{Stdout: "hello"}, At: time.Now()}
		return nil
	}

	c := f.dialOK(t, "/ws/cmd", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsWriteJSON(t, ctx, c, wsRawFrame{
		Op:         "invoke",
		ID:         "1",
		Invocation: &cmdsurface.Invocation{Path: []string{"hello"}},
	})

	sawEvent := false
	for {
		frame := wsReadJSON(t, ctx, c)
		if frame.Op == "event" {
			sawEvent = true
			continue
		}
		if frame.Op == "result" {
			if frame.ID != "1" {
				t.Errorf("result.id=%q want 1", frame.ID)
			}
			if frame.Result == nil || frame.Result.Stdout != "hello" {
				t.Errorf("result=%+v want Stdout=hello", frame.Result)
			}
			break
		}
		if frame.Op == "error" {
			t.Fatalf("unexpected error: %+v", frame.Error)
		}
	}
	if !sawEvent {
		t.Error("expected at least one event frame")
	}
}

func TestWSSurface_UnknownCommand(t *testing.T) {
	f := newWSFixture(t, []string{"hello"})
	c := f.dialOK(t, "/ws/cmd", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsWriteJSON(t, ctx, c, wsRawFrame{
		Op:         "invoke",
		ID:         "x",
		Invocation: &cmdsurface.Invocation{Path: []string{"bogus"}},
	})

	frame := wsReadJSON(t, ctx, c)
	if frame.Op != "error" {
		t.Fatalf("op=%q want error (frame=%+v)", frame.Op, frame)
	}
	if frame.Error == nil || frame.Error.Code != "unknown_command" {
		t.Errorf("error=%+v want code=unknown_command", frame.Error)
	}
}

func TestWSSurface_SurfaceNotEnabled(t *testing.T) {
	// hello is exposed on WS; hidden is not.
	f := newWSFixture(t, []string{"hello"})
	c := f.dialOK(t, "/ws/cmd", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsWriteJSON(t, ctx, c, wsRawFrame{
		Op:         "invoke",
		ID:         "y",
		Invocation: &cmdsurface.Invocation{Path: []string{"hidden"}},
	})

	frame := wsReadJSON(t, ctx, c)
	if frame.Op != "error" {
		t.Fatalf("op=%q want error (frame=%+v)", frame.Op, frame)
	}
	if frame.Error == nil || frame.Error.Code != "not_enabled" {
		t.Errorf("error=%+v want code=not_enabled", frame.Error)
	}
}

func TestWSSurface_DestructiveBlocked(t *testing.T) {
	// Default policy disallows destructive on WS.
	f := newWSFixture(t, []string{"destroy"})
	c := f.dialOK(t, "/ws/cmd", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsWriteJSON(t, ctx, c, wsRawFrame{
		Op:         "invoke",
		ID:         "d",
		Invocation: &cmdsurface.Invocation{Path: []string{"destroy"}},
	})

	frame := wsReadJSON(t, ctx, c)
	if frame.Op != "error" {
		t.Fatalf("op=%q want error (frame=%+v)", frame.Op, frame)
	}
	if frame.Error == nil || frame.Error.Code != "destructive_blocked" {
		t.Errorf("error=%+v want code=destructive_blocked", frame.Error)
	}
}

func TestWSSurface_DestructiveAllowed(t *testing.T) {
	f := newWSFixtureWithPolicy(t,
		[]string{"destroy"},
		cmdsurface.Policy{
			AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceWS},
			DefaultEnabled:     []cmdsurface.Surface{cmdsurface.SurfaceCLI, cmdsurface.SurfaceLib},
		},
	)
	f.runner.StreamFn = func(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
		out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{Stdout: "boom"}, At: time.Now()}
		return nil
	}

	c := f.dialOK(t, "/ws/cmd", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsWriteJSON(t, ctx, c, wsRawFrame{
		Op:         "invoke",
		ID:         "d2",
		Invocation: &cmdsurface.Invocation{Path: []string{"destroy"}},
	})

	frame := readFirstResultOrError(t, ctx, c)
	if frame.Op != "result" {
		t.Fatalf("op=%q want result (frame=%+v)", frame.Op, frame)
	}
	if frame.Result == nil || frame.Result.Stdout != "boom" {
		t.Errorf("result=%+v want Stdout=boom", frame.Result)
	}
}

func TestWSSurface_AuthRequired_NoHeader(t *testing.T) {
	f := newWSFixture(t, []string{"secret"})

	_, resp, err := f.dial(t, "/ws/cmd", nil)
	if err == nil {
		t.Fatal("expected dial to fail without auth header")
	}
	if resp == nil {
		t.Fatalf("nil response (err=%v)", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", resp.StatusCode)
	}
}

func TestWSSurface_ConfirmationRequired_NoHeader(t *testing.T) {
	f := newWSFixture(t, []string{"confirm"})

	_, resp, err := f.dial(t, "/ws/cmd", nil)
	if err == nil {
		t.Fatal("expected dial to fail without confirm header")
	}
	if resp == nil {
		t.Fatalf("nil response (err=%v)", err)
	}
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Errorf("status=%d want 428", resp.StatusCode)
	}
}

func TestWSSurface_Cancellation(t *testing.T) {
	f := newWSFixture(t, []string{"lines"})
	ranTo := make(chan struct{})
	f.runner.StreamFn = func(ctx context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
		out <- cmdsurface.Event{Kind: "stdout", Data: "first", At: time.Now()}
		<-ctx.Done()
		f.runner.StreamCtxObserved <- ctx.Err()
		close(ranTo)
		return ctx.Err()
	}

	c := f.dialOK(t, "/ws/cmd", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsWriteJSON(t, ctx, c, wsRawFrame{
		Op:         "invoke",
		ID:         "cancel-me",
		Invocation: &cmdsurface.Invocation{Path: []string{"lines"}},
	})

	// Read the first event so we know the runner is in its select.
	frame := wsReadJSON(t, ctx, c)
	if frame.Op != "event" {
		t.Fatalf("first frame op=%q want event", frame.Op)
	}

	wsWriteJSON(t, ctx, c, wsRawFrame{Op: "cancel", ID: "cancel-me"})

	select {
	case got := <-f.runner.StreamCtxObserved:
		if got == nil {
			t.Errorf("ctx.Err()=nil after cancel; want non-nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner ctx did not observe cancellation in time")
	}
	<-ranTo
}

func TestWSSurface_ConcurrentInvocations(t *testing.T) {
	f := newWSFixture(t, []string{"lines"})
	// StreamFn forwards the id back as the stdout line so the test can
	// pair completion frames to their invoke ids.
	f.runner.StreamFn = func(_ context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
		out <- cmdsurface.Event{Kind: "stdout", Data: inv.Meta.TraceID, At: time.Now()}
		out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{Stdout: inv.Meta.TraceID}, At: time.Now()}
		return nil
	}

	c := f.dialOK(t, "/ws/cmd", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ids := []string{"a", "b", "c"}
	for _, id := range ids {
		wsWriteJSON(t, ctx, c, wsRawFrame{
			Op: "invoke",
			ID: id,
			Invocation: &cmdsurface.Invocation{
				Path: []string{"lines"},
				Meta: cmdsurface.Meta{TraceID: id},
			},
		})
	}

	seen := map[string]bool{}
	for len(seen) < len(ids) {
		frame := wsReadJSON(t, ctx, c)
		if frame.Op == "event" {
			continue
		}
		if frame.Op == "error" {
			t.Fatalf("unexpected error frame: %+v", frame.Error)
		}
		if frame.Op == "result" {
			if frame.Result == nil {
				t.Fatalf("nil result on frame %+v", frame)
			}
			seen[frame.ID] = true
		}
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("missing result for id=%q (saw=%v)", id, seen)
		}
	}
}

func TestWSSurface_MetaSurfaceForced(t *testing.T) {
	f := newWSFixture(t, []string{"hello"})

	c := f.dialOK(t, "/ws/cmd", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsWriteJSON(t, ctx, c, wsRawFrame{
		Op: "invoke",
		ID: "1",
		Invocation: &cmdsurface.Invocation{
			Path: []string{"hello"},
			Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceCLI}, // bogus
		},
	})
	frame := readFirstResultOrError(t, ctx, c)
	if frame.Op != "result" {
		t.Fatalf("frame=%+v want result", frame)
	}
	if got := f.runner.LastInvocation.Meta.Surface; got != cmdsurface.SurfaceWS {
		t.Errorf("runner saw Meta.Surface=%q want=%q", got, cmdsurface.SurfaceWS)
	}
}

func TestWSSurface_DisconnectCancelsPending(t *testing.T) {
	f := newWSFixture(t, []string{"lines"})
	observed := make(chan error, 1)
	started := make(chan struct{})
	f.runner.StreamFn = func(ctx context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
		out <- cmdsurface.Event{Kind: "stdout", Data: "starting", At: time.Now()}
		close(started)
		<-ctx.Done()
		observed <- ctx.Err()
		return ctx.Err()
	}

	c, _, err := f.dial(t, "/ws/cmd", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsWriteJSON(t, ctx, c, wsRawFrame{
		Op:         "invoke",
		ID:         "lifecycle",
		Invocation: &cmdsurface.Invocation{Path: []string{"lines"}},
	})
	// Read the first event so we know the runner has started.
	if frame := wsReadJSON(t, ctx, c); frame.Op != "event" {
		t.Fatalf("first frame op=%q want event", frame.Op)
	}
	<-started

	// Slam the connection shut. The server-side reader returns an
	// error; the deferred cancelAll fires and the runner sees its ctx
	// canceled.
	_ = c.Close(websocket.StatusNormalClosure, "")

	select {
	case got := <-observed:
		if got == nil {
			t.Errorf("runner ctx err=nil after disconnect; want non-nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner ctx did not observe cancellation after disconnect")
	}
}

func TestWSSurface_NilArgs(t *testing.T) {
	if err := cmdsurface.MountWS(nil, api.NewRouter()); err == nil {
		t.Error("MountWS(nil, r) = nil; want error")
	}
	br := cmdsurface.New(&cobra.Command{Use: "root"})
	if err := cmdsurface.MountWS(br, nil); err == nil {
		t.Error("MountWS(b, nil) = nil; want error")
	}
}

func TestWSSurface_BadOpEmitsError(t *testing.T) {
	f := newWSFixture(t, []string{"hello"})
	c := f.dialOK(t, "/ws/cmd", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsWriteJSON(t, ctx, c, wsRawFrame{Op: "bogus", ID: "1"})
	frame := wsReadJSON(t, ctx, c)
	if frame.Op != "error" {
		t.Fatalf("op=%q want error", frame.Op)
	}
	if frame.Error == nil || frame.Error.Code != "bad_request" {
		t.Errorf("error=%+v want code=bad_request", frame.Error)
	}
}

func TestWSSurface_WithWSPath(t *testing.T) {
	f := newWSFixture(t, []string{"hello"}, cmdsurface.WithWSPath("/custom/ws"))
	c := f.dialOK(t, "/custom/ws", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsWriteJSON(t, ctx, c, wsRawFrame{
		Op:         "invoke",
		ID:         "p",
		Invocation: &cmdsurface.Invocation{Path: []string{"hello"}},
	})
	frame := readFirstResultOrError(t, ctx, c)
	if frame.Op != "result" {
		t.Errorf("op=%q want result", frame.Op)
	}
}

// readFirstResultOrError drains event frames until a result or error
// frame appears. It is the read loop the table-driven tests share.
func readFirstResultOrError(t *testing.T, ctx context.Context, c *websocket.Conn) wsRawFrame {
	t.Helper()
	for {
		frame := wsReadJSON(t, ctx, c)
		switch frame.Op {
		case "event":
			continue
		case "result", "error":
			return frame
		default:
			t.Fatalf("unexpected op=%q frame=%+v", frame.Op, frame)
		}
	}
}

// TestWSSurface_BridgeSentinelMapping verifies the bridge sentinels
// surface through Bridge.Invoke as `errors.Is`-matchable values even
// when fired through the WS path's manual gates.
func TestWSSurface_BridgeSentinelMapping(t *testing.T) {
	f := newWSFixture(t, []string{"hello"})
	_, brErr := f.bridge.Invoke(context.Background(), cmdsurface.Invocation{
		Path: []string{"missing"},
		Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceWS},
	})
	if !errors.Is(brErr, cmdsurface.ErrUnknownCommand) {
		t.Errorf("bridge err=%v want=ErrUnknownCommand", brErr)
	}
}

// Sanity probe: confirm WithWSHub avoids starting a second hub.
// We can't observe goroutine counts portably, but we can verify the
// returned MountWS error is nil and the endpoint serves.
func TestWSSurface_WithWSHub_Honored(t *testing.T) {
	hub := api.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	f := newWSFixture(t, []string{"hello"}, cmdsurface.WithWSHub(hub))
	c := f.dialOK(t, "/ws/cmd", nil)
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()

	wsWriteJSON(t, rctx, c, wsRawFrame{
		Op:         "invoke",
		ID:         "h",
		Invocation: &cmdsurface.Invocation{Path: []string{"hello"}},
	})
	frame := readFirstResultOrError(t, rctx, c)
	if frame.Op != "result" {
		t.Errorf("op=%q want result", frame.Op)
	}
}

// counterRunner is used as a smoke-check that concurrent invocations
// truly run on separate goroutines: the runner blocks until all three
// streams are running, then releases.
type wsCounterRunner struct {
	target int32
	count  atomic.Int32
	gate   chan struct{}
}

func (r *wsCounterRunner) Run(context.Context, cmdsurface.Invocation) (cmdsurface.Result, error) {
	return cmdsurface.Result{}, errors.New("not implemented")
}

func (r *wsCounterRunner) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	defer close(out)
	n := r.count.Add(1)
	if n == r.target {
		close(r.gate)
	}
	select {
	case <-r.gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{Stdout: inv.Meta.TraceID}, At: time.Now()}
	return nil
}

func TestWSSurface_ConcurrentInvocations_Parallelism(t *testing.T) {
	cr := &wsCounterRunner{target: 3, gate: make(chan struct{})}
	br := cmdsurface.New(wsTestTree(),
		cmdsurface.WithRunner(cr),
		cmdsurface.WithPolicy(cmdsurface.Policy{
			DefaultEnabled: []cmdsurface.Surface{cmdsurface.SurfaceCLI, cmdsurface.SurfaceLib},
		}),
	)
	br.Expose("lines", cmdsurface.SurfaceWS)

	r := api.NewRouter()
	if err := cmdsurface.MountWS(br, r); err != nil {
		t.Fatalf("MountWS: %v", err)
	}
	srv := httptest.NewServer(r)
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):] + "/ws/cmd"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	for _, id := range []string{"x", "y", "z"} {
		wsWriteJSON(t, ctx, c, wsRawFrame{
			Op: "invoke",
			ID: id,
			Invocation: &cmdsurface.Invocation{
				Path: []string{"lines"},
				Meta: cmdsurface.Meta{TraceID: id},
			},
		})
	}

	seen := map[string]bool{}
	for len(seen) < 3 {
		frame := wsReadJSON(t, ctx, c)
		if frame.Op == "result" {
			seen[frame.ID] = true
		}
		if frame.Op == "error" {
			t.Fatalf("unexpected error frame: %+v", frame.Error)
		}
	}
}
