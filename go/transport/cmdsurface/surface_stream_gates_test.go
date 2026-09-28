package cmdsurface_test

// Streaming surfaces run a command under the gates, audit and
// idempotency forwarding Bridge.Invoke applies. These tests pin that
// for each of them — SSE, WebSocket, RPC InvokeStream and the
// library's StreamArgs — with one shape per surface: a permission
// refusal is answered in the surface's own vocabulary, never reaches
// the Runner and is audited once; a non-invocable leaf is refused the
// same way; a permitted stream runs once and is audited once.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
)

// gateSink records every audit record the bridge emits.
type gateSink struct {
	mu   sync.Mutex
	invs []cmdsurface.Invocation
	res  []cmdsurface.Result
	errs []error
}

func (s *gateSink) Emit(_ context.Context, inv cmdsurface.Invocation, res cmdsurface.Result, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invs = append(s.invs, inv)
	s.res = append(s.res, res)
	s.errs = append(s.errs, err)
	return nil
}

// records returns a snapshot of the audit errors seen so far.
func (s *gateSink) records() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]error(nil), s.errs...)
}

// awaitOne waits for the first audit record and then asserts it is
// the only one. A stream's record is written when the run ends, which
// a client may observe a moment before the server has finished.
func (s *gateSink) awaitOne(t *testing.T) error {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(s.records()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// Give a duplicate record the chance to show up before asserting.
	time.Sleep(20 * time.Millisecond)
	recs := s.records()
	if len(recs) != 1 {
		t.Fatalf("audit records = %d (%v), want exactly 1", len(recs), recs)
	}
	return recs[0]
}

// gateRunner streams one line and a done event, counting how often a
// surface reached it. Run is not a streaming surface's path.
type gateRunner struct {
	streams atomic.Int32
}

func (r *gateRunner) Run(context.Context, cmdsurface.Invocation) (cmdsurface.Result, error) {
	return cmdsurface.Result{}, errors.New("gateRunner: Run is not the streaming path")
}

func (r *gateRunner) Stream(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	defer close(out)
	r.streams.Add(1)
	out <- cmdsurface.Event{Kind: "stdout", Data: "ran", At: time.Now()}
	out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{Stdout: "ran\n"}, At: time.Now()}
	return nil
}

// gateTree is the tree every streaming-gate test projects:
//
//	root
//	├── open     (read; permitted)
//	├── locked   (read; refused by the permission gate)
//	└── shell    (interactive; never invocable through a transport)
func gateTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	for _, name := range []string{"open", "locked"} {
		root.AddCommand(&cobra.Command{
			Use:         name,
			RunE:        func(*cobra.Command, []string) error { return nil },
			Annotations: map[string]string{"kit/side-effect": "read"},
		})
	}
	root.AddCommand(&cobra.Command{
		Use:         "shell",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "interactive"},
	})
	return root
}

// denyLocked refuses the "locked" leaf for every caller.
func denyLocked(_ context.Context, _ cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
	if leaf.PathKey() == "locked" {
		return cmdsurface.PermissionDecision{Reason: "locked is off limits"}
	}
	return cmdsurface.PermissionDecision{Allowed: true}
}

// gateBridge builds the bridge over gateTree with every leaf exposed
// on surface, the denyLocked gate, and a recording sink.
func gateBridge(surface cmdsurface.Surface) (*cmdsurface.Bridge, *gateRunner, *gateSink) {
	run := &gateRunner{}
	sink := &gateSink{}
	b := cmdsurface.New(gateTree(),
		cmdsurface.WithRunner(run),
		cmdsurface.WithPermission(denyLocked),
		cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnOK: true, OnError: true}),
	)
	b.Expose("*", surface)
	return b, run, sink
}

// --- SSE ---

func TestStreamGates_SSE(t *testing.T) {
	refusals := []struct {
		leaf   string
		status int
		code   string
		want   error
	}{
		{"locked", http.StatusForbidden, "permission_denied", cmdsurface.ErrPermissionDenied},
		{"shell", http.StatusNotFound, "not_invocable", cmdsurface.ErrNotInvocable},
	}
	for _, tc := range refusals {
		t.Run(tc.leaf+" refused before the stream opens", func(t *testing.T) {
			b, run, sink := gateBridge(cmdsurface.SurfaceSSE)
			srv, stop := sseTestServer(t, b)
			defer stop()

			resp := sseGetStream(t, srv.URL+"/cmd/"+tc.leaf+"/stream", nil)
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			body, _ := io.ReadAll(resp.Body)
			var ae api.APIError
			if err := json.Unmarshal(body, &ae); err != nil {
				t.Fatalf("decode: %v; body=%s", err, body)
			}
			if ae.Code != tc.code {
				t.Errorf("code = %q, want %q", ae.Code, tc.code)
			}
			if n := run.streams.Load(); n != 0 {
				t.Fatalf("runner streamed %d times for a refused call", n)
			}
			if err := sink.awaitOne(t); !errors.Is(err, tc.want) {
				t.Fatalf("audited %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("permitted stream runs and is audited once", func(t *testing.T) {
		b, run, sink := gateBridge(cmdsurface.SurfaceSSE)
		srv, stop := sseTestServer(t, b)
		defer stop()

		resp := sseGetStream(t, srv.URL+"/cmd/open/stream", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		rdr := newSSEReader(resp.Body)
		var terminal string
		for terminal == "" {
			f, err := rdr.next()
			if err != nil {
				t.Fatalf("read frame: %v", err)
			}
			if f.Event == "result" || f.Event == "error" {
				terminal = f.Event
			}
		}
		if terminal != "result" {
			t.Fatalf("terminal frame = %q, want result", terminal)
		}
		if n := run.streams.Load(); n != 1 {
			t.Fatalf("runner streams = %d, want 1", n)
		}
		if err := sink.awaitOne(t); err != nil {
			t.Fatalf("audited %v, want a clean run", err)
		}
	})
}

// --- WebSocket ---

func TestStreamGates_WS(t *testing.T) {
	refusals := []struct {
		leaf string
		code string
		want error
	}{
		{"locked", "permission_denied", cmdsurface.ErrPermissionDenied},
		{"shell", "not_invocable", cmdsurface.ErrNotInvocable},
	}
	for _, tc := range refusals {
		t.Run(tc.leaf+" refused in band", func(t *testing.T) {
			b, run, sink := gateBridge(cmdsurface.SurfaceWS)
			f := mountGateWS(t, b)
			c := f.dialOK(t, "/ws/cmd", nil)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			wsWriteJSON(t, ctx, c, wsRawFrame{
				Op: "invoke", ID: "r1",
				Invocation: &cmdsurface.Invocation{Path: []string{tc.leaf}},
			})
			frame := readFirstResultOrError(t, ctx, c)
			if frame.Op != "error" || frame.ID != "r1" {
				t.Fatalf("frame = %+v, want an error frame for r1", frame)
			}
			if frame.Error == nil || frame.Error.Code != tc.code {
				t.Fatalf("error = %+v, want code %s", frame.Error, tc.code)
			}
			if n := run.streams.Load(); n != 0 {
				t.Fatalf("runner streamed %d times for a refused call", n)
			}
			if err := sink.awaitOne(t); !errors.Is(err, tc.want) {
				t.Fatalf("audited %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("permitted stream runs and is audited once", func(t *testing.T) {
		b, run, sink := gateBridge(cmdsurface.SurfaceWS)
		f := mountGateWS(t, b)
		c := f.dialOK(t, "/ws/cmd", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		wsWriteJSON(t, ctx, c, wsRawFrame{
			Op: "invoke", ID: "ok",
			Invocation: &cmdsurface.Invocation{Path: []string{"open"}},
		})
		frame := readFirstResultOrError(t, ctx, c)
		if frame.Op != "result" || frame.Result == nil || frame.Result.Stdout != "ran\n" {
			t.Fatalf("frame = %+v, want the run's result", frame)
		}
		if n := run.streams.Load(); n != 1 {
			t.Fatalf("runner streams = %d, want 1", n)
		}
		if err := sink.awaitOne(t); err != nil {
			t.Fatalf("audited %v, want a clean run", err)
		}
	})
}

// mountGateWS mounts b's WS surface on a test server and returns the
// fixture the WS tests dial through.
func mountGateWS(t *testing.T, b *cmdsurface.Bridge) *wsTestFixture {
	t.Helper()
	r := api.NewRouter()
	if err := cmdsurface.MountWS(b, r); err != nil {
		t.Fatalf("MountWS: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &wsTestFixture{t: t, bridge: b, srv: srv, wsURL: "ws" + srv.URL[len("http"):]}
}

// --- RPC ---

func TestStreamGates_RPCInvokeStream(t *testing.T) {
	refusals := []struct {
		leaf string
		code connect.Code
		want error
	}{
		{"locked", connect.CodePermissionDenied, cmdsurface.ErrPermissionDenied},
		{"shell", connect.CodeNotFound, cmdsurface.ErrNotInvocable},
	}
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		for _, tc := range refusals {
			t.Run(tc.leaf+" refused before the first message", func(t *testing.T) {
				b, run, sink := gateBridge(cmdsurface.SurfaceRPC)
				client := mountGateRPC(t, b, p)

				stream, err := client.InvokeStream(context.Background(), invocation(tc.leaf))
				var got int
				if err == nil {
					for stream.Receive() {
						got++
					}
					err = stream.Err()
					_ = stream.Close()
				}
				if connect.CodeOf(err) != tc.code {
					t.Fatalf("code = %v (err %v), want %v", connect.CodeOf(err), err, tc.code)
				}
				if got != 0 {
					t.Fatalf("received %d messages before the refusal", got)
				}
				if n := run.streams.Load(); n != 0 {
					t.Fatalf("runner streamed %d times for a refused call", n)
				}
				if err := sink.awaitOne(t); !errors.Is(err, tc.want) {
					t.Fatalf("audited %v, want %v", err, tc.want)
				}
			})
		}

		t.Run("permitted stream runs and is audited once", func(t *testing.T) {
			b, run, sink := gateBridge(cmdsurface.SurfaceRPC)
			client := mountGateRPC(t, b, p)

			stream, err := client.InvokeStream(context.Background(), invocation("open"))
			if err != nil {
				t.Fatalf("InvokeStream: %v", err)
			}
			var kinds []string
			for stream.Receive() {
				kinds = append(kinds, stream.Msg().GetKind())
			}
			if err := stream.Err(); err != nil {
				t.Fatalf("Receive: %v", err)
			}
			_ = stream.Close()
			if len(kinds) != 2 || kinds[1] != "done" {
				t.Fatalf("events = %v, want [stdout done]", kinds)
			}
			if n := run.streams.Load(); n != 1 {
				t.Fatalf("runner streams = %d, want 1", n)
			}
			if err := sink.awaitOne(t); err != nil {
				t.Fatalf("audited %v, want a clean run", err)
			}
		})
	})
}

// TestStreamGates_RPCInvokeRefusalCodes pins the unary procedure's
// mapping of the two refusals that used to fall through to Internal.
func TestStreamGates_RPCInvokeRefusalCodes(t *testing.T) {
	cases := []struct {
		leaf string
		code connect.Code
	}{
		{"locked", connect.CodePermissionDenied},
		{"shell", connect.CodeNotFound},
	}
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		b, _, _ := gateBridge(cmdsurface.SurfaceRPC)
		client := mountGateRPC(t, b, p)
		for _, tc := range cases {
			_, err := client.Invoke(context.Background(), invocation(tc.leaf))
			if connect.CodeOf(err) != tc.code {
				t.Errorf("%s: code = %v (err %v), want %v", tc.leaf, connect.CodeOf(err), err, tc.code)
			}
		}
	})
}

// mountGateRPC mounts b's RPC surface on an h2c test server and
// returns a generated client speaking protocol p.
func mountGateRPC(t *testing.T, b *cmdsurface.Bridge, p wireProtocol) cmdsurfacev1connect.CommandsClient {
	t.Helper()
	f := &testFixture{t: t, bridge: b}
	f.start()
	return f.client(p)
}

// --- Library ---

func TestStreamGates_LibStreamArgs(t *testing.T) {
	refusals := []struct {
		leaf string
		want error
	}{
		{"locked", cmdsurface.ErrPermissionDenied},
		{"shell", cmdsurface.ErrNotInvocable},
	}
	for _, tc := range refusals {
		t.Run(tc.leaf+" refused", func(t *testing.T) {
			b, run, sink := gateBridge(cmdsurface.SurfaceLib)
			out := make(chan cmdsurface.Event, 8)
			err := cmdsurface.StreamArgs(context.Background(), b, []string{tc.leaf}, out)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if n := run.streams.Load(); n != 0 {
				t.Fatalf("runner streamed %d times for a refused call", n)
			}
			// An in-process call has no caller to attribute: the
			// library surface is not audited, refusal or run.
			if recs := sink.records(); len(recs) != 0 {
				t.Fatalf("library call audited: %v", recs)
			}
		})
	}

	t.Run("permitted stream runs", func(t *testing.T) {
		b, run, sink := gateBridge(cmdsurface.SurfaceLib)
		out := make(chan cmdsurface.Event, 8)
		if err := cmdsurface.StreamArgs(context.Background(), b, []string{"open"}, out); err != nil {
			t.Fatalf("StreamArgs: %v", err)
		}
		var kinds []string
		for ev := range out {
			kinds = append(kinds, ev.Kind)
		}
		if len(kinds) != 2 || kinds[1] != "done" {
			t.Fatalf("events = %v, want [stdout done]", kinds)
		}
		if n := run.streams.Load(); n != 1 {
			t.Fatalf("runner streams = %d, want 1", n)
		}
		if recs := sink.records(); len(recs) != 0 {
			t.Fatalf("library call audited: %v", recs)
		}
	})
}
