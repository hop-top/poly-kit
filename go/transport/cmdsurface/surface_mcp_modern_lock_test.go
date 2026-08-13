package cmdsurface

// Modern MCP surface conformance lock (protocol 2026-07-28).
//
// This suite pins the CURRENT wire behavior of the modern handler
// (surface_mcp_dispatch.go, surface_mcp_modern.go,
// surface_mcp_modern_list.go, surface_mcp_modern_call.go,
// surface_mcp_modern_confirm.go) byte-for-byte, in the same style as
// surface_mcp_legacy_lock_test.go: every response body asserted here
// is a literal string constant (or, where the ADR pins construction
// rather than bytes — MRTR requestState — derived via the package's
// own exported/unexported helpers), never copied from the production
// code under test.
//
// Relationship to the per-behavior suites (surface_mcp_dispatch_test.go,
// surface_mcp_modern_test.go, surface_mcp_modern_list_test.go,
// surface_mcp_modern_call_test.go, surface_mcp_modern_confirm_test.go):
// those files are frozen and stay as the fine-grained, structural
// (decode-and-assert-fields) regression coverage for internals. This
// file is the WIRE CONTRACT: one self-describing golden exchange per
// case — request bytes+headers in, expected status+body bytes out —
// intended as the future cross-language parity fixture source. It
// does not re-test internals; it locks the bytes a client actually
// sees. Some structural coverage is intentionally NOT duplicated here
// (e.g. every V6/V7 duplicate-header permutation) since the
// per-behavior suites already pin those and this file's job is
// breadth-of-CASE-SHAPE, not breadth-of-every-negative-permutation.
//
// Byte-exactness rationale (same as the legacy lock): response bodies
// are built from map[string]any, and encoding/json.Marshal sorts
// object keys lexicographically, so the wire bytes are deterministic
// run to run for every case that does not embed a MAC-derived
// requestState. The MRTR cases DO embed such a value, so those
// specific goldens are construction-exact (built via mintMCPConfirmState/
// mcpConfirmBinding/mcpConfirmArgsDigest — the same helpers the
// production gate uses) rather than byte-exact against a literal
// string, per the task brief's instruction: "byte-exact where the ADR
// pins bytes, construction-exact where it pins construction."
//
// Fixture convention: this file defines its own cobra tree
// (modernLockTree), independent of legacyLockTree, modernTestTree,
// dispatchTestTree, and confirmTestTree — none of those are reused,
// matching every other file in this package's frozen-fixture
// convention.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
)

// --- fixed cobra tree ---------------------------------------------------
//
//	root
//	├── ping        (read; happy-path exec target, prints "pong")
//	├── widget
//	│   ├── add     (write; flags: name str required, count int)
//	│   └── delete  (destructive)
//	├── secret      (auth-required)
//	└── deploy      (requires-confirmation)
//
// Deliberately shaped like (but narrower than) legacyLockTree: this
// tree's widget.add carries only name+count, not legacyLockTree's
// force+tag+hidden-flag+deprecated-flag set. The dual-version matrix
// test (surface_mcp_matrix_test.go) does NOT use this tree — it mounts
// on legacyLockTree() directly via legacyLockServer() so its legacy
// steps can cite the legacy lock's own goldens by true identity. This
// tree exists for the rest of this file's modern-only wire-contract
// cases, where no legacy-lock cross-reference is being made.
func modernLockTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

	ping := &cobra.Command{
		Use:   "ping",
		Short: "Ping the server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("pong")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	root.AddCommand(ping)

	widget := &cobra.Command{Use: "widget"}
	add := &cobra.Command{
		Use:   "add",
		Short: "Add a widget",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("added")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "write"},
	}
	add.Flags().String("name", "", "widget name")
	add.Flags().Int("count", 0, "widget count")
	_ = add.MarkFlagRequired("name")
	widget.AddCommand(add)

	del := &cobra.Command{
		Use:   "delete",
		Short: "Delete a widget",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("deleted")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	}
	widget.AddCommand(del)
	root.AddCommand(widget)

	secret := &cobra.Command{
		Use:         "secret",
		Short:       "Locked",
		RunE:        func(cmd *cobra.Command, _ []string) error { return nil },
		Annotations: map[string]string{"kit/auth-required": "true"},
	}
	root.AddCommand(secret)

	deploy := &cobra.Command{
		Use:         "deploy",
		Short:       "Deploy",
		RunE:        func(cmd *cobra.Command, _ []string) error { return nil },
		Annotations: map[string]string{"kit/requires-confirmation": "true"},
	}
	root.AddCommand(deploy)

	return root
}

// modernLockServer mounts MountMCP with the given options over a fresh
// modernLockTree bridge and returns a live httptest.Server.
func modernLockServer(t *testing.T, build func(root *cobra.Command) *Bridge, mountOpts ...MCPOption) *httptest.Server {
	t.Helper()
	root := modernLockTree()
	if build == nil {
		build = func(root *cobra.Command) *Bridge { return New(root) }
	}
	b := build(root)
	r := api.NewRouter()
	if err := MountMCP(b, r, mountOpts...); err != nil {
		t.Fatalf("MountMCP: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// --- hermetic HTTP client (harness-flake fix) ---------------------------
//
// The per-behavior suites' postJSON helper (surface_mcp_dispatch_test.go)
// and the legacy lock's rawPOST helper both call http.DefaultClient.Do,
// which is a process-global client with HTTP keep-alives enabled. Under
// `go test -count=N`, each test function's httptest.NewServer spins up
// a fresh loopback listener that is closed at t.Cleanup — but
// http.DefaultClient's shared connection pool can still hold (or be
// mid-handshake on) a pooled connection to a just-closed server's
// address/port pair when the OS immediately reissues that port to the
// NEXT server in the run, producing a transient connection-reset/EOF
// failure unrelated to any assertion in the test. This was root-caused
// during MRTR review (one observed transient FAIL) and is exactly the
// hazard "keep-alives against serially closed httptest servers"
// describes.
//
// Fix: this suite's own request helper (goldenExchange, below) uses a
// private *http.Client with keep-alives disabled, so every request
// opens (and closes) its own connection — no pooled socket can outlive
// the server that owned it. Per the task brief, the frozen legacy lock
// and the frozen per-behavior suites are NOT touched: their
// http.DefaultClient usage remains exactly as it was (see report for
// the residual-flake note carried forward to review).
func hermeticHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
}

// --- golden exchange -----------------------------------------------------
//
// A goldenExchange is a single self-describing wire case: the exact
// request bytes+headers a client sends, and the exact response
// status+body bytes the surface must produce. This shape (not a
// decode-and-assert-fields struct) is deliberate: it is what a future
// cross-language fixture-extraction task consumes directly — dump
// the table to JSON/YAML once, replay against any implementation.
type goldenExchange struct {
	name    string
	path    string // defaults to "/mcp" when empty
	headers map[string]string
	body    []byte

	wantStatus int
	wantBody   []byte
}

// runGoldenExchange posts the exchange's request to srv over a
// hermetic (non-pooled) client and asserts the response byte-exact.
func runGoldenExchange(t *testing.T, srv *httptest.Server, client *http.Client, gx goldenExchange) {
	t.Helper()
	path := gx.path
	if path == "" {
		path = "/mcp"
	}
	var rdr io.Reader
	if gx.body != nil {
		rdr = bytes.NewReader(gx.body)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range gx.headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != gx.wantStatus {
		t.Fatalf("status=%d want=%d\nbody: %s", resp.StatusCode, gx.wantStatus, raw)
	}
	if !bytes.Equal(raw, gx.wantBody) {
		t.Errorf("%s: wire body mismatch\n got:  %s\nwant:  %s", gx.name, raw, gx.wantBody)
	}
}

// goldenExchangeH is goldenExchange's sibling for the one case shape
// goldenExchange's map[string]string cannot express: a single header
// NAME sent with more than one value (duplicate-header golden
// exchanges — see the "duplicate headers" section below). A
// map[string]string collapses repeated keys to one; http.Header.Add
// preserves every occurrence in send order, which is what the ADR's
// "sent more than once" cases require to actually exercise the
// production request. Otherwise identical in spirit and shape to
// goldenExchange (request in, status+body bytes out) — a fixture
// extractor consuming this row shape need only support multi-valued
// headers per case, the same accommodation net/http.Header itself
// makes.
type goldenExchangeH struct {
	name    string
	path    string // defaults to "/mcp" when empty
	headers http.Header
	body    []byte

	wantStatus int
	wantBody   []byte
}

// runGoldenExchangeHeader is runGoldenExchange for goldenExchangeH:
// same hermetic client, same byte-exact comparator, only the header
// construction differs (assigned wholesale rather than Set per key,
// so Add-accumulated duplicate values survive onto the wire).
func runGoldenExchangeHeader(t *testing.T, srv *httptest.Server, client *http.Client, gx goldenExchangeH) {
	t.Helper()
	path := gx.path
	if path == "" {
		path = "/mcp"
	}
	var rdr io.Reader
	if gx.body != nil {
		rdr = bytes.NewReader(gx.body)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if gx.headers != nil {
		req.Header = gx.headers.Clone()
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != gx.wantStatus {
		t.Fatalf("status=%d want=%d\nbody: %s", resp.StatusCode, gx.wantStatus, raw)
	}
	if !bytes.Equal(raw, gx.wantBody) {
		t.Errorf("%s: wire body mismatch\n got:  %s\nwant:  %s", gx.name, raw, gx.wantBody)
	}
}

// stdModernHeaders returns the three headers a conforming modern
// request carries: MCP-Protocol-Version, Mcp-Method mirroring the
// body method, and (for tools/call) Mcp-Name mirroring params.name.
func stdModernHeaders(method, name string) map[string]string {
	h := map[string]string{
		headerMCPProtocolVersion: mcpModernProtocolVersion,
		headerMCPMethod:          method,
	}
	if name != "" {
		h[headerMCPName] = name
	}
	return h
}

// --- server/discover ------------------------------------------------------

func TestModernLock_ServerDiscover_Defaults(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "server/discover defaults",
		headers:    stdModernHeaders("server/discover", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"cacheScope":"private","capabilities":{"tools":{}},"resultType":"complete","supportedVersions":["2026-07-28"],"ttlMs":0}}` + "\n"),
	})
}

func TestModernLock_ServerDiscover_ServerInfoAndCacheHintsOverride(t *testing.T) {
	srv := modernLockServer(t, nil,
		WithMCPServerInfo("acme-cli", "1.2.3"),
		WithMCPCacheHints(30*time.Second, MCPCacheScopePublic))
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "server/discover overrides",
		headers:    stdModernHeaders("server/discover", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":"d1","method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":"d1","result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"acme-cli","version":"1.2.3"}},"cacheScope":"public","capabilities":{"tools":{}},"resultType":"complete","supportedVersions":["2026-07-28"],"ttlMs":30000}}` + "\n"),
	})
}

// --- tools/list -------------------------------------------------------

func TestModernLock_ToolsList_ExactDescriptors(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	// Tool order matches Bridge.Leaves() discovery order over
	// modernLockTree(): cobra sorts each level's children
	// alphabetically, so root's children walk as deploy, ping,
	// secret, widget — and widget's children as add, delete.
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "tools/list exact descriptors",
		headers:    stdModernHeaders("tools/list", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody: []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"cacheScope":"private","resultType":"complete","tools":[` +
			`{"description":"Deploy","inputSchema":{"properties":{},"type":"object"},"name":"deploy"},` +
			`{"description":"Ping the server","inputSchema":{"properties":{},"type":"object"},"name":"ping"},` +
			`{"description":"Locked","inputSchema":{"properties":{},"type":"object"},"name":"secret"},` +
			`{"description":"Add a widget","inputSchema":{"properties":{"count":{"description":"widget count","type":"integer"},"name":{"description":"widget name","type":"string"}},"required":["name"],"type":"object"},"name":"widget.add"},` +
			`{"description":"Delete a widget","inputSchema":{"properties":{},"type":"object"},"name":"widget.delete"}` +
			`],"ttlMs":0}}` + "\n"),
	})
}

func TestModernLock_ToolsList_FiltersDisabledSurface(t *testing.T) {
	srv := modernLockServer(t, func(root *cobra.Command) *Bridge {
		return New(root).Hide("widget delete", SurfaceMCP)
	})
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "tools/list filters disabled surface",
		headers:    stdModernHeaders("tools/list", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody: []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"cacheScope":"private","resultType":"complete","tools":[` +
			`{"description":"Deploy","inputSchema":{"properties":{},"type":"object"},"name":"deploy"},` +
			`{"description":"Ping the server","inputSchema":{"properties":{},"type":"object"},"name":"ping"},` +
			`{"description":"Locked","inputSchema":{"properties":{},"type":"object"},"name":"secret"},` +
			`{"description":"Add a widget","inputSchema":{"properties":{"count":{"description":"widget count","type":"integer"},"name":{"description":"widget name","type":"string"}},"required":["name"],"type":"object"},"name":"widget.add"}` +
			`],"ttlMs":0}}` + "\n"),
	})
}

// --- tools/call ---------------------------------------------------------

func TestModernLock_ToolsCall_HappyPath_RealExec(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "tools/call happy path (real exec)",
		headers:    stdModernHeaders("tools/call", "ping"),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"pong\n","type":"text"}],"isError":false,"resultType":"complete"}}` + "\n"),
	})
}

func TestModernLock_ToolsCall_ArgumentMapping(t *testing.T) {
	captured := make(chan Invocation, 1)
	srv := modernLockServer(t, func(root *cobra.Command) *Bridge {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, inv Invocation) (Result, error) {
				captured <- inv
				return Result{Stdout: "added\n"}, nil
			},
		}))
	})
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "tools/call argument mapping response",
		headers:    stdModernHeaders("tools/call", "widget.add"),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"widget.add","arguments":{"name":"gizmo","count":3},"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"acme-client","version":"9.0"},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"added\n","type":"text"}],"isError":false,"resultType":"complete"}}` + "\n"),
	})

	inv := <-captured
	if got, want := len(inv.Path), 2; got != want {
		t.Fatalf("Path len=%d want=%d (%v)", got, want, inv.Path)
	}
	if inv.Path[0] != "widget" || inv.Path[1] != "add" {
		t.Errorf("Path=%v want=[widget add]", inv.Path)
	}
	if inv.Meta.Surface != SurfaceMCP {
		t.Errorf("Meta.Surface=%v want=%v", inv.Meta.Surface, SurfaceMCP)
	}
	if inv.Flags["name"] != "gizmo" {
		t.Errorf(`Flags["name"]=%v want=gizmo`, inv.Flags["name"])
	}
	if inv.Flags["count"] != float64(3) {
		t.Errorf(`Flags["count"]=%v (%T) want=float64(3)`, inv.Flags["count"], inv.Flags["count"])
	}
	// Meta.Extra audit bag: spec version always, client identity when
	// clientInfo was present (ADR 0004 "One surface, not two").
	if inv.Meta.Extra["mcp_spec_version"] != "2026-07-28" {
		t.Errorf(`Extra["mcp_spec_version"]=%v want=2026-07-28`, inv.Meta.Extra["mcp_spec_version"])
	}
	if inv.Meta.Extra["mcp_client_name"] != "acme-client" {
		t.Errorf(`Extra["mcp_client_name"]=%v want=acme-client`, inv.Meta.Extra["mcp_client_name"])
	}
	if inv.Meta.Extra["mcp_client_version"] != "9.0" {
		t.Errorf(`Extra["mcp_client_version"]=%v want=9.0`, inv.Meta.Extra["mcp_client_version"])
	}
}

func TestModernLock_ToolsCall_StructuredContent(t *testing.T) {
	srv := modernLockServer(t, func(root *cobra.Command) *Bridge {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, _ Invocation) (Result, error) {
				return Result{Stdout: "ok", Stderr: "warning", Data: map[string]any{"id": 42}}, nil
			},
		}))
	})
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "tools/call structuredContent + stderr block",
		headers:    stdModernHeaders("tools/call", "ping"),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"ok","type":"text"},{"text":"[stderr] warning","type":"text"},{"text":"{\"id\":42}","type":"text"}],"isError":false,"resultType":"complete","structuredContent":{"id":42}}}` + "\n"),
	})
}

func TestModernLock_ToolsCall_NonZeroExitIsError(t *testing.T) {
	srv := modernLockServer(t, func(root *cobra.Command) *Bridge {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, _ Invocation) (Result, error) {
				return Result{Stdout: "boom", ExitCode: 2}, nil
			},
		}))
	})
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "tools/call non-zero exit",
		headers:    stdModernHeaders("tools/call", "ping"),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"boom","type":"text"}],"isError":true,"resultType":"complete"}}` + "\n"),
	})
}

// --- V1-V9 validation outcomes, exact codes/statuses --------------------

func TestModernLock_V1_InvalidJSONRPCVersion(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V1 invalid jsonrpc version",
		headers:    stdModernHeaders("server/discover", ""),
		body:       []byte(`{"jsonrpc":"1.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"invalid jsonrpc version"}}` + "\n"),
	})
}

func TestModernLock_V2_Notification_202Empty(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V2 notification 202 empty",
		headers:    stdModernHeaders("server/discover", ""),
		body:       []byte(`{"jsonrpc":"2.0","method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusAccepted,
		wantBody:   []byte(``),
	})
}

func TestModernLock_V2_NullIDRejected(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V2 null id rejected",
		headers:    stdModernHeaders("server/discover", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":null,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid request id: must be a string or integer, got null"}}` + "\n"),
	})
}

func TestModernLock_V2_FloatIDRejected(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V2 fractional-float id rejected",
		headers:    stdModernHeaders("server/discover", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":1.5,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1.5,"error":{"code":-32600,"message":"invalid request id: must be a string or integer, got 1.5"}}` + "\n"),
	})
}

func TestModernLock_V3_MissingMeta(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V3 missing params._meta entirely",
		headers:    stdModernHeaders("server/discover", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover"}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"missing required params._meta"}}` + "\n"),
	})
}

func TestModernLock_V3_MissingClientCapabilities(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V3 missing clientCapabilities key",
		headers:    stdModernHeaders("server/discover", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"missing required _meta key: io.modelcontextprotocol/clientCapabilities"}}` + "\n"),
	})
}

func TestModernLock_V4_MissingProtocolVersionHeader(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V4 missing MCP-Protocol-Version header",
		headers:    map[string]string{headerMCPMethod: "server/discover"},
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32020,"message":"missing MCP-Protocol-Version header"}}` + "\n"),
	})
}

func TestModernLock_V4_HeaderMetaMismatch(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name: "V4 header does not match _meta protocolVersion",
		headers: map[string]string{
			headerMCPProtocolVersion: "2099-01-01",
			headerMCPMethod:          "server/discover",
		},
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32020,"message":"MCP-Protocol-Version header \"2099-01-01\" does not match _meta protocolVersion 2026-07-28"}}` + "\n"),
	})
}

func TestModernLock_V5_UnsupportedVersion(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name: "V5 unsupported protocol version",
		headers: map[string]string{
			headerMCPProtocolVersion: "2024-11-05",
			headerMCPMethod:          "server/discover",
		},
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2024-11-05"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32022,"message":"unsupported protocol version: 2024-11-05","data":{"requested":"2024-11-05","supported":["2026-07-28"]}}}` + "\n"),
	})
}

func TestModernLock_V6_MissingMethodHeader(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V6 missing Mcp-Method header",
		headers:    map[string]string{headerMCPProtocolVersion: mcpModernProtocolVersion},
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32020,"message":"missing Mcp-Method header"}}` + "\n"),
	})
}

func TestModernLock_V6_MethodHeaderMismatch(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name: "V6 Mcp-Method header disagrees with body method",
		headers: map[string]string{
			headerMCPProtocolVersion: mcpModernProtocolVersion,
			headerMCPMethod:          "tools/list",
		},
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32020,"message":"Mcp-Method header \"tools/list\" does not match body method \"server/discover\""}}` + "\n"),
	})
}

func TestModernLock_V7_MissingNameHeader(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V7 missing Mcp-Name header on tools/call",
		headers:    map[string]string{headerMCPProtocolVersion: mcpModernProtocolVersion, headerMCPMethod: "tools/call"},
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32020,"message":"missing Mcp-Name header"}}` + "\n"),
	})
}

func TestModernLock_V7_NameHeaderMismatch(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V7 Mcp-Name header disagrees with params.name",
		headers:    stdModernHeaders("tools/call", "widget.add"),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32020,"message":"Mcp-Name header \"widget.add\" does not match body params.name \"ping\""}}` + "\n"),
	})
}

func TestModernLock_V7_EmptySentinelDecodesToEmpty(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V7 empty base64 sentinel decodes to empty and is rejected",
		headers:    map[string]string{headerMCPProtocolVersion: mcpModernProtocolVersion, headerMCPMethod: "tools/call", headerMCPName: "=?base64??="},
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32020,"message":"Mcp-Name header decodes to an empty value"}}` + "\n"),
	})
}

func TestModernLock_V7_SentinelDecodedBeforeCompare(t *testing.T) {
	// "ping" base64-encoded is "cGluZw==".
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V7 sentinel-encoded Mcp-Name decodes then matches",
		headers:    map[string]string{headerMCPProtocolVersion: mcpModernProtocolVersion, headerMCPMethod: "tools/call", headerMCPName: "=?base64?cGluZw==?="},
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"pong\n","type":"text"}],"isError":false,"resultType":"complete"}}` + "\n"),
	})
}

// --- duplicate headers: conflicting -> -32020@400, identical -> tolerated
//
// ADR 0004 "Duplicate headers": a header sent more than once with
// byte-identical values is tolerated (benign proxy/intermediary
// duplication); sent more than once with differing values is a
// validation failure in its own right (-32020@400), without ever
// comparing either conflicting value against the body. Locked here for
// all three modern-authority headers (MCP-Protocol-Version, Mcp-Method,
// Mcp-Name) — these previously existed only as structural assertions in
// frozen per-behavior suites, unreachable by a fixture extractor.

func TestModernLock_DuplicateHeader_MethodConflictingRejected(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	headers := http.Header{}
	headers.Add(headerMCPProtocolVersion, mcpModernProtocolVersion)
	headers.Add(headerMCPMethod, "server/discover")
	headers.Add(headerMCPMethod, "tools/list")
	runGoldenExchangeHeader(t, srv, client, goldenExchangeH{
		name:       "conflicting duplicate Mcp-Method rejected -32020@400",
		headers:    headers,
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32020,"message":"Mcp-Method header sent with conflicting duplicate values"}}` + "\n"),
	})
}

func TestModernLock_DuplicateHeader_MethodIdenticalTolerated(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	headers := http.Header{}
	headers.Add(headerMCPProtocolVersion, mcpModernProtocolVersion)
	headers.Add(headerMCPMethod, "server/discover")
	headers.Add(headerMCPMethod, "server/discover")
	runGoldenExchangeHeader(t, srv, client, goldenExchangeH{
		name:       "byte-identical duplicate Mcp-Method tolerated",
		headers:    headers,
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"cacheScope":"private","capabilities":{"tools":{}},"resultType":"complete","supportedVersions":["2026-07-28"],"ttlMs":0}}` + "\n"),
	})
}

func TestModernLock_DuplicateHeader_NameConflictingRejected(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	headers := http.Header{}
	headers.Add(headerMCPProtocolVersion, mcpModernProtocolVersion)
	headers.Add(headerMCPMethod, "tools/call")
	headers.Add(headerMCPName, "ping")
	headers.Add(headerMCPName, "widget.add")
	runGoldenExchangeHeader(t, srv, client, goldenExchangeH{
		name:       "conflicting duplicate Mcp-Name rejected -32020@400",
		headers:    headers,
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32020,"message":"Mcp-Name header sent with conflicting duplicate values"}}` + "\n"),
	})
}

func TestModernLock_DuplicateHeader_NameIdenticalTolerated(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	headers := http.Header{}
	headers.Add(headerMCPProtocolVersion, mcpModernProtocolVersion)
	headers.Add(headerMCPMethod, "tools/call")
	headers.Add(headerMCPName, "ping")
	headers.Add(headerMCPName, "ping")
	runGoldenExchangeHeader(t, srv, client, goldenExchangeH{
		name:       "byte-identical duplicate Mcp-Name tolerated",
		headers:    headers,
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"pong\n","type":"text"}],"isError":false,"resultType":"complete"}}` + "\n"),
	})
}

func TestModernLock_DuplicateHeader_ProtocolVersionConflictingRejected(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	headers := http.Header{}
	headers.Add(headerMCPProtocolVersion, "2026-07-28")
	headers.Add(headerMCPProtocolVersion, "2099-01-01")
	headers.Add(headerMCPMethod, "server/discover")
	runGoldenExchangeHeader(t, srv, client, goldenExchangeH{
		name:       "conflicting duplicate MCP-Protocol-Version rejected -32020@400",
		headers:    headers,
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32020,"message":"MCP-Protocol-Version header sent with conflicting duplicate values"}}` + "\n"),
	})
}

func TestModernLock_DuplicateHeader_ProtocolVersionIdenticalTolerated(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	headers := http.Header{}
	headers.Add(headerMCPProtocolVersion, "2026-07-28")
	headers.Add(headerMCPProtocolVersion, "2026-07-28")
	headers.Add(headerMCPMethod, "server/discover")
	runGoldenExchangeHeader(t, srv, client, goldenExchangeH{
		name:       "byte-identical duplicate MCP-Protocol-Version tolerated",
		headers:    headers,
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"cacheScope":"private","capabilities":{"tools":{}},"resultType":"complete","supportedVersions":["2026-07-28"],"ttlMs":0}}` + "\n"),
	})
}

func TestModernLock_V8_UnknownMethod404(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V8 unknown method -32601 at 404 (modern, not legacy's 200 quirk)",
		headers:    stdModernHeaders("nope/anywhere", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"nope/anywhere","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusNotFound,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found: nope/anywhere"}}` + "\n"),
	})
}

func TestModernLock_V9_UnknownTool(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "V9 unknown tool -32602 at 200",
		headers:    stdModernHeaders("tools/call", "nope.nada"),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope.nada","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"unknown tool: nope.nada"}}` + "\n"),
	})
}

// --- pre-flight gates + capability fallback ------------------------------
//
// ADR 0004 "Pre-flight gates (modern), mirroring legacy exactly" pins
// three outcomes ahead of Bridge.Invoke, and "MRTR confirmation slot"
// pins a fourth (capability-gated fallback): AuthRequired without an
// Authorization header -> isError result @ 401; RequiresConfirmation
// without X-Confirm-Token (the default gate) -> isError result @ 428;
// ErrDestructiveBlocked -> isError result @ 200; and, on a mount keyed
// with WithMCPConfirmationKey, a client that does NOT declare the
// elicitation capability still gets the header gate (428), never the
// -32021 "missing required client capability" code — the capability is
// optional precisely because this fallback exists. These previously
// existed only as structural assertions in frozen per-behavior suites
// (TestModernCall_AuthRequired401, _ConfirmationRequired428,
// _DestructiveBlockedByDefault, TestModernConfirm_NoCapability_
// HeaderFallback, decline/cancel loop) — high port-divergence-risk
// wire behavior with no golden-exchange coverage until now.

func TestModernLock_PreFlight_AuthRequired401(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "auth-required leaf without Authorization header -> isError@401",
		headers:    stdModernHeaders("tools/call", "secret"),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"secret","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusUnauthorized,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"authentication required","type":"text"}],"isError":true,"resultType":"complete"}}` + "\n"),
	})
}

func TestModernLock_PreFlight_AuthRequired_WithHeaderSucceeds(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	headers := stdModernHeaders("tools/call", "secret")
	headers["Authorization"] = "Bearer x"
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "auth-required leaf with Authorization header succeeds",
		headers:    headers,
		body:       []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"secret","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":2,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"","type":"text"}],"isError":false,"resultType":"complete"}}` + "\n"),
	})
}

func TestModernLock_PreFlight_ConfirmationRequired428(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "confirmation-required leaf without X-Confirm-Token -> isError@428",
		headers:    stdModernHeaders("tools/call", "deploy"),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusPreconditionRequired,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"confirmation required","type":"text"}],"isError":true,"resultType":"complete"}}` + "\n"),
	})
}

func TestModernLock_PreFlight_ConfirmationRequired_WithTokenSucceeds(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	headers := stdModernHeaders("tools/call", "deploy")
	headers["X-Confirm-Token"] = "yes"
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "confirmation-required leaf with X-Confirm-Token succeeds",
		headers:    headers,
		body:       []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":2,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"","type":"text"}],"isError":false,"resultType":"complete"}}` + "\n"),
	})
}

func TestModernLock_PreFlight_DestructiveBlockedByDefault(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "destructive leaf blocked by default policy -> isError@200",
		headers:    stdModernHeaders("tools/call", "widget.delete"),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"widget.delete","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"cmdsurface: destructive command blocked on this surface: widget delete on mcp","type":"text"}],"isError":true,"resultType":"complete"}}` + "\n"),
	})
}

// mcpLockConfirmKey is the shared HMAC key the pre-flight-gate goldens
// use for keyed-mount (MRTR) cases below — independent of
// mcpConfirmTestKey (surface_mcp_modern_confirm_test.go, frozen).
var mcpLockConfirmKey = []byte("modern-lock-suite-shared-secret-32b")

func TestModernLock_PreFlight_NoElicitationCapability_HeaderFallback(t *testing.T) {
	// Keyed mount (WithMCPConfirmationKey), but the request's
	// clientCapabilities does NOT declare "elicitation" at all: the
	// gate must fall back to the X-Confirm-Token header check exactly
	// as an unkeyed mount would, and must never answer -32021
	// (missing required client capability) — the capability is
	// optional precisely because this fallback exists (ADR 0004).
	srv := modernLockServer(t, nil, WithMCPConfirmationKey(mcpLockConfirmKey))
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "keyed mount, no elicitation capability declared -> header gate fallback (428, not -32021)",
		headers:    stdModernHeaders("tools/call", "deploy"),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusPreconditionRequired,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"confirmation required","type":"text"}],"isError":true,"resultType":"complete"}}` + "\n"),
	})
}

func TestModernLock_MRTR_DeclineRefuses(t *testing.T) {
	// Round 1 mints a real requestState (construction-exact — see the
	// MRTR section below for the rationale); round 2 echoes it with
	// inputResponses.confirm.action == "decline", which ADR 0004 pins
	// as an isError "confirmation declined" complete result — never an
	// execution, never a re-prompt.
	srv := modernLockServer(t, nil, WithMCPConfirmationKey(mcpLockConfirmKey))
	client := hermeticHTTPClient()

	round1Body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy","_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	req1, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", bytes.NewReader(round1Body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req1.Header.Set("Content-Type", "application/json")
	for k, v := range stdModernHeaders("tools/call", "deploy") {
		req1.Header.Set(k, v)
	}
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("round1 do: %v", err)
	}
	defer resp1.Body.Close()
	raw1, err := io.ReadAll(resp1.Body)
	if err != nil {
		t.Fatalf("round1 read: %v", err)
	}
	var decoded1 struct {
		Result struct {
			RequestState string `json:"requestState"`
		} `json:"result"`
	}
	if err := jsonUnmarshalLock(raw1, &decoded1); err != nil {
		t.Fatalf("round1 decode: %v\nbody: %s", err, raw1)
	}
	state := decoded1.Result.RequestState
	if state == "" {
		t.Fatalf("round1 missing requestState\nbody: %s", raw1)
	}

	round2Body := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy","requestState":` +
		mustJSONLock(t, state) + `,"inputResponses":{"confirm":{"action":"decline"}},"_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "MRTR decline refuses with isError, never executes",
		headers:    stdModernHeaders("tools/call", "deploy"),
		body:       round2Body,
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":2,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"confirmation declined","type":"text"}],"isError":true,"resultType":"complete"}}` + "\n"),
	})
}

// --- initialize on modern-only mount (naming supported versions) --------

func TestModernLock_InitializeRejection_NamesSupportedVersions(t *testing.T) {
	root := modernLockTree()
	b := New(root)
	r := api.NewRouter()
	if err := MountMCP(b, r, WithMCPSpecVersions(MCPSpec20260728)); err != nil {
		t.Fatalf("MountMCP: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "modern-only initialize fails V3, message names supported versions",
		headers:    map[string]string{headerMCPProtocolVersion: mcpModernProtocolVersion, headerMCPMethod: "initialize"},
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"missing required params._meta; supported protocol versions: 2026-07-28"}}` + "\n"),
	})
}

// --- tasks extension ABSENCE conformance ---------------------------------
//
// Per the task brief's controller scope addition, the tasks extension
// (io.modelcontextprotocol/tasks: tasks/get, tasks/update) was
// deliberately descoped by ADR decision. This suite does NOT implement
// it; these goldens instead lock the two proofs of its absence: the
// extension methods answer -32601 at 404 like any other unrecognized
// method, and server/discover's capabilities object carries no
// "extensions" key at all — not an empty map, an omitted key. A future
// implementation of the extension would have to change both bytes
// below, which is the point.

func TestModernLock_TasksExtension_GetAbsent(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "tasks/get -32601 at 404 (extension not implemented)",
		headers:    stdModernHeaders("tasks/get", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tasks/get","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusNotFound,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found: tasks/get"}}` + "\n"),
	})
}

func TestModernLock_TasksExtension_UpdateAbsent(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "tasks/update -32601 at 404 (extension not implemented)",
		headers:    stdModernHeaders("tasks/update", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tasks/update","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusNotFound,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found: tasks/update"}}` + "\n"),
	})
}

func TestModernLock_TasksExtension_DiscoverAdvertisesNoExtensionsMap(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	// The full server/discover golden (TestModernLock_ServerDiscover_Defaults)
	// already pins this byte-exact; this case exists as an explicit,
	// separately-named absence proof per the controller scope addition —
	// capabilities is exactly {"tools":{}}, with no "extensions" member
	// anywhere in the object, byte-exact.
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "server/discover capabilities has no extensions key",
		headers:    stdModernHeaders("server/discover", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"cacheScope":"private","capabilities":{"tools":{}},"resultType":"complete","supportedVersions":["2026-07-28"],"ttlMs":0}}` + "\n"),
	})
}

// --- era-detection marker permutations (ADR 0004 D1-D4, M1-M4) ----------
//
// The dual-version matrix (surface_mcp_matrix_test.go) proves era
// isolation for the marker combinations most likely on real traffic
// (M1+M2+M3 together, or no markers at all). This section locks the
// distinctive individual-marker and precedence-rule wire outcomes ADR
// 0004 pins but the matrix does not exercise in isolation: a single
// marker alone routing modern (M2-only, M3-only, M4-only), the D2
// initialize-wins-over-markers quirk, and the D1 parse-failure path
// under modern headers. A port that requires M1 (Mcp-Method) for
// modern routing — a natural but ADR-noncompliant implementation —
// would pass every other case in this suite while misrouting M2/M3/M4
// -only requests to the legacy handler; these goldens catch exactly
// that. Mounted with both spec versions enabled (the default), so
// era detection is genuinely exercised rather than assumed.

func TestModernLock_EraDetection_M3Only_RoutesModern(t *testing.T) {
	// M3 alone: body params._meta carries the reserved protocolVersion
	// key (and nothing else — no clientCapabilities, no headers).
	// Routes modern per D3; fails modern V3 (missing required _meta
	// key io.modelcontextprotocol/clientCapabilities — V3 runs and
	// fails before V4's header check is ever reached) rather than
	// being served (or silently ignored) by the legacy handler, which
	// never reads params._meta at all and would have executed
	// tools/list successfully if this had been misrouted legacy.
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "M3-only (_meta protocolVersion key, no headers) routes modern",
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"missing required _meta key: io.modelcontextprotocol/clientCapabilities"}}` + "\n"),
	})
}

func TestModernLock_EraDetection_M4Only_RoutesModern(t *testing.T) {
	// M4 alone: bare server/discover, no headers, no _meta at all.
	// method == "server/discover" routes modern unconditionally (D3);
	// legacy has no server/discover handling to fall back to, so this
	// also proves the method-name marker fires even with an otherwise
	// completely bare request.
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "M4-only (bare server/discover) routes modern",
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover"}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"missing required params._meta"}}` + "\n"),
	})
}

func TestModernLock_EraDetection_M2Only_RoutesModern(t *testing.T) {
	// M2 alone: Mcp-Name header present, no Mcp-Method, no
	// MCP-Protocol-Version, no _meta. Routes modern per D3 on the
	// header marker alone; fails modern V3 (missing params._meta) —
	// proving a single recognized header, unaccompanied by any other
	// marker, is sufficient to route modern rather than requiring M1.
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "M2-only (Mcp-Name header alone) routes modern",
		headers:    map[string]string{headerMCPName: "ping"},
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"missing required params._meta"}}` + "\n"),
	})
}

func TestModernLock_EraDetection_InitializeWithMarkers_RoutesLegacy(t *testing.T) {
	// D2 (the ADR-acknowledged quirk): method == "initialize" routes
	// legacy UNCONDITIONALLY, even carrying both header markers (M1)
	// and a _meta protocolVersion key (M3) that would otherwise route
	// modern. The response is the ordinary legacy initialize result —
	// modern markers are silently ignored on this one method name.
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "initialize with modern markers still routes legacy (D2)",
		headers:    map[string]string{headerMCPProtocolVersion: mcpModernProtocolVersion, headerMCPMethod: "initialize"},
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"tools":{}},"protocolVersion":"2024-11-05","serverInfo":{"name":"cmdsurface","version":"0.0.0"}}}` + "\n"),
	})
}

func TestModernLock_EraDetection_UnparseableBodyWithModernHeaders_ParseError(t *testing.T) {
	// D1 (parse) runs before any marker inspection: an unparseable
	// body gets the same -32700@400 shape regardless of headers
	// present, including a full set of modern headers that would
	// otherwise route modern — the dispatcher delegates this exact
	// failure to the single frozen legacy implementation of the parse-
	// error path (surface_mcp_dispatch.go's design note).
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "unparseable body with modern headers -> -32700@400, headers ignored",
		headers:    map[string]string{headerMCPProtocolVersion: mcpModernProtocolVersion, headerMCPMethod: "server/discover"},
		body:       []byte(`{not valid json`),
		wantStatus: http.StatusBadRequest,
		wantBody:   []byte(`{"jsonrpc":"2.0","error":{"code":-32700,"message":"parse error: invalid character 'n' looking for beginning of object key string"}}` + "\n"),
	})
}

// --- Origin allowlist -----------------------------------------------------

func TestModernLock_Origin_Disallowed403(t *testing.T) {
	srv := modernLockServer(t, nil, WithMCPOriginAllowlist("https://app.example.com"))
	client := hermeticHTTPClient()
	headers := stdModernHeaders("server/discover", "")
	headers["Origin"] = "https://evil.example.com"
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "disallowed Origin rejected 403",
		headers:    headers,
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusForbidden,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"origin not allowed"}}` + "\n"),
	})
}

func TestModernLock_Origin_Allowed(t *testing.T) {
	srv := modernLockServer(t, nil, WithMCPOriginAllowlist("https://app.example.com"))
	client := hermeticHTTPClient()
	headers := stdModernHeaders("server/discover", "")
	headers["Origin"] = "https://app.example.com"
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "allowlisted Origin served",
		headers:    headers,
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"cacheScope":"private","capabilities":{"tools":{}},"resultType":"complete","supportedVersions":["2026-07-28"],"ttlMs":0}}` + "\n"),
	})
}

// --- HTTP verbs: GET/DELETE 405, notifications 202 -----------------------

func TestModernLock_HTTP_GET405(t *testing.T) {
	srv := modernLockServer(t, nil)
	client := hermeticHTTPClient()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp status=%d want=405", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	want := `{"jsonrpc":"2.0","error":{"code":-32600,"message":"method not allowed"}}` + "\n"
	if !bytes.Equal(raw, []byte(want)) {
		t.Errorf("GET /mcp body mismatch\n got:  %s\nwant:  %s", raw, want)
	}
}

func TestModernLock_HTTP_DELETE405(t *testing.T) {
	srv := modernLockServer(t, nil)
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/mcp", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	client := hermeticHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /mcp status=%d want=405", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	want := `{"jsonrpc":"2.0","error":{"code":-32600,"message":"method not allowed"}}` + "\n"
	if !bytes.Equal(raw, []byte(want)) {
		t.Errorf("DELETE /mcp body mismatch\n got:  %s\nwant:  %s", raw, want)
	}
}

// --- MRTR full loop + tamper + expiry (construction-exact) --------------
//
// Per the task brief and fixture-convention note: requestState is
// HMAC-derived, so these goldens cannot be literal string constants —
// a byte-identical ADR-pinned FRAMING (v1.<expiry>.<mac>) holds, but
// the mac's own encoding is only reproducible by calling the package's
// own mintMCPConfirmState/verifyMCPConfirmState/mcpConfirmBinding/
// mcpConfirmArgsDigest/mcpConfirmPrincipal helpers — the same ones the
// production gate uses — rather than hardcoding an opaque string that
// would break the moment an unrelated, ADR-conforming encoding detail
// (e.g. base64 alphabet choice within spec bounds) changed. This is
// construction-exact, not byte-exact, exactly per the brief's
// distinction.

var mrtrLockKey = []byte("mrtr-lock-suite-shared-secret-32b")

// mrtrLockTree returns a root with a single requires-confirmation leaf
// (purge) whose execution is counted, isolated from every other
// fixture tree in this package.
func mrtrLockTree() (*cobra.Command, *mrtrExecCounter) {
	counter := &mrtrExecCounter{}
	root := &cobra.Command{Use: "root"}
	purge := &cobra.Command{
		Use:   "purge",
		Short: "Purge a target",
		RunE: func(cmd *cobra.Command, _ []string) error {
			counter.n++
			target, _ := cmd.Flags().GetString("target")
			cmd.Printf("purged %s\n", target)
			return nil
		},
		Annotations: map[string]string{
			"kit/side-effect":           "write",
			"kit/requires-confirmation": "true",
		},
	}
	purge.Flags().String("target", "", "what to purge")
	root.AddCommand(purge)
	return root, counter
}

// mrtrExecCounter counts leaf executions without pulling in
// sync/atomic for a single-threaded golden-exchange sequence.
type mrtrExecCounter struct{ n int }

// mrtrElicitMeta returns a complete reserved _meta declaring form-mode
// elicitation support (the empty-object form).
func mrtrElicitMeta() map[string]any {
	return map[string]any{
		metaKeyProtocolVersion:    mcpModernProtocolVersion,
		metaKeyClientCapabilities: map[string]any{"elicitation": map[string]any{}},
	}
}

func TestModernLock_MRTR_FullLoop(t *testing.T) {
	root, counter := mrtrLockTree()
	b := New(root)
	r := api.NewRouter()
	if err := MountMCP(b, r, WithMCPConfirmationKey(mrtrLockKey)); err != nil {
		t.Fatalf("MountMCP: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	client := hermeticHTTPClient()

	// Round 1: no state yet -> input_required. We can't hardcode the
	// full response (requestState is freshly minted, time-bound), so
	// this leg asserts shape via a direct POST + decode rather than
	// runGoldenExchange's byte-exact comparator, then hands the
	// extracted requestState to round 2, which IS byte-exact-checkable
	// once round 2's own state is independently re-derived.
	//
	// Beyond resultType + non-empty requestState, this also pins (per
	// ADR 0004): the inputRequests shape — exactly one entry under the
	// reserved key "confirm", an elicitation/create form request — the
	// v1.<expiry>.<mac> requestState FRAMING (three dot-separated
	// parts, version tag "v1", numeric-decimal expiry; the mac segment
	// itself is production-derived, never hardcoded, per the
	// construction-exact exception), and the ABSENCE of ttlMs/
	// cacheScope on the interim result ("Interim input_required results
	// carry no cache hints and are never cached" — ADR 0004).
	body1 := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"purge","arguments":{"target":"data"},"_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	req1, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", bytes.NewReader(body1))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req1.Header.Set("Content-Type", "application/json")
	for k, v := range stdModernHeaders("tools/call", "purge") {
		req1.Header.Set(k, v)
	}
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("round1 do: %v", err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("round1 status=%d want=200", resp1.StatusCode)
	}
	raw1, err := io.ReadAll(resp1.Body)
	if err != nil {
		t.Fatalf("round1 read: %v", err)
	}
	var decoded1 struct {
		Result map[string]any `json:"result"`
	}
	if err := jsonUnmarshalLock(raw1, &decoded1); err != nil {
		t.Fatalf("round1 decode: %v\nbody: %s", err, raw1)
	}
	res1 := decoded1.Result
	if res1["resultType"] != mcpResultTypeInputRequired {
		t.Fatalf("round1 resultType=%v want=%q\nbody: %s", res1["resultType"], mcpResultTypeInputRequired, raw1)
	}
	state, _ := res1["requestState"].(string)
	if state == "" {
		t.Fatalf("round1 missing requestState\nbody: %s", raw1)
	}
	if counter.n != 0 {
		t.Fatalf("leaf executed before confirmation, execs=%d", counter.n)
	}

	// inputRequests shape: exactly one entry, reserved key "confirm",
	// an elicitation/create form request (ADR 0004
	// "confirmInputRequired").
	reqs, ok := res1["inputRequests"].(map[string]any)
	if !ok || len(reqs) != 1 {
		t.Fatalf("round1 inputRequests=%v want single entry", res1["inputRequests"])
	}
	confirm, ok := reqs[mcpConfirmInputRequestKey].(map[string]any)
	if !ok {
		t.Fatalf("round1 missing %q inputRequest: %v", mcpConfirmInputRequestKey, reqs)
	}
	if confirm["method"] != "elicitation/create" {
		t.Errorf("round1 inputRequest method=%v want=elicitation/create", confirm["method"])
	}
	cp, _ := confirm["params"].(map[string]any)
	if cp["mode"] != "form" {
		t.Errorf("round1 elicit mode=%v want=form", cp["mode"])
	}
	if _, ok := cp["requestedSchema"].(map[string]any); !ok {
		t.Errorf("round1 form elicitation missing requestedSchema: %v", cp)
	}

	// requestState framing: v1.<expiry>.<mac> — three dot-separated
	// parts, version tag exactly "v1", expiry a base-10 integer. The
	// mac segment's actual bytes are never asserted against a literal
	// (construction-exact exception); only the ADR-pinned FRAMING is.
	stateParts := strings.Split(state, ".")
	if len(stateParts) != 3 {
		t.Fatalf("round1 requestState framing=%q want 3 dot-separated parts (v1.<expiry>.<mac>)", state)
	}
	if stateParts[0] != mcpConfirmStateVersion {
		t.Errorf("round1 requestState version tag=%q want=%q", stateParts[0], mcpConfirmStateVersion)
	}
	if _, err := strconv.ParseInt(stateParts[1], 10, 64); err != nil {
		t.Errorf("round1 requestState expiry segment=%q not a base-10 integer: %v", stateParts[1], err)
	}
	if stateParts[2] == "" {
		t.Errorf("round1 requestState mac segment is empty")
	}

	// Cache-hint absence: interim input_required results carry no
	// ttlMs/cacheScope, ever (ADR 0004).
	if _, ok := res1["ttlMs"]; ok {
		t.Errorf("round1 input_required must not carry ttlMs: %v", res1)
	}
	if _, ok := res1["cacheScope"]; ok {
		t.Errorf("round1 input_required must not carry cacheScope: %v", res1)
	}

	// Round 2: retry (new id) echoing the state, action "accept". This
	// leg IS byte-exact: the response contains no MAC-derived value,
	// only the deterministic execution result.
	body2 := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"purge","arguments":{"target":"data"},"requestState":` +
		mustJSONLock(t, state) + `,"inputResponses":{"confirm":{"action":"accept"}},"_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "MRTR round 2: accepted retry executes",
		headers:    stdModernHeaders("tools/call", "purge"),
		body:       body2,
		wantStatus: http.StatusOK,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":2,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"purged data\n","type":"text"}],"isError":false,"resultType":"complete"}}` + "\n"),
	})
	if counter.n != 1 {
		t.Fatalf("executions=%d want=1", counter.n)
	}

	// Round 3 (tamper): flip one character of the valid state's MAC
	// segment and retry with the same binding. verifyMCPConfirmState
	// must classify this Invalid (never Expired), so the gate issues a
	// fresh input_required rather than executing or hard-erroring —
	// derived via the package's own verify helper so this assertion
	// tracks the ADR's construction rule, not a hardcoded guess at the
	// tamper detection outcome.
	tampered := tamperMCPConfirmState(state)
	binding := mcpConfirmBinding{
		tool:       "purge",
		argsDigest: mcpConfirmArgsDigest([]byte(`{"arguments":{"target":"data"}}`)),
		principal:  "",
	}
	if got := verifyMCPConfirmState(mrtrLockKey, tampered, binding, time.Now()); got != mcpConfirmStateInvalid {
		t.Fatalf("tampered state verify=%v want=Invalid (sanity check on tamperMCPConfirmState)", got)
	}
	body3 := []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"purge","arguments":{"target":"data"},"requestState":` +
		mustJSONLock(t, tampered) + `,"inputResponses":{"confirm":{"action":"accept"}},"_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	req3, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", bytes.NewReader(body3))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req3.Header.Set("Content-Type", "application/json")
	for k, v := range stdModernHeaders("tools/call", "purge") {
		req3.Header.Set(k, v)
	}
	resp3, err := client.Do(req3)
	if err != nil {
		t.Fatalf("round3 do: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("round3 status=%d want=200", resp3.StatusCode)
	}
	raw3, err := io.ReadAll(resp3.Body)
	if err != nil {
		t.Fatalf("round3 read: %v", err)
	}
	var decoded3 struct {
		Result struct {
			ResultType   string `json:"resultType"`
			RequestState string `json:"requestState"`
		} `json:"result"`
	}
	if err := jsonUnmarshalLock(raw3, &decoded3); err != nil {
		t.Fatalf("round3 decode: %v\nbody: %s", err, raw3)
	}
	if decoded3.Result.ResultType != mcpResultTypeInputRequired {
		t.Fatalf("tampered retry resultType=%q want=%q (re-prompt, not error/execute)\nbody: %s", decoded3.Result.ResultType, mcpResultTypeInputRequired, raw3)
	}
	if counter.n != 1 {
		t.Fatalf("tampered state must never execute the leaf, execs=%d want=1", counter.n)
	}

	// Round 4 (expiry): mint an already-expired, otherwise-authentic
	// state via the production minting helper and confirm it re-prompts
	// (never errors, never executes) — the routine-re-prompt path is
	// distinct from Round 3's tamper path per ADR 0004.
	expired := mintMCPConfirmState(mrtrLockKey, binding, time.Now().Add(-time.Minute))
	if got := verifyMCPConfirmState(mrtrLockKey, expired, binding, time.Now()); got != mcpConfirmStateExpired {
		t.Fatalf("expired state verify=%v want=Expired (sanity check)", got)
	}
	body4 := []byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"purge","arguments":{"target":"data"},"requestState":` +
		mustJSONLock(t, expired) + `,"inputResponses":{"confirm":{"action":"accept"}},"_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	req4, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", bytes.NewReader(body4))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req4.Header.Set("Content-Type", "application/json")
	for k, v := range stdModernHeaders("tools/call", "purge") {
		req4.Header.Set(k, v)
	}
	resp4, err := client.Do(req4)
	if err != nil {
		t.Fatalf("round4 do: %v", err)
	}
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusOK {
		t.Fatalf("round4 status=%d want=200", resp4.StatusCode)
	}
	raw4, err := io.ReadAll(resp4.Body)
	if err != nil {
		t.Fatalf("round4 read: %v", err)
	}
	var decoded4 struct {
		Result struct {
			ResultType string `json:"resultType"`
		} `json:"result"`
	}
	if err := jsonUnmarshalLock(raw4, &decoded4); err != nil {
		t.Fatalf("round4 decode: %v\nbody: %s", err, raw4)
	}
	if decoded4.Result.ResultType != mcpResultTypeInputRequired {
		t.Fatalf("expired retry resultType=%q want=%q\nbody: %s", decoded4.Result.ResultType, mcpResultTypeInputRequired, raw4)
	}
	if counter.n != 1 {
		t.Fatalf("expired state must never execute the leaf, execs=%d want=1", counter.n)
	}
}

// tamperMCPConfirmState flips the last character of the MAC segment of
// an otherwise-valid "v1.<expiry>.<mac>" state, producing a state that
// is authentic-looking (right shape, right version tag) but fails HMAC
// verification — the exact "tampered, not merely malformed" case ADR
// 0004 distinguishes from a structurally-broken string.
func tamperMCPConfirmState(state string) string {
	if len(state) == 0 {
		return state
	}
	b := []byte(state)
	last := b[len(b)-1]
	if last == 'A' {
		b[len(b)-1] = 'B'
	} else {
		b[len(b)-1] = 'A'
	}
	return string(b)
}

// jsonUnmarshalLock decodes raw JSON into v; a thin named wrapper so
// call sites in this file read as "decode a golden response" rather
// than a bare encoding/json call, matching the file's self-describing
// naming convention.
func jsonUnmarshalLock(raw []byte, v any) error {
	return json.Unmarshal(raw, v)
}

// mustJSONLock marshals v (typically a requestState string) to its
// JSON literal for splicing into a hand-built request body, failing
// the test on error.
func mustJSONLock(t *testing.T, v any) string {
	t.Helper()
	enc, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(enc)
}
