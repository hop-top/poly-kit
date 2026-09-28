package cmdsurface_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
)

// limitAuditSink records every audit emission so a test can assert a
// size-cap refusal reached the bridge's sinks.
type limitAuditSink struct {
	mu   sync.Mutex
	invs []cmdsurface.Invocation
	errs []error
}

func (s *limitAuditSink) Emit(_ context.Context, inv cmdsurface.Invocation, _ cmdsurface.Result, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invs = append(s.invs, inv)
	s.errs = append(s.errs, err)
	return nil
}

func (s *limitAuditSink) spec() cmdsurface.SinkSpec {
	return cmdsurface.SinkSpec{Sink: s, OnOK: true, OnError: true}
}

// tooLarge returns the size-cap refusals recorded so far.
func (s *limitAuditSink) tooLarge() []cmdsurface.Invocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []cmdsurface.Invocation
	for i, err := range s.errs {
		if errors.Is(err, cmdsurface.ErrBodyTooLarge) {
			out = append(out, s.invs[i])
		}
	}
	return out
}

// eventuallyTooLarge waits for n refusals; the WS refusal is audited
// from the server's connection goroutine.
func (s *limitAuditSink) eventuallyTooLarge(t *testing.T, n int) []cmdsurface.Invocation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := s.tooLarge()
		if len(got) >= n || time.Now().After(deadline) {
			if len(got) != n {
				t.Fatalf("size-cap audit records = %d, want %d", len(got), n)
			}
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// padTo grows the "x" run in tmpl (which contains PAD) until the
// rendered string is exactly n bytes.
func padTo(t *testing.T, tmpl string, n int) string {
	t.Helper()
	base := len(tmpl) - len("PAD")
	if n < base {
		t.Fatalf("target %d below template size %d", n, base)
	}
	return strings.Replace(tmpl, "PAD", strings.Repeat("x", n-base), 1)
}

// chunked hides the reader type so net/http sends the body with
// Transfer-Encoding: chunked and no Content-Length.
type chunked struct{ io.Reader }

func doPost(t *testing.T, url string, body io.Reader) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

type bodyCase struct {
	name string
	size int
	wrap func(string) io.Reader
	over bool
}

func bodyCases(limit int) []bodyCase {
	sized := func(s string) io.Reader { return strings.NewReader(s) }
	unsized := func(s string) io.Reader { return chunked{strings.NewReader(s)} }
	return []bodyCase{
		{"sized at limit", limit, sized, false},
		{"sized one over", limit + 1, sized, true},
		{"chunked at limit", limit, unsized, false},
		{"chunked one over", limit + 1, unsized, true},
		{"chunked far over", 8 * limit, unsized, true},
	}
}

func TestRESTBodyLimit(t *testing.T) {
	const limit = 256
	for _, c := range bodyCases(limit) {
		t.Run(c.name, func(t *testing.T) {
			sink := &limitAuditSink{}
			runner := &recordingRunner{}
			b := cmdsurface.New(testTree(), cmdsurface.WithRunner(runner), cmdsurface.WithSinks(sink.spec())).
				Expose("ping", cmdsurface.SurfaceREST)
			url, stop := newServer(t, b, cmdsurface.WithRESTMaxBodyBytes(limit))
			defer stop()

			body := padTo(t, `{"args":["PAD"]}`, c.size)
			status, resp := doPost(t, url+"/cmd/ping", c.wrap(body))
			if !c.over {
				if status != http.StatusOK {
					t.Fatalf("status=%d want 200; body=%s", status, resp)
				}
				if len(runner.captured()) != 1 {
					t.Fatalf("runner calls=%d want 1", len(runner.captured()))
				}
				return
			}
			assertAPITooLarge(t, status, resp)
			if n := len(runner.captured()); n != 0 {
				t.Fatalf("runner calls=%d; an oversized body must never run", n)
			}
			got := sink.eventuallyTooLarge(t, 1)
			if got[0].Meta.Surface != cmdsurface.SurfaceREST || strings.Join(got[0].Path, " ") != "ping" {
				t.Fatalf("audit = %+v, want REST refusal for ping", got[0])
			}
		})
	}
}

func TestRESTBodyLimitDefault(t *testing.T) {
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(&recordingRunner{})).
		Expose("ping", cmdsurface.SurfaceREST)
	url, stop := newServer(t, b)
	defer stop()

	body := padTo(t, `{"args":["PAD"]}`, int(api.DefaultMaxBodyBytes)+1)
	status, resp := doPost(t, url+"/cmd/ping", chunked{strings.NewReader(body)})
	assertAPITooLarge(t, status, resp)
}

func TestProjectionBodyLimit(t *testing.T) {
	// MountProjection caps its command routes and their streams; an
	// oversized body never runs and is audited against the command
	// the URL addresses.
	const limit = 256
	for _, route := range []string{"/v1/commands/widget/add", "/v1/commands/widget/add/stream"} {
		for _, c := range bodyCases(limit) {
			t.Run(route+"/"+c.name, func(t *testing.T) {
				sink := &limitAuditSink{}
				b := cmdsurface.New(projectionTree(false), cmdsurface.WithSinks(sink.spec()))
				b.Expose("*", cmdsurface.SurfaceREST)
				r := api.NewRouter()
				if err := cmdsurface.MountProjection(b, r, cmdsurface.WithProjectionMaxBodyBytes(limit)); err != nil {
					t.Fatalf("MountProjection: %v", err)
				}
				url := serve(t, r)

				body := padTo(t, `{"flags":{"name":"PAD"}}`, c.size)
				status, resp := doPost(t, url+route, c.wrap(body))
				if !c.over {
					if status != http.StatusOK {
						t.Fatalf("status=%d want 200; body=%s", status, resp)
					}
					return
				}
				assertAPITooLarge(t, status, resp)
				got := sink.eventuallyTooLarge(t, 1)
				if got[0].Meta.Surface != cmdsurface.SurfaceREST || strings.Join(got[0].Path, " ") != "widget add" {
					t.Fatalf("audit = %+v, want REST refusal for widget add", got[0])
				}
				if n := len(sink.invs); n != 1 {
					t.Fatalf("audit records=%d; an oversized body must never run", n)
				}
			})
		}
	}
}

func TestProjectionBodyLimitDefaultAndOff(t *testing.T) {
	b := cmdsurface.New(projectionTree(false))
	b.Expose("*", cmdsurface.SurfaceREST)
	body := padTo(t, `{"flags":{"name":"PAD"}}`, int(api.DefaultMaxBodyBytes)+1)

	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	status, resp := doPost(t, serve(t, r)+"/v1/commands/widget/add", chunked{strings.NewReader(body)})
	assertAPITooLarge(t, status, resp)

	// A negative cap leaves bodies to the router.
	r = api.NewRouter()
	if err := cmdsurface.MountProjection(b, r, cmdsurface.WithProjectionMaxBodyBytes(-1)); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	if status, resp := doPost(t, serve(t, r)+"/v1/commands/widget/add", chunked{strings.NewReader(body)}); status != http.StatusOK {
		t.Fatalf("uncapped: status=%d body=%.200s", status, resp)
	}
}

func assertAPITooLarge(t *testing.T, status int, body []byte) {
	t.Helper()
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413; body=%s", status, body)
	}
	var e api.APIError
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	if e.Code != api.CodeBodyTooLarge {
		t.Fatalf("code=%q want %q", e.Code, api.CodeBodyTooLarge)
	}
}

func TestMCPBodyLimit(t *testing.T) {
	const limit = 256
	for _, c := range bodyCases(limit) {
		t.Run(c.name, func(t *testing.T) {
			sink := &limitAuditSink{}
			b := cmdsurface.New(testTree(), cmdsurface.WithRunner(&recordingRunner{}), cmdsurface.WithSinks(sink.spec())).
				Expose("ping", cmdsurface.SurfaceMCP)
			r := api.NewRouter()
			if err := cmdsurface.MountMCP(b, r, cmdsurface.WithMCPMaxBodyBytes(limit)); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(r)
			defer srv.Close()

			body := padTo(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"pad":"PAD"}}`, c.size)
			status, resp := doPost(t, srv.URL+"/mcp", c.wrap(body))

			var rpc struct {
				ID     json.RawMessage `json:"id"`
				Result map[string]any  `json:"result"`
				Error  *struct {
					Code    int            `json:"code"`
					Message string         `json:"message"`
					Data    map[string]any `json:"data"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp, &rpc); err != nil {
				t.Fatalf("decode: %v; body=%s", err, resp)
			}
			if !c.over {
				if status != http.StatusOK || rpc.Error != nil || rpc.Result == nil {
					t.Fatalf("status=%d body=%s; want a tools/list result", status, resp)
				}
				return
			}
			if status != http.StatusRequestEntityTooLarge {
				t.Fatalf("status=%d want 413; body=%s", status, resp)
			}
			if rpc.Error == nil || rpc.Error.Code != -32600 || rpc.Error.Data["reason"] != api.CodeBodyTooLarge {
				t.Fatalf("error=%+v; want -32600 body_too_large", rpc.Error)
			}
			if rpc.Error.Data["limit"] != float64(limit) {
				t.Fatalf("data.limit=%v want %d", rpc.Error.Data["limit"], limit)
			}
			got := sink.eventuallyTooLarge(t, 1)
			if got[0].Meta.Surface != cmdsurface.SurfaceMCP {
				t.Fatalf("audit surface=%q want mcp", got[0].Meta.Surface)
			}
		})
	}
}

// invocationOfSize returns an RPC invocation of echo whose binary
// proto encoding is exactly n bytes.
func invocationOfSize(t *testing.T, n int) *cmdsurfacev1.Invocation {
	t.Helper()
	for pad := 0; pad <= n; pad++ {
		m := &cmdsurfacev1.Invocation{Path: []string{"echo"}, Args: []string{strings.Repeat("x", pad)}}
		if proto.Size(m) == n {
			return m
		}
	}
	t.Fatalf("no invocation encodes to %d bytes", n)
	return nil
}

func TestRPCBodyLimit(t *testing.T) {
	const limit = 256
	binary := map[string]bool{"connect+proto": true, "grpc": true, "grpc-web": true}
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.start(cmdsurface.WithRPCMaxBodyBytes(limit))
		cl := f.client(p)
		ctx := context.Background()

		under := &cmdsurfacev1.Invocation{Path: []string{"echo"}, Args: []string{strings.Repeat("x", limit/4)}}
		if _, err := cl.Invoke(ctx, connect.NewRequest(under)); err != nil {
			t.Fatalf("under the cap: %v", err)
		}
		over := &cmdsurfacev1.Invocation{Path: []string{"echo"}, Args: []string{strings.Repeat("x", 2*limit)}}
		_, err := cl.Invoke(ctx, connect.NewRequest(over))
		if connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("over the cap: code=%v err=%v; want resource_exhausted", connect.CodeOf(err), err)
		}

		if !binary[p.name] {
			return
		}
		// Binary protocols carry the proto encoding verbatim, so the
		// boundary is exact: at the cap passes, one byte over fails.
		if _, err := cl.Invoke(ctx, connect.NewRequest(invocationOfSize(t, limit))); err != nil {
			t.Fatalf("exactly at the cap: %v", err)
		}
		_, err = cl.Invoke(ctx, connect.NewRequest(invocationOfSize(t, limit+1)))
		if connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("one byte over: code=%v err=%v; want resource_exhausted", connect.CodeOf(err), err)
		}
	})
}

func TestRPCBodyLimitDefault(t *testing.T) {
	f := newFixture(t)
	f.start()
	cl := f.client(wireProtocols[0])
	big := &cmdsurfacev1.Invocation{Path: []string{"echo"}, Args: []string{strings.Repeat("x", int(api.DefaultMaxBodyBytes))}}
	_, err := cl.Invoke(context.Background(), connect.NewRequest(big))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code=%v err=%v; want resource_exhausted at the default cap", connect.CodeOf(err), err)
	}
}

func newWSLimitServer(t *testing.T, sink *limitAuditSink, opts ...cmdsurface.WSOption) string {
	t.Helper()
	br := cmdsurface.New(wsTestTree(),
		cmdsurface.WithRunner(newWSFakeRunner()),
		cmdsurface.WithSinks(sink.spec()),
	)
	br.Expose("hello", cmdsurface.SurfaceWS)
	r := api.NewRouter()
	if err := cmdsurface.MountWS(br, r, opts...); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/cmd"
}

func TestWSMessageLimit(t *testing.T) {
	const limit = 256
	frame := `{"op":"invoke","id":"1","invocation":{"path":["hello"],"args":["PAD"]}}`

	t.Run("at limit runs", func(t *testing.T) {
		sink := &limitAuditSink{}
		url := newWSLimitServer(t, sink, cmdsurface.WithWSMaxMessageBytes(limit))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

		if err := c.Write(ctx, websocket.MessageText, []byte(padTo(t, frame, limit))); err != nil {
			t.Fatal(err)
		}
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var f struct{ Op string }
			_ = json.Unmarshal(data, &f)
			if f.Op == "result" {
				break
			}
			if f.Op == "error" {
				t.Fatalf("error frame: %s", data)
			}
		}
		if n := len(sink.tooLarge()); n != 0 {
			t.Fatalf("size-cap audits=%d want 0", n)
		}
	})

	over := func(t *testing.T, size int, opts ...cmdsurface.WSOption) {
		sink := &limitAuditSink{}
		url := newWSLimitServer(t, sink, opts...)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.CloseNow() }()

		if err := c.Write(ctx, websocket.MessageText, []byte(padTo(t, frame, size))); err != nil {
			t.Fatal(err)
		}
		_, _, err = c.Read(ctx)
		if got := websocket.CloseStatus(err); got != websocket.StatusMessageTooBig {
			t.Fatalf("close status=%v err=%v; want 1009 message too big", got, err)
		}
		got := sink.eventuallyTooLarge(t, 1)
		if got[0].Meta.Surface != cmdsurface.SurfaceWS {
			t.Fatalf("audit surface=%q want ws", got[0].Meta.Surface)
		}
	}
	t.Run("one over closes 1009", func(t *testing.T) {
		over(t, limit+1, cmdsurface.WithWSMaxMessageBytes(limit))
	})
	t.Run("default cap", func(t *testing.T) {
		over(t, int(cmdsurface.DefaultWSMaxMessageBytes)+1)
	})
}
