package cmdsurface

// Legacy MCP surface conformance lock (protocol 2024-11-05).
//
// This suite pins the CURRENT wire behavior of the MCP surface
// (surface_mcp.go, surface_mcp_list.go, surface_mcp_call.go) byte-
// for-byte, before any dual-spec (2026-07-28) refactor lands. It is
// the no-deprecation guarantee for that work: every response body
// asserted here is a literal string constant, not derived from the
// production code under test, so a future change that alters legacy
// wire output — even a single byte — fails this suite.
//
// Frozen for future work: this file, its helpers, and its golden
// constants are frozen. Keep it green; do not edit it. Add new
// coverage for the 2026-07-28 surface in new files instead.
//
// Design note (golden-file mechanism): the repo's xrr-backed
// conformance harness (go/conformance/, go/console/cli/conformance/
// harness/) drives cobra trees via argv (in-process cmd.Execute or
// subprocess) and cassettes OUTBOUND adapter traffic (HTTP client
// calls, SQL, Redis, gRPC, exec, fs) made *during* that invocation.
// It has no primitive for grading an INBOUND http.Handler's request/
// response bytes — harness.Invoker.Invoke takes argv, not HTTP
// requests, and xrr's http adapter models a client Request/Response
// wrapped at the outbound call site (see harness/harness.go Invoker,
// xrr adapters/http/http.go). MountMCP's surface is itself an
// http.Handler serving inbound JSON-RPC; there is nothing to record
// as outbound traffic here. This suite instead falls back to golden
// files + httptest with byte-exact assertions, which is what follows.
// Every response-body assertion in this suite is byte-
// exact. Most cases drive the surface end-to-end over real HTTP via
// httptest.NewServer; the sole exception
// (TestLegacyLock_ErrorCode_InternalError32603_UnreadableBody) calls
// the unexported mcpHandler.serveHTTP directly with an
// httptest.NewRecorder, because triggering a server-side
// io.ReadAll(req.Body) failure through a real network round-trip is
// inherently racy — that case is explained in place.
//
// encoding/json.Marshal on map[string]any sorts object keys
// lexicographically (verified against the Go stdlib), so the
// map[string]any-built response bodies in surface_mcp*.go are
// deterministic on the wire — no field reordering between runs, no
// timestamps or random IDs embedded in any response body asserted
// here (Meta.RequestedAt is request-side only and never echoed).
// That determinism is what makes byte-exact golden assertions valid
// for this surface.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
)

// --- fixed cobra tree -------------------------------------------------
//
// A second, self-contained tree (deliberately not shared with
// surface_mcp_test.go's newMCPTestTree — that tree is free to grow
// for future non-lock coverage; this one is frozen alongside the
// golden bytes below). Shape:
//
//	root
//	├── widget
//	│   ├── add     (write; flags: name str req, count int, force bool, tag []str, hidden/deprecated flags excluded from schema)
//	│   └── delete  (destructive)
//	├── secret      (auth-required)
//	├── deploy      (requires-confirmation)
//	└── ping        (read; the happy-path exec target)

func legacyLockTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

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
	add.Flags().Bool("force", false, "force flag")
	add.Flags().StringSlice("tag", nil, "tag list")
	add.Flags().String("hidden-flag", "", "should be hidden")
	_ = add.Flags().MarkHidden("hidden-flag")
	add.Flags().String("deprecated-flag", "", "should be dropped")
	_ = add.Flags().MarkDeprecated("deprecated-flag", "old")
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

	return root
}

// legacyLockServer mounts MountMCP with the given options over a
// fresh legacyLockTree bridge and returns a live httptest.Server.
// build defaults to a plain New(root) bridge (real InProcessRunner —
// leaf RunE funcs execute for real, so "ping" really prints "pong").
func legacyLockServer(t *testing.T, build func(root *cobra.Command) *Bridge, mountOpts ...MCPOption) *httptest.Server {
	t.Helper()
	root := legacyLockTree()
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

// rawPOST posts body (already-encoded bytes, or nil) to /mcp and
// returns the exact response bytes, status, and content-type header.
// No JSON decode/re-encode step — this is what makes the comparison
// byte-exact rather than structural.
func rawPOST(t *testing.T, srv *httptest.Server, path string, headers map[string]string, body []byte) (status int, contentType string, raw []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), raw
}

// assertByteExact fails the test with a readable diff when got !=
// want.
func assertByteExact(t *testing.T, label string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("%s: wire body mismatch\n got:  %s\nwant:  %s", label, got, want)
	}
}

// --- initialize ---------------------------------------------------

func TestLegacyLock_Initialize_Defaults(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, ct, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	if ct != "application/json" {
		t.Errorf("content-type=%q want=application/json", ct)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"tools":{}},"protocolVersion":"2024-11-05","serverInfo":{"name":"cmdsurface","version":"0.0.0"}}}` + "\n"
	assertByteExact(t, "initialize defaults", raw, []byte(want))
}

func TestLegacyLock_Initialize_ServerInfoOverride(t *testing.T) {
	srv := legacyLockServer(t, nil, WithMCPServerInfo("acme-cli", "1.2.3"))
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":"abc","method":"initialize"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":"abc","result":{"capabilities":{"tools":{}},"protocolVersion":"2024-11-05","serverInfo":{"name":"acme-cli","version":"1.2.3"}}}` + "\n"
	assertByteExact(t, "initialize serverInfo override", raw, []byte(want))
}

func TestLegacyLock_Initialize_NullID(t *testing.T) {
	// The legacy handler tolerates a null/absent id on any method
	// (no JSON-RPC notification handling exists); id round-trips
	// verbatim including "null".
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":null,"method":"initialize"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":null,"result":{"capabilities":{"tools":{}},"protocolVersion":"2024-11-05","serverInfo":{"name":"cmdsurface","version":"0.0.0"}}}` + "\n"
	assertByteExact(t, "initialize null id", raw, []byte(want))
}

func TestLegacyLock_Initialize_MissingJSONRPCMember(t *testing.T) {
	// jsonrpc member entirely absent is tolerated (only a non-empty,
	// non-"2.0" value is rejected) — current behavior, locked as-is.
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"id":1,"method":"initialize"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"tools":{}},"protocolVersion":"2024-11-05","serverInfo":{"name":"cmdsurface","version":"0.0.0"}}}` + "\n"
	assertByteExact(t, "initialize missing jsonrpc member", raw, []byte(want))
}

// --- tools/list -----------------------------------------------------

func TestLegacyLock_ToolsList_ExactDescriptors(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	// Tool order matches Bridge.Leaves() discovery order over
	// legacyLockTree(): cobra sorts each level's children
	// alphabetically by name by default (EnableCommandSorting),
	// so root's children walk as deploy, ping, secret, widget — and
	// widget's children as add, delete. hidden-flag / deprecated-flag
	// are excluded from the schema; "name" is the sole required
	// property.
	want := `{"jsonrpc":"2.0","id":1,"result":{"tools":[` +
		`{"description":"Deploy","inputSchema":{"properties":{},"type":"object"},"name":"deploy"},` +
		`{"description":"Ping the server","inputSchema":{"properties":{},"type":"object"},"name":"ping"},` +
		`{"description":"Locked","inputSchema":{"properties":{},"type":"object"},"name":"secret"},` +
		`{"description":"Add a widget","inputSchema":{"properties":{"count":{"description":"widget count","type":"integer"},"force":{"description":"force flag","type":"boolean"},"name":{"description":"widget name","type":"string"},"tag":{"description":"tag list","items":{"type":"string"},"type":"array"}},"required":["name"],"type":"object"},"name":"widget.add"},` +
		`{"description":"Delete a widget","inputSchema":{"properties":{},"type":"object"},"name":"widget.delete"}` +
		`]}}` + "\n"
	assertByteExact(t, "tools/list exact descriptors", raw, []byte(want))
}

func TestLegacyLock_ToolsList_FiltersDisabledSurface(t *testing.T) {
	srv := legacyLockServer(t, func(root *cobra.Command) *Bridge {
		return New(root).Hide("widget delete", SurfaceMCP)
	})
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"tools":[` +
		`{"description":"Deploy","inputSchema":{"properties":{},"type":"object"},"name":"deploy"},` +
		`{"description":"Ping the server","inputSchema":{"properties":{},"type":"object"},"name":"ping"},` +
		`{"description":"Locked","inputSchema":{"properties":{},"type":"object"},"name":"secret"},` +
		`{"description":"Add a widget","inputSchema":{"properties":{"count":{"description":"widget count","type":"integer"},"force":{"description":"force flag","type":"boolean"},"name":{"description":"widget name","type":"string"},"tag":{"description":"tag list","items":{"type":"string"},"type":"array"}},"required":["name"],"type":"object"},"name":"widget.add"}` +
		`]}}` + "\n"
	assertByteExact(t, "tools/list filters disabled surface", raw, []byte(want))
}

// --- tools/call -------------------------------------------------------

func TestLegacyLock_ToolsCall_HappyPath_RealExec(t *testing.T) {
	// Real InProcessRunner (no fake): the leaf's actual RunE executes.
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"pong\n","type":"text"}],"isError":false}}` + "\n"
	assertByteExact(t, "tools/call happy path (real exec)", raw, []byte(want))
}

func TestLegacyLock_ToolsCall_ArgumentMapping(t *testing.T) {
	captured := make(chan Invocation, 1)
	srv := legacyLockServer(t, func(root *cobra.Command) *Bridge {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, inv Invocation) (Result, error) {
				captured <- inv
				return Result{Stdout: "added\n"}, nil
			},
		}))
	})
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"widget.add","arguments":{"name":"gizmo","count":3,"force":true}}}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"added\n","type":"text"}],"isError":false}}` + "\n"
	assertByteExact(t, "tools/call argument mapping response", raw, []byte(want))

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
		// arguments decode through encoding/json into map[string]any:
		// numeric literals become float64. Locking this exact
		// (surprising, easy-to-regress) typed shape is the point.
		t.Errorf(`Flags["count"]=%v (%T) want=float64(3)`, inv.Flags["count"], inv.Flags["count"])
	}
	if inv.Flags["force"] != true {
		t.Errorf(`Flags["force"]=%v want=true`, inv.Flags["force"])
	}
}

func TestLegacyLock_ToolsCall_StderrAndDataBlocks(t *testing.T) {
	srv := legacyLockServer(t, func(root *cobra.Command) *Bridge {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, _ Invocation) (Result, error) {
				return Result{
					Stdout: "ok",
					Stderr: "warning",
					Data:   map[string]any{"id": 42},
				}, nil
			},
		}))
	})
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"ok","type":"text"},{"text":"[stderr] warning","type":"text"},{"text":"{\"id\":42}","type":"text"}],"isError":false}}` + "\n"
	assertByteExact(t, "tools/call stderr+data blocks", raw, []byte(want))
}

func TestLegacyLock_ToolsCall_NonZeroExitIsError(t *testing.T) {
	srv := legacyLockServer(t, func(root *cobra.Command) *Bridge {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, _ Invocation) (Result, error) {
				return Result{Stdout: "boom", ExitCode: 2}, nil
			},
		}))
	})
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"boom","type":"text"}],"isError":true}}` + "\n"
	assertByteExact(t, "tools/call non-zero exit", raw, []byte(want))
}

// --- JSON-RPC error codes: -32700, -32600, -32601, -32602, -32603 ---

func TestLegacyLock_ErrorCode_ParseError32700(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil, []byte(`{not valid json`))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	want := `{"jsonrpc":"2.0","error":{"code":-32700,"message":"parse error: invalid character 'n' looking for beginning of object key string"}}` + "\n"
	assertByteExact(t, "parse error -32700", raw, []byte(want))
}

func TestLegacyLock_ErrorCode_InvalidRequest32600(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"1.0","id":1,"method":"initialize"}`))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"invalid jsonrpc version"}}` + "\n"
	assertByteExact(t, "invalid request -32600", raw, []byte(want))
}

func TestLegacyLock_ErrorCode_MethodNotFound32601(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"nope/anywhere"}`))
	// Acknowledged current-behavior quirk (see ADR 0004 "Acknowledged
	// quirks"): method-not-found rides HTTP 200 on the legacy path.
	// This is the exact case the "bugs included" clause in the task
	// brief exists for — pinned as-is, not "fixed."
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200 (legacy quirk: -32601 rides HTTP 200)", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found: nope/anywhere"}}` + "\n"
	assertByteExact(t, "method not found -32601", raw, []byte(want))
}

func TestLegacyLock_ErrorCode_InvalidParams32602_UnparseableParams(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":"not-an-object"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"invalid params: json: cannot unmarshal string into Go value of type cmdsurface.callParams"}}` + "\n"
	assertByteExact(t, "invalid params -32602 (unparseable)", raw, []byte(want))
}

func TestLegacyLock_ErrorCode_InvalidParams32602_MissingToolName(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"missing tool name"}}` + "\n"
	assertByteExact(t, "invalid params -32602 (missing tool name)", raw, []byte(want))
}

func TestLegacyLock_ErrorCode_InvalidParams32602_UnknownTool(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope.nada"}}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"unknown tool: nope.nada"}}` + "\n"
	assertByteExact(t, "invalid params -32602 (unknown tool)", raw, []byte(want))
}

func TestLegacyLock_ErrorCode_InvalidParams32602_SurfaceNotEnabled(t *testing.T) {
	srv := legacyLockServer(t, func(root *cobra.Command) *Bridge {
		return New(root).Hide("ping", SurfaceMCP)
	})
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"unknown tool: ping"}}` + "\n"
	assertByteExact(t, "invalid params -32602 (surface not enabled)", raw, []byte(want))
}

func TestLegacyLock_ErrorCode_InternalError32603_UnreadableBody(t *testing.T) {
	// -32603 is reachable only via a body-read failure
	// (io.ReadAll(req.Body) error in serveHTTP). A real network
	// client cannot reliably trigger a server-side Body.Read error
	// through httptest's loopback listener — the failure has to be
	// injected at the http.Request.Body itself. This is the one
	// byte-exactness gap the "no network / deterministic" constraint
	// forces: rather than a flaky/racy network-level reproduction, we
	// call the real, unexported mcpHandler.serveHTTP directly (same
	// package, zero mocking of the code under test) with an
	// httptest.NewRecorder and a request whose Body always errors on
	// Read. This exercises the exact production line
	// (surface_mcp.go: io.ReadAll(req.Body)) and captures its
	// byte-exact output — no HTTP transport involved, no flake.
	root := legacyLockTree()
	b := New(root)
	h := &mcpHandler{b: b, cfg: mcpConfig{
		path:          defaultMCPPath,
		serverName:    defaultMCPServerName,
		serverVersion: defaultMCPServerVersion,
	}}

	req := httptest.NewRequest(http.MethodPost, "/mcp", &erroringReader{})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.serveHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type=%q want=application/json", ct)
	}
	want := `{"jsonrpc":"2.0","error":{"code":-32603,"message":"read request body: unexpected EOF"}}` + "\n"
	assertByteExact(t, "internal error -32603 (unreadable body)", rr.Body.Bytes(), []byte(want))
}

// erroringReader is an io.Reader that always fails, used to drive
// serveHTTP's io.ReadAll(req.Body) error branch (-32603).
type erroringReader struct{}

func (*erroringReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

// --- safety gate: destructive lockdown -------------------------------

func TestLegacyLock_SafetyGate_DestructiveBlockedByDefault(t *testing.T) {
	srv := legacyLockServer(t, nil) // default policy: no AllowDestructiveOn
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"widget.delete"}}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	// bridge.Invoke wraps ErrDestructiveBlocked with leaf path + surface
	// context (bridge.go: fmt.Errorf("%w: %s on %s", ...)); the MCP
	// handler renders err.Error() verbatim into the content block.
	want := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"cmdsurface: destructive command blocked on this surface: widget delete on mcp","type":"text"}],"isError":true}}` + "\n"
	assertByteExact(t, "destructive blocked by default", raw, []byte(want))
}

func TestLegacyLock_SafetyGate_AuthRequired(t *testing.T) {
	srv := legacyLockServer(t, func(root *cobra.Command) *Bridge {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, _ Invocation) (Result, error) { return Result{}, nil },
		}))
	})
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"secret"}}`))
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d want=401", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"authentication required","type":"text"}],"isError":true}}` + "\n"
	assertByteExact(t, "auth required (no header)", raw, []byte(want))

	status, _, raw = rawPOST(t, srv, "/mcp", map[string]string{"Authorization": "Bearer x"},
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"secret"}}`))
	if status != http.StatusOK {
		t.Fatalf("status with auth=%d want=200", status)
	}
	want2 := `{"jsonrpc":"2.0","id":2,"result":{"content":[{"text":"","type":"text"}],"isError":false}}` + "\n"
	assertByteExact(t, "auth required (with header)", raw, []byte(want2))
}

func TestLegacyLock_SafetyGate_ConfirmationRequired(t *testing.T) {
	srv := legacyLockServer(t, func(root *cobra.Command) *Bridge {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, _ Invocation) (Result, error) { return Result{}, nil },
		}))
	})
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy"}}`))
	if status != http.StatusPreconditionRequired {
		t.Fatalf("status=%d want=428", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"confirmation required","type":"text"}],"isError":true}}` + "\n"
	assertByteExact(t, "confirmation required (no token)", raw, []byte(want))

	status, _, raw = rawPOST(t, srv, "/mcp", map[string]string{"X-Confirm-Token": "yes"},
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy"}}`))
	if status != http.StatusOK {
		t.Fatalf("status with confirm=%d want=200", status)
	}
	want2 := `{"jsonrpc":"2.0","id":2,"result":{"content":[{"text":"","type":"text"}],"isError":false}}` + "\n"
	assertByteExact(t, "confirmation required (with token)", raw, []byte(want2))
}

func TestLegacyLock_SafetyGate_DestructiveAllowedWhenPolicyOptsIn(t *testing.T) {
	srv := legacyLockServer(t, func(root *cobra.Command) *Bridge {
		return New(root,
			WithPolicy(Policy{
				AllowDestructiveOn: []Surface{SurfaceMCP},
				DefaultEnabled:     []Surface{SurfaceCLI, SurfaceLib, SurfaceMCP},
			}),
			WithRunner(&fakeRunner{
				run: func(_ context.Context, _ Invocation) (Result, error) {
					return Result{Stdout: "deleted\n"}, nil
				},
			}),
		)
	})
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"widget.delete"}}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"deleted\n","type":"text"}],"isError":false}}` + "\n"
	assertByteExact(t, "destructive allowed by policy opt-in", raw, []byte(want))
}

// --- HTTP-level behavior: status codes, content-type, verbs, paths --

func TestLegacyLock_HTTP_ContentTypeAlwaysJSON(t *testing.T) {
	srv := legacyLockServer(t, nil)
	cases := []struct {
		name string
		body []byte
	}{
		{"initialize", []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)},
		{"tools/list", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)},
		{"unknown method", []byte(`{"jsonrpc":"2.0","id":1,"method":"nope"}`)},
		{"parse error", []byte(`not json`)},
	}
	for _, c := range cases {
		_, ct, _ := rawPOST(t, srv, "/mcp", nil, c.body)
		if ct != "application/json" {
			t.Errorf("%s: content-type=%q want=application/json", c.name, ct)
		}
	}
}

func TestLegacyLock_HTTP_WrongMethodNotAllowed(t *testing.T) {
	// Only POST is registered at /mcp (see MountMCP: r.Handle(http.MethodPost, ...)).
	// net/http.ServeMux returns a stdlib-standard 405 for GET on a
	// POST-only pattern. Status is what's locked here; the stdlib
	// error body text is not part of this surface's contract.
	srv := legacyLockServer(t, nil)
	resp, err := http.Get(srv.URL + "/mcp")
	if err != nil {
		t.Fatalf("GET /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp status=%d want=405", resp.StatusCode)
	}
}

func TestLegacyLock_HTTP_UnknownPathNotFound(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, _, _ := rawPOST(t, srv, "/not-mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if status != http.StatusNotFound {
		t.Errorf("POST /not-mcp status=%d want=404", status)
	}
}

func TestLegacyLock_HTTP_CustomMountPath(t *testing.T) {
	srv := legacyLockServer(t, nil, WithMCPPath("/custom/mcp"))
	status, _, raw := rawPOST(t, srv, "/custom/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"tools":{}},"protocolVersion":"2024-11-05","serverInfo":{"name":"cmdsurface","version":"0.0.0"}}}` + "\n"
	assertByteExact(t, "initialize via custom mount path", raw, []byte(want))

	// Default path is not mounted when overridden.
	status, _, _ = rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if status != http.StatusNotFound {
		t.Errorf("default /mcp status=%d want=404 (custom path overrides, not adds)", status)
	}
}
