package cmdsurface

// Coverage for the dual-spec era dispatcher (surface_mcp_dispatch.go):
// D1-D4 detection precedence, the ADR's 11-row worked edge-case
// table, the WithMCPSpecVersions / WithMCPCacheHints /
// WithMCPOriginAllowlist option surface, legacy-only bypass, and the
// MCPConfig declarative block.
//
// This file defines its own fixture tree (dispatchTestTree) per
// project convention — it does not reuse legacyLockTree
// (surface_mcp_legacy_lock_test.go, frozen) or newMCPTestTree
// (surface_mcp_test.go).
//
// Tests here assert ROUTING decisions (which handler served a
// request) and well-formedness of the modern-route placeholder's
// JSON-RPC error shape — not the placeholder's exact wire bytes,
// per the task-2 controller ruling: the real modern handler lands in
// a later task and will change those bytes.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
)

// dispatchTestTree builds a small tree for dispatcher tests:
//
//	root
//	└── ping   (read; the happy-path exec target)
func dispatchTestTree() *cobra.Command {
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
	return root
}

// dispatchServer mounts MountMCP with the given options over a fresh
// dispatchTestTree bridge and returns a live httptest.Server.
func dispatchServer(t *testing.T, mountOpts ...MCPOption) *httptest.Server {
	t.Helper()
	root := dispatchTestTree()
	b := New(root)
	r := api.NewRouter()
	if err := MountMCP(b, r, mountOpts...); err != nil {
		t.Fatalf("MountMCP: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// postJSON posts body to path on srv with optional headers and
// returns the decoded jsonRPCResponse-shaped map plus raw status.
func postJSON(t *testing.T, srv *httptest.Server, path string, headers map[string]string, body string) (status int, decoded map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
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
	if resp.StatusCode == http.StatusNoContent || resp.ContentLength == 0 {
		return resp.StatusCode, nil
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		// 202 notification responses have empty bodies; treat decode
		// failure on an otherwise-empty body as "no content".
		return resp.StatusCode, nil
	}
	return resp.StatusCode, m
}

// errCode extracts the numeric JSON-RPC error code from a decoded
// response, or 0 if absent.
func errCode(m map[string]any) int {
	e, ok := m["error"].(map[string]any)
	if !ok {
		return 0
	}
	c, ok := e["code"].(float64)
	if !ok {
		return 0
	}
	return int(c)
}

// --- D1: parse -----------------------------------------------------

func TestDispatch_D1_UnparseableBody_ParseErrorRegardlessOfHeaders(t *testing.T) {
	srv := dispatchServer(t)
	status, m := postJSON(t, srv, "/mcp", map[string]string{
		headerMCPMethod: "tools/call",
		headerMCPName:   "ping",
	}, `{not valid json`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	if code := errCode(m); code != mcpErrParse {
		t.Errorf("code=%d want=%d (-32700)", code, mcpErrParse)
	}
}

func TestDispatch_D1_UnreadableBody_InternalErrorRegardlessOfHeaders(t *testing.T) {
	// Mirrors the legacy lock's own unreadable-body case: driven
	// directly against the dispatcher (same-package, no network) since
	// a real client cannot reliably trigger a server-side Body.Read
	// error over httptest's loopback listener.
	root := dispatchTestTree()
	b := New(root)
	legacy := &mcpHandler{b: b, cfg: mcpConfig{
		path:          defaultMCPPath,
		serverName:    defaultMCPServerName,
		serverVersion: defaultMCPServerVersion,
	}}
	d := &mcpDispatcher{legacy: legacy, modern: &mcpModernHandlerSeam{}}

	req := httptest.NewRequest(http.MethodPost, "/mcp", &erroringReader{})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerMCPMethod, "tools/call")
	rr := httptest.NewRecorder()

	d.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", rr.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code := errCode(m); code != mcpErrInternal {
		t.Errorf("code=%d want=%d (-32603)", code, mcpErrInternal)
	}
}

// --- D2: initialize is legacy unconditionally -----------------------

func TestDispatch_D2_InitializeRoutesLegacy_NoMarkers(t *testing.T) {
	srv := dispatchServer(t)
	status, m := postJSON(t, srv, "/mcp", nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in response: %v", m)
	}
	if res["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion=%v want=2024-11-05 (legacy route)", res["protocolVersion"])
	}
}

func TestDispatch_D2_InitializeRoutesLegacy_EvenWithModernMarkers(t *testing.T) {
	srv := dispatchServer(t)
	status, m := postJSON(t, srv, "/mcp", map[string]string{
		headerMCPMethod: "initialize",
	}, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize+markers must still route legacy, got: %v", m)
	}
	if res["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion=%v want=2024-11-05 (D2: method wins)", res["protocolVersion"])
	}
}

// --- D3: any marker routes modern ------------------------------------

func TestDispatch_D3_M1_MethodHeaderRoutesModern(t *testing.T) {
	srv := dispatchServer(t)
	status, m := postJSON(t, srv, "/mcp", map[string]string{
		headerMCPMethod: "tools/call",
	}, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400 (modern placeholder)", status)
	}
	if code := errCode(m); code != mcpErrUnsupportedVersion {
		t.Errorf("code=%d want=%d (-32022, modern placeholder)", code, mcpErrUnsupportedVersion)
	}
}

func TestDispatch_D3_M2_NameHeaderRoutesModern(t *testing.T) {
	srv := dispatchServer(t)
	status, m := postJSON(t, srv, "/mcp", map[string]string{
		headerMCPName: "ping",
	}, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400 (modern placeholder)", status)
	}
	if code := errCode(m); code != mcpErrUnsupportedVersion {
		t.Errorf("code=%d want=%d (-32022, modern placeholder)", code, mcpErrUnsupportedVersion)
	}
}

func TestDispatch_D3_M3_MetaProtocolVersionKeyRoutesModern(t *testing.T) {
	srv := dispatchServer(t)
	status, m := postJSON(t, srv, "/mcp", nil,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400 (modern placeholder)", status)
	}
	if code := errCode(m); code != mcpErrUnsupportedVersion {
		t.Errorf("code=%d want=%d (-32022, modern placeholder)", code, mcpErrUnsupportedVersion)
	}
}

func TestDispatch_D3_M4_ServerDiscoverRoutesModern(t *testing.T) {
	srv := dispatchServer(t)
	status, m := postJSON(t, srv, "/mcp", nil,
		`{"jsonrpc":"2.0","id":1,"method":"server/discover"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400 (modern placeholder)", status)
	}
	if code := errCode(m); code != mcpErrUnsupportedVersion {
		t.Errorf("code=%d want=%d (-32022, modern placeholder)", code, mcpErrUnsupportedVersion)
	}
}

// --- D4: otherwise legacy --------------------------------------------

func TestDispatch_D4_NoMarkers_RoutesLegacy(t *testing.T) {
	srv := dispatchServer(t)
	status, m := postJSON(t, srv, "/mcp", nil,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`)
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200 (legacy route)", status)
	}
	if _, ok := m["error"]; ok {
		t.Fatalf("expected legacy success, got error: %v", m)
	}
}

// --- ADR 0004 worked edge-case table (11 rows, both versions enabled) -

func TestDispatch_WorkedEdgeCases(t *testing.T) {
	srv := dispatchServer(t)

	cases := []struct {
		name       string
		headers    map[string]string
		body       string
		wantStatus int
		wantLegacy bool // response carries a legacy-shaped payload
		wantModern bool // response is the modern placeholder (-32022)
	}{
		{
			name:       "bare initialize",
			body:       `{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
			wantStatus: http.StatusOK,
			wantLegacy: true,
		},
		{
			name:       "initialize + marker",
			headers:    map[string]string{headerMCPMethod: "initialize"},
			body:       `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
			wantStatus: http.StatusOK,
			wantLegacy: true,
		},
		{
			name:       "bare tools/list",
			body:       `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			wantStatus: http.StatusOK,
			wantLegacy: true,
		},
		{
			name:       "tools/call with only MCP-Protocol-Version header (mid-era)",
			headers:    map[string]string{headerMCPProtocolVersion: "2024-11-05"},
			body:       `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`,
			wantStatus: http.StatusOK,
			wantLegacy: true,
		},
		{
			name:       "bare unknown method",
			body:       `{"jsonrpc":"2.0","id":1,"method":"nope/nowhere"}`,
			wantStatus: http.StatusOK, // legacy quirk: -32601 rides HTTP 200
			wantLegacy: true,
		},
		{
			name:       "tools/call with _meta protocolVersion key only (M3), no headers",
			body:       `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
			wantStatus: http.StatusBadRequest,
			wantModern: true,
		},
		{
			name: "tools/call with complete _meta, no headers",
			body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping",` +
				`"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
			wantStatus: http.StatusBadRequest,
			wantModern: true,
		},
		{
			name:       "tools/call with M1 only (no _meta)",
			headers:    map[string]string{headerMCPMethod: "tools/call"},
			body:       `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`,
			wantStatus: http.StatusBadRequest,
			wantModern: true,
		},
		{
			name:       "bare server/discover",
			body:       `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`,
			wantStatus: http.StatusBadRequest,
			wantModern: true,
		},
		{
			name:       "unknown method + valid modern envelope",
			headers:    map[string]string{headerMCPMethod: "nope/nowhere"},
			body:       `{"jsonrpc":"2.0","id":1,"method":"nope/nowhere","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
			wantStatus: http.StatusBadRequest, // modern placeholder: -32022 @ 400 (real V8 404 lands with the modern handler task)
			wantModern: true,
		},
		{
			name:       "unparseable body (any headers)",
			headers:    map[string]string{headerMCPMethod: "tools/call", headerMCPName: "ping"},
			body:       `{not valid json`,
			wantStatus: http.StatusBadRequest,
		},
		{
			// Real V2 notification handling (HTTP 202, empty body,
			// discard) is the next task's scope; today the request
			// still ROUTES modern (which is what this row asserts) and
			// the placeholder answers -32022 rather than 202. Wire
			// bytes beyond routing are intentionally not pinned here,
			// per the task-2 controller ruling.
			name:       "notification (no id) + markers",
			headers:    map[string]string{headerMCPMethod: "tools/call"},
			body:       `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
			wantStatus: http.StatusBadRequest,
			wantModern: true,
		},
		{
			// Real V2 malformed-id handling (-32600) is the next
			// task's scope; today the request still ROUTES modern.
			name:       "id: null + markers",
			headers:    map[string]string{headerMCPMethod: "tools/call"},
			body:       `{"jsonrpc":"2.0","id":null,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
			wantStatus: http.StatusBadRequest,
			wantModern: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, m := postJSON(t, srv, "/mcp", c.headers, c.body)
			if status != c.wantStatus {
				t.Fatalf("status=%d want=%d", status, c.wantStatus)
			}
			switch {
			case c.wantLegacy:
				if _, hasResult := m["result"]; !hasResult {
					if code := errCode(m); code == mcpErrUnsupportedVersion {
						t.Fatalf("routed modern, want legacy: %v", m)
					}
				}
			case c.wantModern:
				if code := errCode(m); code != mcpErrUnsupportedVersion {
					t.Fatalf("code=%d want=%d (modern placeholder): %v", code, mcpErrUnsupportedVersion, m)
				}
			}
		})
	}
}

// Notification (no id) + markers, and id:null + markers are exercised
// separately since the modern placeholder always answers with an
// error envelope today (the real modern V2 notification/null-id
// handling lands with the modern handler task) — these two rows are
// asserted at the DETECTION level (detectMCPEra) rather than the
// wire, since the placeholder cannot yet honor HTTP 202/-32600
// semantics that depend on unimplemented V2 logic.
func TestDispatch_WorkedEdgeCases_DetectionOnly_NotificationAndNullID(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set(headerMCPMethod, "tools/call")

	notification := jsonRPCRequest{
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"ping","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`),
	}
	if era := detectMCPEra(req, notification); era != mcpEraModern {
		t.Errorf("notification+markers era=%v want=modern", era)
	}

	nullID := jsonRPCRequest{
		ID:     json.RawMessage(`null`),
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"ping","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`),
	}
	if era := detectMCPEra(req, nullID); era != mcpEraModern {
		t.Errorf("id:null+markers era=%v want=modern", era)
	}
}

// --- Deliberate non-markers (cross-check against detectMCPEra) ------

func TestDispatch_NonMarker_BareParamsMetaWithoutReservedKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rpc := jsonRPCRequest{
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"ping","_meta":{"progressToken":"abc"}}`),
	}
	if era := detectMCPEra(req, rpc); era != mcpEraLegacy {
		t.Errorf("bare _meta (no reserved key) era=%v want=legacy", era)
	}
}

func TestDispatch_NonMarker_ProtocolVersionHeaderAlone(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set(headerMCPProtocolVersion, "2024-11-05")
	rpc := jsonRPCRequest{Method: "tools/list"}
	if era := detectMCPEra(req, rpc); era != mcpEraLegacy {
		t.Errorf("MCP-Protocol-Version header alone era=%v want=legacy", era)
	}
}

// --- Options: WithMCPSpecVersions ------------------------------------

func TestOption_SpecVersions_DefaultBothEnabled(t *testing.T) {
	srv := dispatchServer(t) // no WithMCPSpecVersions
	// Legacy-shaped request still works.
	status, _ := postJSON(t, srv, "/mcp", nil, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if status != http.StatusOK {
		t.Fatalf("legacy initialize status=%d want=200", status)
	}
	// Modern-marked request also gets routed (placeholder answers).
	status, m := postJSON(t, srv, "/mcp", map[string]string{headerMCPMethod: "tools/call"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`)
	if status != http.StatusBadRequest {
		t.Fatalf("modern-marked status=%d want=400", status)
	}
	if code := errCode(m); code != mcpErrUnsupportedVersion {
		t.Errorf("code=%d want=%d", code, mcpErrUnsupportedVersion)
	}
}

func TestOption_SpecVersions_LegacyOnly_BypassesDispatcher(t *testing.T) {
	root := dispatchTestTree()
	b := New(root)
	r := api.NewRouter()
	if err := MountMCP(b, r, WithMCPSpecVersions(MCPSpec20241105)); err != nil {
		t.Fatalf("MountMCP: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// A modern-marked request is ignored: markers are simply not
	// inspected because the dispatcher is not in the path at all.
	status, m := postJSON(t, srv, "/mcp", map[string]string{headerMCPMethod: "tools/call"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`)
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200 (legacy-only bypass)", status)
	}
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected legacy tools/call result, got: %v", m)
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("expected content blocks, got: %v", res)
	}

	// GET/DELETE are not registered when legacy-only (no 405 handler
	// added) — stdlib ServeMux answers its own 405 exactly as the
	// legacy conformance lock pins for POST-only mounts.
	resp, err := http.Get(srv.URL + "/mcp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET status=%d want=405", resp.StatusCode)
	}
}

func TestOption_SpecVersions_ModernOnly_InitializeFailsModernValidation(t *testing.T) {
	root := dispatchTestTree()
	b := New(root)
	r := api.NewRouter()
	if err := MountMCP(b, r, WithMCPSpecVersions(MCPSpec20260728)); err != nil {
		t.Fatalf("MountMCP: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// A bare legacy initialize, with NO modern markers, still routes
	// modern (no special-casing when legacy isn't mounted at all) and
	// fails the modern handler's own validation.
	status, m := postJSON(t, srv, "/mcp", nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400 (modern-only rejects legacy initialize)", status)
	}
	if code := errCode(m); code != mcpErrUnsupportedVersion {
		t.Errorf("code=%d want=%d", code, mcpErrUnsupportedVersion)
	}

	// GET/DELETE registered (405) when modern is enabled.
	resp, err := http.Get(srv.URL + "/mcp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET status=%d want=405", resp.StatusCode)
	}
}

func TestOption_SpecVersions_EmptyCall_MountTimeError(t *testing.T) {
	root := dispatchTestTree()
	b := New(root)
	r := api.NewRouter()
	err := MountMCP(b, r, WithMCPSpecVersions())
	if err == nil {
		t.Fatal("expected mount-time error for empty WithMCPSpecVersions() call")
	}
}

func TestOption_SpecVersions_UnknownVersion_MountTimeError(t *testing.T) {
	root := dispatchTestTree()
	b := New(root)
	r := api.NewRouter()
	err := MountMCP(b, r, WithMCPSpecVersions(MCPSpecVersion("1999-01-01")))
	if err == nil {
		t.Fatal("expected mount-time error for unrecognized spec version")
	}
}

func TestOption_SpecVersions_DuplicatesDeduplicated(t *testing.T) {
	root := dispatchTestTree()
	b := New(root)
	r := api.NewRouter()
	err := MountMCP(b, r, WithMCPSpecVersions(MCPSpec20241105, MCPSpec20241105, MCPSpec20241105))
	if err != nil {
		t.Fatalf("MountMCP with duplicate versions: %v", err)
	}
}

// --- Options: WithMCPCacheHints ---------------------------------------

func TestOption_CacheHints_NegativeTTL_MountTimeError(t *testing.T) {
	root := dispatchTestTree()
	b := New(root)
	r := api.NewRouter()
	err := MountMCP(b, r, WithMCPCacheHints(-1*time.Millisecond, MCPCacheScopePrivate))
	if err == nil {
		t.Fatal("expected mount-time error for negative ttl")
	}
}

func TestOption_CacheHints_UnknownScope_MountTimeError(t *testing.T) {
	root := dispatchTestTree()
	b := New(root)
	r := api.NewRouter()
	err := MountMCP(b, r, WithMCPCacheHints(0, MCPCacheScope("bogus")))
	if err == nil {
		t.Fatal("expected mount-time error for unknown cache scope")
	}
}

func TestOption_CacheHints_ValidValuesAccepted(t *testing.T) {
	root := dispatchTestTree()
	b := New(root)
	r := api.NewRouter()
	err := MountMCP(b, r, WithMCPCacheHints(5*time.Second, MCPCacheScopePublic))
	if err != nil {
		t.Fatalf("MountMCP with valid cache hints: %v", err)
	}
}

// --- Options: WithMCPOriginAllowlist -----------------------------------

func TestOption_OriginAllowlist_MountSucceeds(t *testing.T) {
	root := dispatchTestTree()
	b := New(root)
	r := api.NewRouter()
	err := MountMCP(b, r, WithMCPOriginAllowlist("https://app.example.com"))
	if err != nil {
		t.Fatalf("MountMCP with origin allowlist: %v", err)
	}
}

// --- Existing-call compatibility --------------------------------------

func TestMountMCP_ExistingCallsCompileAndBehaveUnchanged(t *testing.T) {
	// No new options at all — the exact call shape that predates this
	// track. Both versions enabled by default; legacy traffic is
	// unaffected.
	srv := dispatchServer(t)
	status, m := postJSON(t, srv, "/mcp", nil, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	res, ok := m["result"].(map[string]any)
	if !ok || res["protocolVersion"] != "2024-11-05" {
		t.Fatalf("unexpected result: %v", m)
	}
}

func TestMountMCP_WithMCPPathAndServerInfo_StillCompile(t *testing.T) {
	root := dispatchTestTree()
	b := New(root)
	r := api.NewRouter()
	err := MountMCP(b, r, WithMCPPath("/custom"), WithMCPServerInfo("acme", "9.9.9"))
	if err != nil {
		t.Fatalf("MountMCP: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	status, m := postJSON(t, srv, "/custom", nil, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	res := m["result"].(map[string]any)
	si := res["serverInfo"].(map[string]any)
	if si["name"] != "acme" || si["version"] != "9.9.9" {
		t.Errorf("serverInfo=%v want name=acme version=9.9.9", si)
	}
}

// --- No shared mutable session state -----------------------------------

func TestDispatch_ConcurrentRequests_NoSharedState(t *testing.T) {
	srv := dispatchServer(t)
	const n = 50
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			// Alternate legacy and modern-marked requests on the same
			// mount concurrently; a shared-state bug would show up as
			// cross-talk (wrong protocolVersion, wrong error code).
			if i%2 == 0 {
				status, m := postJSON(t, srv, "/mcp", nil,
					`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
				if status != http.StatusOK {
					t.Errorf("legacy status=%d want=200", status)
					return
				}
				res, ok := m["result"].(map[string]any)
				if !ok || res["protocolVersion"] != "2024-11-05" {
					t.Errorf("legacy result corrupted: %v", m)
				}
			} else {
				status, m := postJSON(t, srv, "/mcp", map[string]string{headerMCPMethod: "tools/call"},
					`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`)
				if status != http.StatusBadRequest {
					t.Errorf("modern status=%d want=400", status)
					return
				}
				if code := errCode(m); code != mcpErrUnsupportedVersion {
					t.Errorf("modern result corrupted: %v", m)
				}
			}
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}
}

// --- Config block: declarative-only, FromConfig mounts nothing ------

func TestConfig_MCPBlock_ParsesFields(t *testing.T) {
	yamlSrc := `
mcp:
  spec_versions: ["2024-11-05", "2026-07-28"]
  path: /custom/mcp
  cache_ttl_ms: 5000
  cache_scope: public
  origin_allowlist: ["https://app.example.com"]
`
	cfg, err := Load(strings.NewReader(yamlSrc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCP == nil {
		t.Fatal("cfg.MCP is nil")
	}
	if len(cfg.MCP.SpecVersions) != 2 {
		t.Errorf("SpecVersions=%v", cfg.MCP.SpecVersions)
	}
	if cfg.MCP.Path != "/custom/mcp" {
		t.Errorf("Path=%q", cfg.MCP.Path)
	}
	if cfg.MCP.CacheTTLMs != 5000 {
		t.Errorf("CacheTTLMs=%d", cfg.MCP.CacheTTLMs)
	}
	if cfg.MCP.CacheScope != "public" {
		t.Errorf("CacheScope=%q", cfg.MCP.CacheScope)
	}
	if len(cfg.MCP.OriginAllowlist) != 1 || cfg.MCP.OriginAllowlist[0] != "https://app.example.com" {
		t.Errorf("OriginAllowlist=%v", cfg.MCP.OriginAllowlist)
	}
}

func TestConfig_MCPBlock_AbsentLeavesNil(t *testing.T) {
	cfg, err := Load(strings.NewReader("surfaces:\n  defaults: [cli]\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MCP != nil {
		t.Errorf("cfg.MCP=%v want=nil", cfg.MCP)
	}
}

func TestConfig_MCPBlock_FromConfigMountsNothing(t *testing.T) {
	// FromConfig is declarative-only for MCP, matching webhook/bus/
	// cron: it must not itself call MountMCP or otherwise construct
	// HTTP handlers. There is no direct observable for "did not
	// mount an HTTP server" beyond confirming FromConfig succeeds and
	// returns a plain *Bridge with no MCP-specific side effects (the
	// bridge has no field referencing an MCP mount).
	root := dispatchTestTree()
	yamlSrc := `
mcp:
  path: /custom/mcp
surfaces:
  defaults: [cli, mcp]
`
	cfg, err := Load(strings.NewReader(yamlSrc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b, err := FromConfig(root, cfg)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	if b == nil {
		t.Fatal("FromConfig returned nil bridge")
	}
	// The bridge's leaves are still discoverable/invocable directly —
	// FromConfig's job is enablement, not transport mounting.
	res, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"ping"},
		Meta: Meta{Surface: SurfaceMCP},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.Stdout != "pong\n" {
		t.Errorf("Stdout=%q want=pong\\n", res.Stdout)
	}
}
