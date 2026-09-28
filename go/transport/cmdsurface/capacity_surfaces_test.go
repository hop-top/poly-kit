package cmdsurface_test

// The capacity gate answers in each surface's vocabulary: 503
// overloaded with Retry-After over HTTP — before a stream opens —
// Unavailable with Retry-After metadata over Connect, an overloaded
// error frame over WebSocket. Each case holds the service's one slot,
// with no queue, so the call is refused.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// fullBridge is a bridge over gateTree exposed on surface whose one
// slot is held and whose queue is empty, so every call is overloaded.
func fullBridge(t *testing.T, surface cmdsurface.Surface) (*cmdsurface.Bridge, *gateSink) {
	t.Helper()
	sink := &gateSink{}
	b := cmdsurface.New(gateTree(),
		cmdsurface.WithRunner(&rlRunner{}),
		cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnOK: true, OnError: true}),
		cmdsurface.WithConcurrency(cmdsurface.Concurrency{MaxInflight: 1}),
	)
	b.Expose("*", surface)
	t.Cleanup(cmdsurface.TestingHoldSlots(b))
	return b, sink
}

// assertAuditedOverloaded checks the refusal reached the sinks once.
func assertAuditedOverloaded(t *testing.T, sink *gateSink) {
	t.Helper()
	if err := sink.awaitOne(t); !errors.Is(err, cmdsurface.ErrOverloaded) {
		t.Fatalf("audit = %v, want overloaded", err)
	}
}

// assertHTTPOverloaded checks a 503 overloaded answer with a one-second
// Retry-After: no run has ended yet, so the hint is its floor.
func assertHTTPOverloaded(t *testing.T, resp *http.Response) {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want 1", got)
	}
	var ae api.APIError
	if err := json.Unmarshal(body, &ae); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	if ae.Code != api.CodeOverloaded {
		t.Errorf("code = %q, want %q", ae.Code, api.CodeOverloaded)
	}
}

func TestCapacitySurface_Projection(t *testing.T) {
	for _, route := range []string{"/v1/commands/widget/add", "/v1/commands/widget/add/stream"} {
		t.Run(route, func(t *testing.T) {
			sink := &auditSink{}
			b := cmdsurface.New(projectionTree(false),
				cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnOK: true, OnError: true}),
				cmdsurface.WithConcurrency(cmdsurface.Concurrency{MaxInflight: 1}))
			b.Expose("*", cmdsurface.SurfaceREST)
			defer cmdsurface.TestingHoldSlots(b)()
			r := api.NewRouter()
			if err := cmdsurface.MountProjection(b, r); err != nil {
				t.Fatal(err)
			}
			url := serve(t, r)
			assertHTTPOverloaded(t, httpSend(t, http.MethodPost, url+route, `{"flags":{"name":"b"}}`))
			recs := sink.records()
			if len(recs) != 1 || !errors.Is(recs[0].err, cmdsurface.ErrOverloaded) {
				t.Fatalf("audit = %+v, want the one overloaded refusal", recs)
			}
		})
	}
}

func TestCapacitySurface_RESTAndSSE(t *testing.T) {
	t.Run("rest", func(t *testing.T) {
		b, sink := fullBridge(t, cmdsurface.SurfaceREST)
		url, stop := newServer(t, b)
		defer stop()
		assertHTTPOverloaded(t, httpSend(t, http.MethodPost, url+"/cmd/open", "{}"))
		assertAuditedOverloaded(t, sink)
	})
	t.Run("sse", func(t *testing.T) {
		b, sink := fullBridge(t, cmdsurface.SurfaceSSE)
		srv, stop := sseTestServer(t, b)
		defer stop()
		assertHTTPOverloaded(t, sseGetStream(t, srv.URL+"/cmd/open/stream", nil))
		assertAuditedOverloaded(t, sink)
	})
}

func TestCapacitySurface_WS(t *testing.T) {
	b, sink := fullBridge(t, cmdsurface.SurfaceWS)
	f := mountGateWS(t, b)
	c := f.dialOK(t, "/ws/cmd", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsWriteJSON(t, ctx, c, wsRawFrame{Op: "invoke", ID: "r", Invocation: &cmdsurface.Invocation{Path: []string{"open"}}})
	frame := readFirstResultOrError(t, ctx, c)
	if frame.Op != "error" || frame.Error == nil || frame.Error.Code != api.CodeOverloaded {
		t.Fatalf("frame = %+v, want an overloaded error", frame)
	}
	assertAuditedOverloaded(t, sink)
}

func TestCapacitySurface_RPC(t *testing.T) {
	check := func(t *testing.T, err error) {
		t.Helper()
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("code = %v (err %v), want Unavailable", connect.CodeOf(err), err)
		}
		var ce *connect.Error
		if !errors.As(err, &ce) || ce.Meta().Get("Retry-After") != "1" {
			t.Fatalf("Retry-After metadata = %q, want 1", ce.Meta().Get("Retry-After"))
		}
	}
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		t.Run("invoke", func(t *testing.T) {
			b, sink := fullBridge(t, cmdsurface.SurfaceRPC)
			client := mountGateRPC(t, b, p)
			_, err := client.Invoke(context.Background(), invocation("open"))
			check(t, err)
			assertAuditedOverloaded(t, sink)
		})
		t.Run("stream", func(t *testing.T) {
			b, sink := fullBridge(t, cmdsurface.SurfaceRPC)
			client := mountGateRPC(t, b, p)
			stream, err := client.InvokeStream(context.Background(), invocation("open"))
			if err == nil {
				for stream.Receive() {
					t.Fatal("a refused stream sent a message")
				}
				err = stream.Err()
				_ = stream.Close()
			}
			check(t, err)
			assertAuditedOverloaded(t, sink)
		})
	})
}
