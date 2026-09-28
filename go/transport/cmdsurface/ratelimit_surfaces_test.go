package cmdsurface_test

// The rate-limit gate answers in each surface's vocabulary: 429
// rate_limited with Retry-After over HTTP, ResourceExhausted with
// Retry-After metadata over Connect, a rate_limited error frame over
// WebSocket. Each case spends the caller's one token with a first
// call, then asserts the second call's refusal and its audit record.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// oneAMinute allows one call a minute in every tier, so a second call
// is refused with a Retry-After of one minute.
var oneAMinute = cmdsurface.RateLimit{
	Read:        cmdsurface.RateRule{PerMinute: 1, Burst: 1},
	Write:       cmdsurface.RateRule{PerMinute: 1, Burst: 1},
	Destructive: cmdsurface.RateRule{PerMinute: 1, Burst: 1},
}

// rlRunner answers Run and Stream alike.
type rlRunner struct{ gateRunner }

func (r *rlRunner) Run(context.Context, cmdsurface.Invocation) (cmdsurface.Result, error) {
	return cmdsurface.Result{Stdout: "ran\n"}, nil
}

// limitedBridge is gateBridge with the rate limit installed and rlRunner.
func limitedBridge(surface cmdsurface.Surface) (*cmdsurface.Bridge, *gateSink) {
	sink := &gateSink{}
	b := cmdsurface.New(gateTree(),
		cmdsurface.WithRunner(&rlRunner{}),
		cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnOK: true, OnError: true}),
		cmdsurface.WithRateLimit(oneAMinute),
	)
	b.Expose("*", surface)
	return b, sink
}

// assertLastAuditedRateLimited checks the refusal reached the sinks.
func assertLastAuditedRateLimited(t *testing.T, sink *gateSink) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		recs := sink.records()
		if len(recs) > 0 && errors.Is(recs[len(recs)-1], cmdsurface.ErrRateLimited) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no rate_limited audit record; got %v", sink.records())
}

// assertHTTPRateLimited checks a 429 rate_limited answer with a
// one-minute Retry-After.
func assertHTTPRateLimited(t *testing.T, resp *http.Response) {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want 60", got)
	}
	var ae api.APIError
	if err := json.Unmarshal(body, &ae); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	if ae.Code != api.CodeRateLimited {
		t.Errorf("code = %q, want %q", ae.Code, api.CodeRateLimited)
	}
}

func httpSend(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func drain(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("first call: status %d, want %d; body=%s", resp.StatusCode, want, body)
	}
}

func TestRateLimitSurface_Projection(t *testing.T) {
	for _, route := range []string{"/v1/commands/widget/add", "/v1/commands/widget/add/stream"} {
		t.Run(route, func(t *testing.T) {
			sink := &auditSink{}
			b := cmdsurface.New(projectionTree(false),
				cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnOK: true, OnError: true}),
				cmdsurface.WithRateLimit(oneAMinute))
			b.Expose("*", cmdsurface.SurfaceREST)
			r := api.NewRouter()
			if err := cmdsurface.MountProjection(b, r); err != nil {
				t.Fatal(err)
			}
			url := serve(t, r)
			drain(t, httpSend(t, http.MethodPost, url+"/v1/commands/widget/add", `{"flags":{"name":"a"}}`), http.StatusOK)
			assertHTTPRateLimited(t, httpSend(t, http.MethodPost, url+route, `{"flags":{"name":"b"}}`))
			recs := sink.records()
			if last := recs[len(recs)-1]; !errors.Is(last.err, cmdsurface.ErrRateLimited) {
				t.Fatalf("refusal not audited: %+v", recs)
			}
		})
	}
}

func TestRateLimitSurface_RESTAndSSE(t *testing.T) {
	t.Run("rest", func(t *testing.T) {
		b, sink := limitedBridge(cmdsurface.SurfaceREST)
		url, stop := newServer(t, b)
		defer stop()
		drain(t, httpSend(t, http.MethodPost, url+"/cmd/open", "{}"), http.StatusOK)
		assertHTTPRateLimited(t, httpSend(t, http.MethodPost, url+"/cmd/open", "{}"))
		assertLastAuditedRateLimited(t, sink)
	})
	t.Run("sse", func(t *testing.T) {
		b, sink := limitedBridge(cmdsurface.SurfaceSSE)
		srv, stop := sseTestServer(t, b)
		defer stop()
		drain(t, sseGetStream(t, srv.URL+"/cmd/open/stream", nil), http.StatusOK)
		assertHTTPRateLimited(t, sseGetStream(t, srv.URL+"/cmd/open/stream", nil))
		assertLastAuditedRateLimited(t, sink)
	})
}

func TestRateLimitSurface_WS(t *testing.T) {
	b, sink := limitedBridge(cmdsurface.SurfaceWS)
	f := mountGateWS(t, b)
	c := f.dialOK(t, "/ws/cmd", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i, want := range []string{"result", "error"} {
		wsWriteJSON(t, ctx, c, wsRawFrame{Op: "invoke", ID: "r", Invocation: &cmdsurface.Invocation{Path: []string{"open"}}})
		frame := readFirstResultOrError(t, ctx, c)
		if frame.Op != want {
			t.Fatalf("call %d: frame %+v, want %s", i+1, frame, want)
		}
		if want == "error" && (frame.Error == nil || frame.Error.Code != api.CodeRateLimited) {
			t.Fatalf("error = %+v, want code rate_limited", frame.Error)
		}
	}
	assertLastAuditedRateLimited(t, sink)
}

func TestRateLimitSurface_RPC(t *testing.T) {
	check := func(t *testing.T, err error) {
		t.Helper()
		if connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("code = %v (err %v), want ResourceExhausted", connect.CodeOf(err), err)
		}
		var ce *connect.Error
		if !errors.As(err, &ce) || ce.Meta().Get("Retry-After") != "60" {
			t.Fatalf("Retry-After metadata = %q, want 60", ce.Meta().Get("Retry-After"))
		}
	}
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		t.Run("invoke", func(t *testing.T) {
			b, sink := limitedBridge(cmdsurface.SurfaceRPC)
			client := mountGateRPC(t, b, p)
			if _, err := client.Invoke(context.Background(), invocation("open")); err != nil {
				t.Fatalf("first call: %v", err)
			}
			_, err := client.Invoke(context.Background(), invocation("open"))
			check(t, err)
			assertLastAuditedRateLimited(t, sink)
		})
		t.Run("stream", func(t *testing.T) {
			b, sink := limitedBridge(cmdsurface.SurfaceRPC)
			client := mountGateRPC(t, b, p)
			if _, err := client.Invoke(context.Background(), invocation("open")); err != nil {
				t.Fatalf("first call: %v", err)
			}
			stream, err := client.InvokeStream(context.Background(), invocation("open"))
			if err == nil {
				for stream.Receive() {
					t.Fatal("a refused stream sent a message")
				}
				err = stream.Err()
				_ = stream.Close()
			}
			check(t, err)
			assertLastAuditedRateLimited(t, sink)
		})
	})
}
