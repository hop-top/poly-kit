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

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// projectionTree is a bare cobra tree (no kit root):
//
//	root
//	├── widget add     (write, --name)
//	├── widget list    (read)
//	├── widget purge   (destructive)
//	├── widget secure  (write, auth-required) — only when secure is set
//	└── ping           (read)
func projectionTree(secure bool) *cobra.Command {
	root := &cobra.Command{Use: "tool"}
	widget := &cobra.Command{Use: "widget", Short: "Widgets"}
	add := &cobra.Command{
		Use:         "add",
		Short:       "Add a widget",
		Annotations: map[string]string{"kit/side-effect": "write"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _ := cmd.Flags().GetString("name")
			cmd.Println("added " + name)
			return nil
		},
	}
	add.Flags().String("name", "", "widget name")
	list := &cobra.Command{
		Use:         "list",
		Short:       "List widgets",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("a b")
			return nil
		},
	}
	purge := &cobra.Command{
		Use:         "purge",
		Short:       "Purge widgets",
		Annotations: map[string]string{"kit/side-effect": "destructive"},
		RunE:        func(*cobra.Command, []string) error { return nil },
	}
	widget.AddCommand(add, list, purge)
	if secure {
		widget.AddCommand(&cobra.Command{
			Use:   "secure",
			Short: "Touch a secure widget",
			Annotations: map[string]string{
				"kit/side-effect":   "write",
				"kit/auth-required": "true",
			},
			RunE: func(cmd *cobra.Command, _ []string) error {
				cmd.Println("secured")
				return nil
			},
		})
	}
	ping := &cobra.Command{
		Use:         "ping",
		Short:       "Ping",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("pong")
			return nil
		},
	}
	root.AddCommand(widget, ping)
	return root
}

// auditRecord is one sink emission.
type auditRecord struct {
	inv cmdsurface.Invocation
	err error
}

// auditSink records every emission.
type auditSink struct {
	mu   sync.Mutex
	recs []auditRecord
}

func (s *auditSink) Emit(_ context.Context, inv cmdsurface.Invocation, _ cmdsurface.Result, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, auditRecord{inv: inv, err: err})
	return nil
}

func (s *auditSink) records() []auditRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]auditRecord(nil), s.recs...)
}

// projectionBridge exposes the whole tree on REST, keeps ping off it,
// and records every audit emission.
func projectionBridge(secure bool) (*cmdsurface.Bridge, *auditSink) {
	sink := &auditSink{}
	b := cmdsurface.New(projectionTree(secure), cmdsurface.WithSinks(cmdsurface.SinkSpec{
		Sink: sink, OnOK: true, OnError: true,
	}))
	b.Expose("*", cmdsurface.SurfaceREST)
	b.Hide("ping", cmdsurface.SurfaceREST)
	return b, sink
}

func do(t *testing.T, method, url, body string, headers map[string]string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// response is what doResp read.
type response struct {
	status int
	header http.Header
	body   string
}

// doResp is do for a caller that also reads the response headers.
func doResp(t *testing.T, method, url string, headers map[string]string) response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: string(raw)}
}

func serve(t *testing.T, r *api.Router) string {
	t.Helper()
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestMountProjection_ServesTheBridgeUnderV1Commands(t *testing.T) {
	b, _ := projectionBridge(false)
	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r,
		cmdsurface.WithProjectionTool("tool", "1.2.3")); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	url := serve(t, r)

	status, body := do(t, http.MethodPost, url+"/v1/commands/widget/add",
		`{"flags":{"name":"foo"}}`, nil)
	if status != http.StatusOK {
		t.Fatalf("POST widget/add: status=%d body=%s", status, body)
	}
	var res api.CommandResult
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if !strings.Contains(res.Stdout, "added foo") {
		t.Errorf("stdout=%q, want it to contain %q", res.Stdout, "added foo")
	}

	// A read command is a GET, per the method rules.
	if status, body := do(t, http.MethodGet, url+"/v1/commands/widget/list", "", nil); status != http.StatusOK {
		t.Errorf("GET widget/list: status=%d body=%s", status, body)
	}
	if status, _ := do(t, http.MethodPost, url+"/v1/commands/widget/list", "", nil); status != http.StatusMethodNotAllowed {
		t.Errorf("POST widget/list: status=%d, want 405", status)
	}
	// Withheld commands are described, not mounted.
	for _, path := range []string{"/v1/commands/widget/purge", "/v1/commands/ping"} {
		if status, _ := do(t, http.MethodPost, url+path, "", nil); status != http.StatusNotFound &&
			status != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: status=%d, want it unmounted", path, status)
		}
	}
}

func TestMountProjection_DiscoveryCarriesEveryVerdict(t *testing.T) {
	b, _ := projectionBridge(false)
	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r,
		cmdsurface.WithProjectionTool("tool", "1.2.3")); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	status, body := do(t, http.MethodGet, serve(t, r)+"/v1/commands", "", nil)
	if status != http.StatusOK {
		t.Fatalf("discovery: status=%d body=%s", status, body)
	}
	var doc api.DiscoveryDocument
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Tool != "tool" || doc.Version != "1.2.3" {
		t.Errorf("tool=%q version=%q", doc.Tool, doc.Version)
	}
	got := map[string]api.DiscoveryEntry{}
	for _, e := range doc.Commands {
		got[strings.Join(e.Path, " ")] = e
	}
	want := map[string]string{
		"widget add":   "",
		"widget list":  "",
		"widget purge": "unauthorized-destructive",
		"ping":         cmdsurface.ReasonWithheldByConfig,
	}
	for key, reason := range want {
		e, ok := got[key]
		if !ok {
			t.Errorf("discovery lacks %q", key)
			continue
		}
		if e.Invocable != (reason == "") || e.Reason != reason {
			t.Errorf("%s: invocable=%v reason=%q, want reason %q", key, e.Invocable, e.Reason, reason)
		}
	}
	if _, ok := got["widget"]; ok {
		t.Error("a pure command group is listed as a command")
	}
}

func TestMountProjection_StreamsThroughTheBridge(t *testing.T) {
	b, sink := projectionBridge(false)
	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	status, body := do(t, http.MethodPost, serve(t, r)+"/v1/commands/widget/add/stream",
		`{"flags":{"name":"bar"}}`, nil)
	if status != http.StatusOK {
		t.Fatalf("stream: status=%d body=%s", status, body)
	}
	if !strings.Contains(body, "event: result") || !strings.Contains(body, "added bar") {
		t.Errorf("stream body lacks the output or the result frame:\n%s", body)
	}
	recs := sink.records()
	if len(recs) != 1 || recs[0].inv.Meta.Surface != cmdsurface.SurfaceREST {
		t.Fatalf("audit records=%+v, want one REST record", recs)
	}
}

func TestMountProjection_MinimalSpecWithoutHuma(t *testing.T) {
	b, _ := projectionBridge(false)
	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	status, body := do(t, http.MethodGet, serve(t, r)+api.OpenAPISpecPath, "", nil)
	if status != http.StatusOK {
		t.Fatalf("spec: status=%d", status)
	}
	if !strings.Contains(body, "/v1/commands/widget/add") {
		t.Errorf("minimal spec lacks the widget add route:\n%s", body)
	}
	if strings.Contains(body, "/v1/commands/widget/purge") {
		t.Error("minimal spec describes a withheld command")
	}
}

func TestMountProjection_DescribesIntoHumaSpec(t *testing.T) {
	b, _ := projectionBridge(false)
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{Title: "t", Version: "0"}))
	if err := cmdsurface.MountProjection(b, r); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	status, body := do(t, http.MethodGet, serve(t, r)+api.OpenAPISpecPath, "", nil)
	if status != http.StatusOK {
		t.Fatalf("spec: status=%d", status)
	}
	for _, id := range []string{
		api.OperationIDFor([]string{"widget", "add"}),
		api.StreamOperationIDFor([]string{"widget", "add"}),
	} {
		if !strings.Contains(body, id) {
			t.Errorf("spec lacks operation %q", id)
		}
	}
}

func TestMountProjection_RefusesUnauthenticatedAuthRequired(t *testing.T) {
	b, _ := projectionBridge(true)
	err := cmdsurface.MountProjection(b, api.NewRouter())
	if err == nil {
		t.Fatal("MountProjection mounted an auth-required command with nothing authenticating it")
	}
	for _, want := range []string{"widget secure", "WithProjectionAuth"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}

	// A command kept off REST is not served, so it needs no auth.
	b.Hide("widget secure", cmdsurface.SurfaceREST)
	if err := cmdsurface.MountProjection(b, api.NewRouter()); err != nil {
		t.Errorf("MountProjection with the auth-required command hidden: %v", err)
	}
}

func TestMountProjection_RouterAuthLiftsTheRefusal(t *testing.T) {
	b, _ := projectionBridge(true)
	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r, cmdsurface.WithProjectionRouterAuth()); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
}

// Declaring that the router authenticates vouches for nobody: without
// a verifier on the router an auth-required command is refused per
// call, whatever header the caller sends; with one it runs as the
// verified principal.
func TestMountProjection_RouterAuthStillNeedsAVerifiedRequest(t *testing.T) {
	b, sink := projectionBridge(true)
	bare := api.NewRouter()
	if err := cmdsurface.MountProjection(b, bare, cmdsurface.WithProjectionRouterAuth()); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	url := serve(t, bare)
	for _, hdr := range []map[string]string{nil, {"Authorization": "Bearer anything"}} {
		resp := doResp(t, http.MethodPost, url+"/v1/commands/widget/secure", hdr)
		if resp.status != http.StatusUnauthorized || !strings.Contains(resp.body, `"code":"unauthenticated"`) {
			t.Fatalf("header %v: status=%d body=%s, want 401 unauthenticated", hdr, resp.status, resp.body)
		}
		if resp.header.Get("WWW-Authenticate") == "" {
			t.Errorf("401 without a WWW-Authenticate challenge")
		}
	}
	for _, rec := range sink.records() {
		if !errors.Is(rec.err, cmdsurface.ErrAuthRefused) {
			t.Errorf("refusal audited as %v, want ErrAuthRefused", rec.err)
		}
	}
	// A streamed call is refused before the stream commits.
	if resp := doResp(t, http.MethodPost, url+"/v1/commands/widget/secure/stream", nil); resp.status != http.StatusUnauthorized {
		t.Errorf("stream: status=%d, want 401", resp.status)
	}
	// Commands without the annotation are untouched.
	if status, _ := do(t, http.MethodGet, url+"/v1/commands/widget/list", "", nil); status != http.StatusOK {
		t.Errorf("plain command: status=%d", status)
	}

	b2, sink2 := projectionBridge(true)
	verifying := api.NewRouter(api.WithMiddleware(api.Auth(func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") != "Bearer good" {
			return nil, errors.New("bad credential")
		}
		return api.Claims{Subject: "alice"}, nil
	})))
	if err := cmdsurface.MountProjection(b2, verifying, cmdsurface.WithProjectionRouterAuth()); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	status, body := do(t, http.MethodPost, serve(t, verifying)+"/v1/commands/widget/secure", "",
		map[string]string{"Authorization": "Bearer good"})
	if status != http.StatusOK {
		t.Fatalf("verified call: status=%d body=%s", status, body)
	}
	recs := sink2.records()
	if last := recs[len(recs)-1]; last.inv.Meta.Caller != "alice" ||
		last.inv.Meta.Established != cmdsurface.EstablishedVerified {
		t.Errorf("run meta=%+v, want alice, verified", last.inv.Meta)
	}
}

func TestMountProjection_WithAuthGuardsOnlyTheProjection(t *testing.T) {
	b, sink := projectionBridge(true)
	r := api.NewRouter(api.WithMiddleware(api.RequestID()))
	r.Handle(http.MethodGet, "/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	auth := func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") != "Bearer good" {
			return nil, errors.New("bad credential")
		}
		return api.Claims{Subject: "alice", Tenant: "acme"}, nil
	}
	if err := cmdsurface.MountProjection(b, r, cmdsurface.WithProjectionAuth(auth)); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	url := serve(t, r)

	if status, _ := do(t, http.MethodGet, url+"/health", "", nil); status != http.StatusNoContent {
		t.Errorf("/health: status=%d; WithProjectionAuth must not guard other routes", status)
	}
	for _, path := range []string{"/v1/commands", "/v1/commands/widget/secure/stream"} {
		method := http.MethodPost
		if path == "/v1/commands" {
			method = http.MethodGet
		}
		if status, _ := do(t, method, url+path, "", nil); status != http.StatusUnauthorized {
			t.Errorf("%s %s unauthenticated: status=%d, want 401", method, path, status)
		}
	}

	recs := sink.records()
	if len(recs) != 2 {
		t.Fatalf("audit records=%d, want one per refusal: %+v", len(recs), recs)
	}
	stream := recs[1]
	if !errors.Is(stream.err, cmdsurface.ErrAuthRefused) {
		t.Errorf("refusal err=%v, want ErrAuthRefused", stream.err)
	}
	if got := strings.Join(stream.inv.Path, " "); got != "widget secure" {
		t.Errorf("refusal path=%q, want the streamed command", got)
	}
	if stream.inv.Meta.Surface != cmdsurface.SurfaceREST || stream.inv.Meta.RequestID == "" {
		t.Errorf("refusal meta=%+v, want REST with a request id", stream.inv.Meta)
	}

	status, body := do(t, http.MethodPost, url+"/v1/commands/widget/secure", "",
		map[string]string{"Authorization": "Bearer good"})
	if status != http.StatusOK || !strings.Contains(body, "secured") {
		t.Fatalf("authenticated call: status=%d body=%s", status, body)
	}
	recs = sink.records()
	last := recs[len(recs)-1]
	if last.err != nil || last.inv.Meta.Caller != "alice" || last.inv.Meta.Tenant != "acme" {
		t.Errorf("run record=%+v, want alice@acme without error", last)
	}
}

func TestMountProjection_NilArguments(t *testing.T) {
	b, _ := projectionBridge(false)
	if err := cmdsurface.MountProjection(nil, api.NewRouter()); err == nil {
		t.Error("nil bridge accepted")
	}
	if err := cmdsurface.MountProjection(b, nil); err == nil {
		t.Error("nil router accepted")
	}
	if _, err := cmdsurface.Projection(nil); err == nil {
		t.Error("Projection accepted a nil bridge")
	}
}

// reservedNames reserves a fixed set of depth-1 verbs.
type reservedNames map[string]bool

func (r reservedNames) IsReserved(name string) bool { return r[name] }

func TestProjection_ReservedVerbsAreDescribedAndWithheld(t *testing.T) {
	b, _ := projectionBridge(false)
	b.Expose("ping", cmdsurface.SurfaceREST)
	pcfg, err := cmdsurface.Projection(b,
		cmdsurface.WithProjectionReserved(reservedNames{"ping": true}))
	if err != nil {
		t.Fatalf("Projection: %v", err)
	}
	for _, d := range pcfg.Descriptors {
		if d.PathKey() != "ping" {
			continue
		}
		if d.Invocable || d.Reason != "management-only" {
			t.Errorf("ping: invocable=%v reason=%q, want withheld as management-only", d.Invocable, d.Reason)
		}
		return
	}
	t.Error("ping is not described")
}

// The projection forwards the request's W3C trace context to the
// runner and to an auth refusal's audit record.
func TestProjectionForwardsTraceContext(t *testing.T) {
	const tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	const ts = "vendor=opaque"
	runner := &recordingRunner{}
	sink := &auditSink{}
	b := cmdsurface.New(projectionTree(false), cmdsurface.WithRunner(runner),
		cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnOK: true, OnError: true}))
	b.Expose("*", cmdsurface.SurfaceREST)
	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r, cmdsurface.WithProjectionAuth(
		func(req *http.Request) (any, error) {
			if req.Header.Get("Authorization") == "" {
				return nil, errors.New("no credential")
			}
			return "alice", nil
		})); err != nil {
		t.Fatalf("MountProjection: %v", err)
	}
	url := serve(t, r)
	hdr := map[string]string{"traceparent": tp, "tracestate": ts}

	if status, body := do(t, http.MethodPost, url+"/v1/commands/widget/add", `{}`, hdr); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status=%d body=%s", status, body)
	}
	recs := sink.records()
	if len(recs) != 1 || recs[0].inv.Meta.Traceparent != tp || recs[0].inv.Meta.Tracestate != ts {
		t.Fatalf("auth refusal audit = %+v, want the trace context", recs)
	}

	hdr["Authorization"] = "Bearer t"
	if status, body := do(t, http.MethodPost, url+"/v1/commands/widget/add", `{}`, hdr); status != http.StatusOK {
		t.Fatalf("call: status=%d body=%s", status, body)
	}
	got := runner.captured()
	if len(got) != 1 || got[0].Meta.Traceparent != tp || got[0].Meta.Tracestate != ts {
		t.Fatalf("runner meta = %+v, want the trace context", got)
	}
}
