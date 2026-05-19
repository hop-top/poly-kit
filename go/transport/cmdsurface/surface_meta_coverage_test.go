package cmdsurface_test

// Coverage test for T-0679 (cmdsurf-telemetry): every surface's
// inbound-decode path MUST populate Meta.RequestedAt with a non-zero
// timestamp and Meta.Surface with a non-empty Surface enum value
// BEFORE the Invocation reaches the Runner / Bridge.
//
// This table drives one inbound flow per surface and asserts on the
// Invocation the Runner observes. Surfaces whose inbound machinery is
// too heavy to assemble inline (signed, oauth, ws, rpc) skip with a
// TODO referencing T-0679; the assignment for those is already covered
// by their own TestXxx_HappyPath tests in this package — they would
// fail their own end-to-end runs if RequestedAt / Surface drifted.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// metaCapRunner is a tiny Runner that records every Invocation it
// observes. Each surface case in the table builds its own bridge with
// this runner, drives one inbound flow, and asserts on the captured
// value.
type metaCapRunner struct {
	mu  sync.Mutex
	got []cmdsurface.Invocation
}

func (r *metaCapRunner) Run(_ context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	r.mu.Lock()
	r.got = append(r.got, inv)
	r.mu.Unlock()
	return cmdsurface.Result{Stdout: "ok"}, nil
}

func (r *metaCapRunner) Stream(_ context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	r.mu.Lock()
	r.got = append(r.got, inv)
	r.mu.Unlock()
	close(out)
	return nil
}

func (r *metaCapRunner) last() (cmdsurface.Invocation, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.got) == 0 {
		return cmdsurface.Invocation{}, false
	}
	return r.got[len(r.got)-1], true
}

// metaCovTree builds a tiny cobra root with a single read-only "ping"
// leaf used by every case in the coverage table. Per-surface tests
// already cover safety / destructive branches; this test asserts only
// on the Meta fields populated on the happy path.
func metaCovTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use:         "ping",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	})
	return root
}

// assertMeta is the shared assertion: RequestedAt non-zero, Surface
// matches expected, RequestedAt at or after sentAt.
func assertMeta(t *testing.T, runner *metaCapRunner, wantSurface cmdsurface.Surface, sentAt time.Time) {
	t.Helper()
	got, ok := runner.last()
	if !ok {
		t.Fatalf("runner captured no invocation")
	}
	if got.Meta.Surface == "" {
		t.Errorf("Meta.Surface is empty; want non-empty")
	}
	if got.Meta.Surface != wantSurface {
		t.Errorf("Meta.Surface=%q want=%q", got.Meta.Surface, wantSurface)
	}
	if got.Meta.RequestedAt.IsZero() {
		t.Errorf("Meta.RequestedAt is zero; want non-zero")
	}
	if !got.Meta.RequestedAt.IsZero() && got.Meta.RequestedAt.Before(sentAt) {
		t.Errorf("Meta.RequestedAt=%v before sentAt=%v", got.Meta.RequestedAt, sentAt)
	}
}

// metaCovCase is one row in the table. drive must trigger exactly one
// successful inbound call against the runner; wantSurface is the
// canonical Surface the surface forces on the Invocation.
type metaCovCase struct {
	name        string
	wantSurface cmdsurface.Surface
	drive       func(t *testing.T, runner *metaCapRunner)
	skip        string // non-empty → t.Skip(skip)
}

func TestEverySurfaceSetsMeta(t *testing.T) {
	cases := []metaCovCase{
		{
			name:        "lib",
			wantSurface: cmdsurface.SurfaceLib,
			drive: func(t *testing.T, runner *metaCapRunner) {
				b := cmdsurface.New(metaCovTree(), cmdsurface.WithRunner(runner)).
					Expose("ping", cmdsurface.SurfaceLib)
				if _, err := cmdsurface.InvokeArgs(context.Background(), b, []string{"ping"}); err != nil {
					t.Fatalf("InvokeArgs: %v", err)
				}
			},
		},
		{
			name:        "rest",
			wantSurface: cmdsurface.SurfaceREST,
			drive: func(t *testing.T, runner *metaCapRunner) {
				b := cmdsurface.New(metaCovTree(), cmdsurface.WithRunner(runner)).
					Expose("ping", cmdsurface.SurfaceREST)
				r := api.NewRouter()
				if err := cmdsurface.MountREST(b, r); err != nil {
					t.Fatalf("MountREST: %v", err)
				}
				srv := httptest.NewServer(r)
				t.Cleanup(srv.Close)
				body, _ := json.Marshal(cmdsurface.Invocation{})
				resp, err := http.Post(srv.URL+"/cmd/ping", "application/json", bytes.NewReader(body))
				if err != nil {
					t.Fatalf("POST: %v", err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status=%d want 200", resp.StatusCode)
				}
			},
		},
		{
			name:        "webhook",
			wantSurface: cmdsurface.SurfaceWebhook,
			drive: func(t *testing.T, runner *metaCapRunner) {
				b := cmdsurface.New(metaCovTree(), cmdsurface.WithRunner(runner)).
					Expose("ping", cmdsurface.SurfaceWebhook)
				r := api.NewRouter()
				err := cmdsurface.MountWebhooks(b, r, []cmdsurface.WebhookMapping{{
					Name: "ping",
					Path: []string{"ping"},
					Auth: cmdsurface.AuthNone{},
				}})
				if err != nil {
					t.Fatalf("MountWebhooks: %v", err)
				}
				srv := httptest.NewServer(r)
				t.Cleanup(srv.Close)
				resp, err := http.Post(srv.URL+"/hooks/ping", "application/json", strings.NewReader(`{}`))
				if err != nil {
					t.Fatalf("POST: %v", err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusAccepted {
					t.Fatalf("status=%d want 202", resp.StatusCode)
				}
			},
		},
		{
			name:        "bus",
			wantSurface: cmdsurface.SurfaceBus,
			drive: func(t *testing.T, runner *metaCapRunner) {
				b := cmdsurface.New(metaCovTree(), cmdsurface.WithRunner(runner)).
					Expose("ping", cmdsurface.SurfaceBus)
				bus := newMetaCovBus()
				cleanup, err := cmdsurface.MountBus(b, bus, bus, []cmdsurface.BusBinding{{
					Path:         []string{"ping"},
					RequestTopic: "cmd.ping.req",
				}})
				if err != nil {
					t.Fatalf("MountBus: %v", err)
				}
				t.Cleanup(cleanup)
				bus.deliver(cmdsurface.BusMessage{Topic: "cmd.ping.req", Payload: []byte(`{}`)})
			},
		},
		{
			name:        "mcp",
			wantSurface: cmdsurface.SurfaceMCP,
			drive: func(t *testing.T, runner *metaCapRunner) {
				b := cmdsurface.New(metaCovTree(), cmdsurface.WithRunner(runner)).
					Expose("ping", cmdsurface.SurfaceMCP)
				r := api.NewRouter()
				if err := cmdsurface.MountMCP(b, r); err != nil {
					t.Fatalf("MountMCP: %v", err)
				}
				srv := httptest.NewServer(r)
				t.Cleanup(srv.Close)
				body, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"method":  "tools/call",
					"params":  map[string]any{"name": "ping"},
				})
				resp, err := http.Post(srv.URL+"/mcp", "application/json", bytes.NewReader(body))
				if err != nil {
					t.Fatalf("POST: %v", err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			},
		},
		{
			name:        "cron",
			wantSurface: cmdsurface.SurfaceCron,
			drive: func(t *testing.T, runner *metaCapRunner) {
				b := cmdsurface.New(metaCovTree(), cmdsurface.WithRunner(runner)).
					Expose("ping", cmdsurface.SurfaceCron)
				eng := newMetaCovCronEngine()
				cleanup, err := cmdsurface.MountCron(b, eng, []cmdsurface.CronSchedule{
					{Path: []string{"ping"}, Expr: "* * * * *"},
				})
				if err != nil {
					t.Fatalf("MountCron: %v", err)
				}
				t.Cleanup(cleanup)
				eng.fireAll()
			},
		},
		{
			name:        "sse",
			wantSurface: cmdsurface.SurfaceSSE,
			drive: func(t *testing.T, runner *metaCapRunner) {
				b := cmdsurface.New(metaCovTree(), cmdsurface.WithRunner(runner)).
					Expose("ping", cmdsurface.SurfaceSSE)
				r := api.NewRouter()
				if err := cmdsurface.MountSSE(b, r); err != nil {
					t.Fatalf("MountSSE: %v", err)
				}
				srv := httptest.NewServer(r)
				t.Cleanup(srv.Close)
				resp, err := http.Get(srv.URL + "/cmd/ping/stream")
				if err != nil {
					t.Fatalf("GET: %v", err)
				}
				// Drain the SSE stream so the Runner.Stream goroutine
				// completes before we sample runner.last().
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			},
		},
		{
			name:        "faas-lambda-direct",
			wantSurface: cmdsurface.SurfaceFaaS,
			drive: func(t *testing.T, runner *metaCapRunner) {
				b := cmdsurface.New(metaCovTree(), cmdsurface.WithRunner(runner)).
					Expose("ping", cmdsurface.SurfaceFaaS)
				h, err := cmdsurface.LambdaHandler(b, cmdsurface.LambdaConfig{
					Event: cmdsurface.EventDirect,
				})
				if err != nil {
					t.Fatalf("LambdaHandler: %v", err)
				}
				payload, _ := json.Marshal(cmdsurface.Invocation{Path: []string{"ping"}})
				if _, err := h(context.Background(), payload); err != nil {
					t.Fatalf("handler: %v", err)
				}
			},
		},
		// Inbound flows below need substantial extra wiring (HMAC key +
		// nonce store, OAuth provider + cookie state, WS upgrade, Connect
		// codec). Each surface's own *_test.go file already exercises
		// the corresponding TestXxx_HappyPath and would catch a missing
		// RequestedAt or Surface assignment at end-to-end run time. Skip
		// here with a TODO referencing T-0679; revisit if/when the
		// inbound machinery gains an easier in-process driver.
		{
			name:        "signed",
			wantSurface: cmdsurface.SurfaceSigned,
			skip:        "T-0679: signed inbound needs HMAC key + NonceStore; covered by surface_signed_test.go",
		},
		{
			name:        "oauth-cb",
			wantSurface: cmdsurface.SurfaceOAuthCB,
			skip:        "T-0679: oauth-cb inbound needs OAuthProvider + StateStore + cookie; covered by surface_oauth_test.go",
		},
		{
			name:        "ws",
			wantSurface: cmdsurface.SurfaceWS,
			skip:        "T-0679: ws inbound needs websocket upgrade + frame protocol; covered by surface_ws_test.go",
		},
		{
			name:        "rpc",
			wantSurface: cmdsurface.SurfaceRPC,
			skip:        "T-0679: rpc inbound needs Connect codec + handler server; covered by surface_rpc_test.go",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if c.skip != "" {
				t.Skip(c.skip)
			}
			runner := &metaCapRunner{}
			sentAt := time.Now()
			c.drive(t, runner)
			assertMeta(t, runner, c.wantSurface, sentAt)
		})
	}
}

// metaCovBus is a minimal in-memory Subscriber + EventPublisher used
// by the bus case. It mirrors busFakeBus in surface_bus_test.go but
// is intentionally tiny — only Subscribe + synchronous deliver matter
// here.
type metaCovBus struct {
	mu       sync.Mutex
	handlers map[string][]func(cmdsurface.BusMessage) error
}

func newMetaCovBus() *metaCovBus {
	return &metaCovBus{handlers: map[string][]func(cmdsurface.BusMessage) error{}}
}

func (b *metaCovBus) Subscribe(_ context.Context, topic string, h func(cmdsurface.BusMessage) error) (func(), error) {
	b.mu.Lock()
	b.handlers[topic] = append(b.handlers[topic], h)
	b.mu.Unlock()
	return func() {}, nil
}

func (b *metaCovBus) Publish(_ context.Context, _ string, _ string, _ any) error { return nil }

func (b *metaCovBus) deliver(msg cmdsurface.BusMessage) {
	b.mu.Lock()
	hs := append([]func(cmdsurface.BusMessage) error(nil), b.handlers[msg.Topic]...)
	b.mu.Unlock()
	for _, h := range hs {
		_ = h(msg)
	}
}

// metaCovCronEngine is a CronEngine that captures registered job
// funcs and lets the test fire them on demand. Mirrors cronTestEngine
// in surface_cron_test.go but minimized to what the coverage case
// needs.
type metaCovCronEngine struct {
	mu  sync.Mutex
	fns []func()
}

func newMetaCovCronEngine() *metaCovCronEngine {
	return &metaCovCronEngine{}
}

func (e *metaCovCronEngine) Schedule(_ string, _ *time.Location, fn func()) (func(), error) {
	e.mu.Lock()
	e.fns = append(e.fns, fn)
	e.mu.Unlock()
	return func() {}, nil
}

func (e *metaCovCronEngine) Start() {}

func (e *metaCovCronEngine) Stop(_ context.Context) error { return nil }

func (e *metaCovCronEngine) fireAll() {
	e.mu.Lock()
	fns := append([]func(){}, e.fns...)
	e.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}
