package cmdsurface_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// sseFakeRunner is a test-only Runner programmable for the SSE
// surface tests. StreamFn drives event emission; Run is a stub.
// LastInvocation captures what the handler passed in; StreamCtxErr
// captures ctx.Err() observed at the moment Stream returned so
// cancellation propagation can be asserted.
type sseFakeRunner struct {
	mu             sync.Mutex
	StreamFn       func(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error
	LastInvocation cmdsurface.Invocation
	StreamCtxErr   atomic.Value // error
	Started        chan struct{}
}

func (r *sseFakeRunner) Run(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
	return cmdsurface.Result{}, errors.New("sseFakeRunner: Run unused")
}

func (r *sseFakeRunner) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	r.mu.Lock()
	r.LastInvocation = inv
	fn := r.StreamFn
	started := r.Started
	r.mu.Unlock()
	if started != nil {
		close(started)
	}
	defer close(out)
	if fn == nil {
		out <- cmdsurface.Event{Kind: "stdout", Data: "hello", At: time.Now()}
		out <- cmdsurface.Event{Kind: "stdout", Data: "world", At: time.Now()}
		return nil
	}
	err := fn(ctx, inv, out)
	if ce := ctx.Err(); ce != nil {
		r.StreamCtxErr.Store(ce)
	}
	return err
}

// sseTestTree builds a small cobra tree with leaves of varying safety
// classes used by the SSE surface tests:
//
//	root
//	├── echo             (read)
//	├── destroy          (destructive)
//	├── secret           (auth-required)
//	├── confirm          (requires-confirmation)
//	└── slow             (heartbeat test target)
func sseTestTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use:         "echo",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	})
	root.AddCommand(&cobra.Command{
		Use:         "destroy",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	})
	root.AddCommand(&cobra.Command{
		Use:         "secret",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/auth-required": "true"},
	})
	root.AddCommand(&cobra.Command{
		Use:         "confirm",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/requires-confirmation": "true"},
	})
	root.AddCommand(&cobra.Command{
		Use:  "slow",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	return root
}

// sseTestServer wires bridge → MountSSE → httptest server.
func sseTestServer(t *testing.T, b *cmdsurface.Bridge, opts ...cmdsurface.SSEOption) (*httptest.Server, func()) {
	t.Helper()
	router := api.NewRouter()
	if err := cmdsurface.MountSSE(b, router, opts...); err != nil {
		t.Fatalf("MountSSE: %v", err)
	}
	srv := httptest.NewServer(router)
	return srv, srv.Close
}

// sseFrame is one parsed Server-Sent-Events frame.
type sseFrame struct {
	Event   string
	Data    string
	Comment string
}

// sseReadFrames reads SSE frames from r until ctx is canceled or io
// returns EOF. Each call to next yields the next frame or io.EOF.
// Comment frames (lines beginning with ':') are surfaced verbatim in
// the Comment field with leading ':' trimmed.
type sseReader struct{ sc *bufio.Scanner }

func newSSEReader(r io.Reader) *sseReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &sseReader{sc: sc}
}

// next reads the next frame from the stream, returning io.EOF when
// the connection closes between frames.
func (r *sseReader) next() (sseFrame, error) {
	var f sseFrame
	hasFields := false
	for r.sc.Scan() {
		line := r.sc.Text()
		if line == "" {
			if hasFields {
				return f, nil
			}
			continue
		}
		hasFields = true
		switch {
		case strings.HasPrefix(line, ":"):
			f.Comment = strings.TrimPrefix(strings.TrimPrefix(line, ":"), " ")
		case strings.HasPrefix(line, "event:"):
			f.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			d := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			if f.Data == "" {
				f.Data = d
			} else {
				f.Data += "\n" + d
			}
		}
	}
	if err := r.sc.Err(); err != nil {
		return f, err
	}
	if !hasFields {
		return f, io.EOF
	}
	return f, nil
}

// sseGetStream opens an SSE request and returns the response so the
// caller can drive a custom read loop.
func sseGetStream(t *testing.T, url string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

// --- tests ---

func TestSSESurface_HappyPath(t *testing.T) {
	runner := &sseFakeRunner{
		StreamFn: func(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
			out <- cmdsurface.Event{Kind: "stdout", Data: "line1", At: time.Now()}
			out <- cmdsurface.Event{Kind: "stdout", Data: "line2", At: time.Now()}
			return nil
		},
	}
	b := cmdsurface.New(sseTestTree(), cmdsurface.WithRunner(runner)).
		Expose("echo", cmdsurface.SurfaceSSE)
	srv, stop := sseTestServer(t, b)
	defer stop()

	resp := sseGetStream(t, srv.URL+"/cmd/echo/stream", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type=%q want text/event-stream", ct)
	}

	rdr := newSSEReader(resp.Body)
	var events []sseFrame
	for {
		f, err := rdr.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		if f.Event == "" && f.Comment != "" {
			continue
		}
		events = append(events, f)
		if f.Event == "result" || f.Event == "error" {
			break
		}
	}
	// Want: 2x event, 1x result.
	if len(events) != 3 {
		t.Fatalf("frames=%d want 3; got=%+v", len(events), events)
	}
	if events[0].Event != "event" || events[1].Event != "event" {
		t.Errorf("first two frames event names = %q, %q; want event,event",
			events[0].Event, events[1].Event)
	}
	if events[2].Event != "result" {
		t.Errorf("terminal frame=%q want result", events[2].Event)
	}
	var res cmdsurface.Result
	if err := json.Unmarshal([]byte(events[2].Data), &res); err != nil {
		t.Fatalf("decode result: %v; data=%s", err, events[2].Data)
	}
	if !strings.Contains(res.Stdout, "line1") || !strings.Contains(res.Stdout, "line2") {
		t.Errorf("Result.Stdout=%q want both line1 and line2", res.Stdout)
	}
}

func TestSSESurface_UnknownCommand(t *testing.T) {
	b := cmdsurface.New(sseTestTree(), cmdsurface.WithRunner(&sseFakeRunner{})).
		Expose("echo", cmdsurface.SurfaceSSE)
	srv, stop := sseTestServer(t, b)
	defer stop()

	resp := sseGetStream(t, srv.URL+"/cmd/bogus/stream", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d want 404", resp.StatusCode)
	}
}

func TestSSESurface_SurfaceNotEnabled(t *testing.T) {
	// echo is exposed, destroy is NOT — but no route is registered for
	// destroy at all, so the server returns 404 from the router. To
	// exercise the handler's own not_enabled path we manually flip the
	// route state via a custom mux is overkill — accept router 404 as
	// the canonical answer (matches REST surface behavior).
	b := cmdsurface.New(sseTestTree(), cmdsurface.WithRunner(&sseFakeRunner{})).
		Expose("echo", cmdsurface.SurfaceSSE)
	srv, stop := sseTestServer(t, b)
	defer stop()

	resp := sseGetStream(t, srv.URL+"/cmd/destroy/stream", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d want 404", resp.StatusCode)
	}
}

func TestSSESurface_DestructiveBlocked(t *testing.T) {
	// Expose destroy on SSE with default policy (no AllowDestructiveOn):
	// route registers, but handler's preflight returns 403.
	b := cmdsurface.New(sseTestTree(), cmdsurface.WithRunner(&sseFakeRunner{})).
		Expose("destroy", cmdsurface.SurfaceSSE)
	srv, stop := sseTestServer(t, b)
	defer stop()

	resp := sseGetStream(t, srv.URL+"/cmd/destroy/stream", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d want 403", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var ae api.APIError
	if err := json.Unmarshal(body, &ae); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	if ae.Code != "destructive_blocked" {
		t.Errorf("code=%q want destructive_blocked", ae.Code)
	}
}

func TestSSESurface_DestructiveAllowed(t *testing.T) {
	runner := &sseFakeRunner{
		StreamFn: func(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
			out <- cmdsurface.Event{Kind: "stdout", Data: "boom", At: time.Now()}
			return nil
		},
	}
	b := cmdsurface.New(sseTestTree(),
		cmdsurface.WithRunner(runner),
		cmdsurface.WithPolicy(cmdsurface.Policy{
			AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceSSE},
			DefaultEnabled:     []cmdsurface.Surface{cmdsurface.SurfaceCLI, cmdsurface.SurfaceLib},
		}),
	).Expose("destroy", cmdsurface.SurfaceSSE)
	srv, stop := sseTestServer(t, b)
	defer stop()

	resp := sseGetStream(t, srv.URL+"/cmd/destroy/stream", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	rdr := newSSEReader(resp.Body)
	gotResult := false
	for {
		f, err := rdr.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if f.Event == "result" {
			gotResult = true
			break
		}
	}
	if !gotResult {
		t.Errorf("did not see result frame")
	}
}

func TestSSESurface_AuthRequired_Missing(t *testing.T) {
	b := cmdsurface.New(sseTestTree(), cmdsurface.WithRunner(&sseFakeRunner{})).
		Expose("secret", cmdsurface.SurfaceSSE)
	srv, stop := sseTestServer(t, b)
	defer stop()

	resp := sseGetStream(t, srv.URL+"/cmd/secret/stream", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", resp.StatusCode)
	}
}

func TestSSESurface_ConfirmationRequired_Missing(t *testing.T) {
	b := cmdsurface.New(sseTestTree(), cmdsurface.WithRunner(&sseFakeRunner{})).
		Expose("confirm", cmdsurface.SurfaceSSE)
	srv, stop := sseTestServer(t, b)
	defer stop()

	resp := sseGetStream(t, srv.URL+"/cmd/confirm/stream", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("status=%d want 428", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var ae api.APIError
	if err := json.Unmarshal(body, &ae); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ae.Code != "confirmation_required" {
		t.Errorf("code=%q want confirmation_required", ae.Code)
	}
}

func TestSSESurface_Heartbeat(t *testing.T) {
	// Runner blocks until release; we expect comment frames in the
	// interim. Use 20ms heartbeat so the test stays fast.
	release := make(chan struct{})
	runner := &sseFakeRunner{
		StreamFn: func(ctx context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
			select {
			case <-release:
			case <-ctx.Done():
			}
			out <- cmdsurface.Event{Kind: "stdout", Data: "done", At: time.Now()}
			return nil
		},
	}
	b := cmdsurface.New(sseTestTree(), cmdsurface.WithRunner(runner)).
		Expose("slow", cmdsurface.SurfaceSSE)
	router := api.NewRouter()
	if err := cmdsurface.MountSSE(b, router); err != nil {
		t.Fatalf("MountSSE: %v", err)
	}
	// Reach into the surface using the unexported heartbeat option by
	// wrapping the call — exported through a package-level Option chain.
	// Since withSSEHeartbeat is unexported, we rebuild the router with
	// the option via a package-internal helper exposed for tests.
	// (The option is in the same package; see surface_sse_internal_test.go.)
	srv := httptest.NewServer(router)
	defer srv.Close()

	// Replace the default router with one that uses a tighter heartbeat.
	router2 := api.NewRouter()
	if err := cmdsurface.MountSSE(b, router2, cmdsurface.TestingSSEHeartbeat(20*time.Millisecond)); err != nil {
		t.Fatalf("MountSSE with heartbeat: %v", err)
	}
	srv2 := httptest.NewServer(router2)
	defer srv2.Close()

	resp := sseGetStream(t, srv2.URL+"/cmd/slow/stream", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}

	rdr := newSSEReader(resp.Body)
	gotComment := false
	done := time.After(500 * time.Millisecond)

readLoop:
	for {
		select {
		case <-done:
			break readLoop
		default:
		}
		f, err := rdr.next()
		if err != nil {
			break
		}
		if f.Comment != "" {
			gotComment = true
			close(release)
			break
		}
	}
	if !gotComment {
		t.Errorf("did not observe heartbeat comment frame")
	}
	// Drain remaining frames so the server-side goroutine exits cleanly.
	for {
		_, err := rdr.next()
		if err != nil {
			return
		}
	}
}

func TestSSESurface_ClientDisconnect_CancelsRunner(t *testing.T) {
	canceled := make(chan struct{})
	runner := &sseFakeRunner{
		Started: make(chan struct{}),
		StreamFn: func(ctx context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
			out <- cmdsurface.Event{Kind: "stdout", Data: "first", At: time.Now()}
			select {
			case <-ctx.Done():
				close(canceled)
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return errors.New("test: ctx never canceled")
			}
		},
	}
	b := cmdsurface.New(sseTestTree(), cmdsurface.WithRunner(runner)).
		Expose("echo", cmdsurface.SurfaceSSE)
	srv, stop := sseTestServer(t, b)
	defer stop()

	// Use a custom client/transport so we can abort mid-stream.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/cmd/echo/stream", nil)
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	rdr := newSSEReader(resp.Body)
	// Read one event frame.
	for {
		f, err := rdr.next()
		if err != nil {
			t.Fatalf("read first frame: %v", err)
		}
		if f.Event == "event" {
			break
		}
	}
	cancel()
	resp.Body.Close()

	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("runner ctx was not canceled after client disconnect")
	}
}

func TestSSESurface_ArgsAndFlagsFromQuery(t *testing.T) {
	runner := &sseFakeRunner{
		StreamFn: func(_ context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
			// Echo the parsed shape via a stdout frame for the assertion.
			payload, _ := json.Marshal(inv)
			out <- cmdsurface.Event{Kind: "stdout", Data: string(payload), At: time.Now()}
			return nil
		},
	}
	b := cmdsurface.New(sseTestTree(), cmdsurface.WithRunner(runner)).
		Expose("echo", cmdsurface.SurfaceSSE)
	srv, stop := sseTestServer(t, b)
	defer stop()

	u := srv.URL + "/cmd/echo/stream?arg=foo&arg=bar&flag.name=hello&flag.tag=a&flag.tag=b"
	resp := sseGetStream(t, u, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	rdr := newSSEReader(resp.Body)
	for {
		f, err := rdr.next()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if f.Event == "result" || f.Event == "error" {
			break
		}
	}

	got := runner.LastInvocation
	if want := []string{"echo"}; !sseEqual(got.Path, want) {
		t.Errorf("Path=%v want=%v", got.Path, want)
	}
	if want := []string{"foo", "bar"}; !sseEqual(got.Args, want) {
		t.Errorf("Args=%v want=%v", got.Args, want)
	}
	if got.Flags == nil {
		t.Fatalf("Flags=nil; want name=hello, tag=[a,b]")
	}
	if got.Flags["name"] != "hello" {
		t.Errorf("Flags[name]=%v want hello", got.Flags["name"])
	}
	tag, ok := got.Flags["tag"].([]string)
	if !ok {
		t.Fatalf("Flags[tag] type=%T want []string", got.Flags["tag"])
	}
	if !sseEqual(tag, []string{"a", "b"}) {
		t.Errorf("Flags[tag]=%v want [a b]", tag)
	}
}

func TestSSESurface_MetaSurfaceForced(t *testing.T) {
	runner := &sseFakeRunner{}
	b := cmdsurface.New(sseTestTree(), cmdsurface.WithRunner(runner)).
		Expose("echo", cmdsurface.SurfaceSSE)
	srv, stop := sseTestServer(t, b)
	defer stop()

	resp := sseGetStream(t, srv.URL+"/cmd/echo/stream?flag.meta.surface=cli", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	rdr := newSSEReader(resp.Body)
	for {
		f, err := rdr.next()
		if err != nil {
			break
		}
		if f.Event == "result" || f.Event == "error" {
			break
		}
	}
	if got := runner.LastInvocation.Meta.Surface; got != cmdsurface.SurfaceSSE {
		t.Errorf("Meta.Surface=%q want sse", got)
	}
}

func TestSSESurface_HeadersCorrect(t *testing.T) {
	b := cmdsurface.New(sseTestTree(), cmdsurface.WithRunner(&sseFakeRunner{})).
		Expose("echo", cmdsurface.SurfaceSSE)
	srv, stop := sseTestServer(t, b)
	defer stop()

	resp := sseGetStream(t, srv.URL+"/cmd/echo/stream", nil)
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	for _, c := range []struct{ k, want string }{
		{"Content-Type", "text/event-stream"},
		{"Cache-Control", "no-cache"},
		{"Connection", "keep-alive"},
		{"X-Accel-Buffering", "no"},
	} {
		got := resp.Header.Get(c.k)
		if got != c.want {
			t.Errorf("header %s=%q want=%q", c.k, got, c.want)
		}
	}
}

// sseEqual compares two string slices.
func sseEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
