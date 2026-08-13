package cmdsurface

// Coverage for the modern (2026-07-28) handler core
// (surface_mcp_modern.go): the V1-V8 validation chain (positive +
// negative per rule), server/discover, the Origin allowlist, cache
// hints, result-envelope stamping, and the supported-versions text
// appended to errors rejecting an initialize request. tools/list and
// tools/call coverage lives in surface_mcp_modern_list_test.go and
// surface_mcp_modern_call_test.go.
//
// This file defines its own fixture tree (modernTestTree) per project
// convention — it does not reuse legacyLockTree or newMCPTestTree.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
)

// modernTestTree builds the fixture tree for modern-handler tests.
// Leaf discovery is depth-first over cobra's alphabetically sorted
// child lists, so tools/list order is alphabetical per cobra level:
//
//	root
//	├── auth-op     (auth-required)
//	├── confirm-op  (requires-confirmation)
//	├── nuke        (destructive)
//	├── ping        (read; prints "pong")
//	└── widget
//	    ├── add     (write; flags: name str required, count int)
//	    └── list    (read)
func modernTestTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

	widget := &cobra.Command{Use: "widget"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List widgets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("w1")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	add := &cobra.Command{
		Use:   "add",
		Short: "Add a widget",
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _ := cmd.Flags().GetString("name")
			count, _ := cmd.Flags().GetInt("count")
			cmd.Printf("added %s x%d\n", name, count)
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "write"},
	}
	add.Flags().String("name", "", "widget name")
	add.Flags().Int("count", 1, "how many")
	_ = add.MarkFlagRequired("name")
	widget.AddCommand(list, add)

	ping := &cobra.Command{
		Use:   "ping",
		Short: "Ping the server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("pong")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	nuke := &cobra.Command{
		Use:   "nuke",
		Short: "Destroy everything",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("boom")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	}
	authOp := &cobra.Command{
		Use:   "auth-op",
		Short: "Operation requiring auth",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("authorized")
			return nil
		},
		Annotations: map[string]string{
			"kit/side-effect":   "write",
			"kit/auth-required": "true",
		},
	}
	confirmOp := &cobra.Command{
		Use:   "confirm-op",
		Short: "Operation requiring confirmation",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("confirmed")
			return nil
		},
		Annotations: map[string]string{
			"kit/side-effect":           "write",
			"kit/requires-confirmation": "true",
		},
	}

	root.AddCommand(widget, ping, nuke, authOp, confirmOp)
	return root
}

// modernServerFor mounts MountMCP over the given bridge and returns a
// live test server.
func modernServerFor(t *testing.T, b *Bridge, mountOpts ...MCPOption) *httptest.Server {
	t.Helper()
	r := api.NewRouter()
	if err := MountMCP(b, r, mountOpts...); err != nil {
		t.Fatalf("MountMCP: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// modernServer mounts MountMCP over a fresh modernTestTree bridge.
func modernServer(t *testing.T, mountOpts ...MCPOption) *httptest.Server {
	t.Helper()
	return modernServerFor(t, New(modernTestTree()), mountOpts...)
}

// modernMeta returns a complete reserved _meta object (both required
// keys) for splicing into request params.
func modernMeta() map[string]any {
	return map[string]any{
		metaKeyProtocolVersion:    mcpModernProtocolVersion,
		metaKeyClientCapabilities: map[string]any{},
	}
}

// modernBodyWithMeta renders a JSON-RPC request body with the given
// method, id 1, and params spliced with the given _meta object.
func modernBodyWithMeta(t *testing.T, method string, params, meta map[string]any) string {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	if meta != nil {
		params["_meta"] = meta
	}
	enc, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return string(enc)
}

// modernBody renders a conforming modern request body: method, id 1,
// params spliced with a complete reserved _meta.
func modernBody(t *testing.T, method string, params map[string]any) string {
	t.Helper()
	return modernBodyWithMeta(t, method, params, modernMeta())
}

// modernHeaders returns the headers a conforming modern request
// carries: MCP-Protocol-Version plus Mcp-Method mirroring the body
// method, and Mcp-Name when name is non-empty.
func modernHeaders(method, name string) map[string]string {
	h := map[string]string{
		headerMCPProtocolVersion: mcpModernProtocolVersion,
		headerMCPMethod:          method,
	}
	if name != "" {
		h[headerMCPName] = name
	}
	return h
}

// errMessage extracts the JSON-RPC error message from a decoded
// response, or "".
func errMessage(m map[string]any) string {
	e, ok := m["error"].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := e["message"].(string)
	return s
}

// errData extracts the JSON-RPC error data object from a decoded
// response, or nil.
func errData(m map[string]any) map[string]any {
	e, ok := m["error"].(map[string]any)
	if !ok {
		return nil
	}
	d, _ := e["data"].(map[string]any)
	return d
}

// --- V1: jsonrpc member ----------------------------------------------

func TestModern_V1_InvalidJSONRPCVersion(t *testing.T) {
	srv := modernServer(t)
	body := `{"jsonrpc":"1.0","id":1,"method":"server/discover","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	status, m := postJSON(t, srv, "/mcp", modernHeaders("server/discover", ""), body)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	if code := errCode(m); code != mcpErrInvalidRequest {
		t.Errorf("code=%d want=%d", code, mcpErrInvalidRequest)
	}
}

func TestModern_V1_AbsentJSONRPCMemberTolerated(t *testing.T) {
	// Same tolerance as legacy: a missing jsonrpc member is accepted.
	srv := modernServer(t)
	body := `{"id":1,"method":"server/discover","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	status, m := postJSON(t, srv, "/mcp", modernHeaders("server/discover", ""), body)
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	if _, ok := m["result"].(map[string]any); !ok {
		t.Fatalf("expected result, got: %v", m)
	}
}

// --- V2: id / notification -------------------------------------------

func TestModern_V2_Notification_202AndDiscarded(t *testing.T) {
	var calls atomic.Int32
	b := New(modernTestTree(), WithRunner(&fakeRunner{
		run: func(context.Context, Invocation) (Result, error) {
			calls.Add(1)
			return Result{Stdout: "ok"}, nil
		},
	}))
	srv := modernServerFor(t, b)

	// Fully valid modern tools/call, but no id: a notification. It is
	// acknowledged with 202 and NOT executed.
	body := `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"ping","_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "ping"), body)
	if status != http.StatusAccepted {
		t.Fatalf("status=%d want=202", status)
	}
	if m != nil {
		t.Errorf("expected empty body, got: %v", m)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("runner invoked %d times for a notification, want 0", n)
	}
}

func TestModern_V2_Notification_202EvenWithIncompleteMeta(t *testing.T) {
	// V2 precedes V3: a notification is acknowledged before _meta
	// validation would have rejected the request.
	srv := modernServer(t)
	body := `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"ping","_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	status, _ := postJSON(t, srv, "/mcp", nil, body)
	if status != http.StatusAccepted {
		t.Fatalf("status=%d want=202", status)
	}
}

func TestModern_V2_NullID(t *testing.T) {
	srv := modernServer(t)
	body := `{"jsonrpc":"2.0","id":null,"method":"server/discover","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	status, m := postJSON(t, srv, "/mcp", modernHeaders("server/discover", ""), body)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	if code := errCode(m); code != mcpErrInvalidRequest {
		t.Errorf("code=%d want=%d", code, mcpErrInvalidRequest)
	}
}

// --- V3: required _meta keys ------------------------------------------

func TestModern_V3_MissingMeta(t *testing.T) {
	srv := modernServer(t)
	cases := []struct {
		name string
		body string
	}{
		{
			name: "no params at all",
			body: `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`,
		},
		{
			name: "params without _meta",
			body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`,
		},
		{
			name: "_meta null",
			body: `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":null}}`,
		},
		{
			name: "_meta not an object",
			body: `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":5}}`,
		},
		{
			name: "missing protocolVersion key",
			body: `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{` +
				`"io.modelcontextprotocol/clientCapabilities":{}}}}`,
		},
		{
			name: "missing clientCapabilities key",
			body: `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{` +
				`"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Mcp-Method marker forces the modern route even when the
			// body carries no modern marker of its own.
			status, m := postJSON(t, srv, "/mcp", map[string]string{
				headerMCPMethod:          "server/discover",
				headerMCPProtocolVersion: mcpModernProtocolVersion,
			}, c.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status=%d want=400: %v", status, m)
			}
			if code := errCode(m); code != mcpErrInvalidParams {
				t.Errorf("code=%d want=%d", code, mcpErrInvalidParams)
			}
		})
	}
}

func TestModern_V3_ClientInfoOptional(t *testing.T) {
	// clientInfo absent: request is fully served (positive V3 case).
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("server/discover", ""),
		modernBody(t, "server/discover", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
}

// --- V4: MCP-Protocol-Version header consistency ----------------------

func TestModern_V4_MissingHeader(t *testing.T) {
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", nil, // no headers at all; M3 routes modern
		modernBody(t, "server/discover", nil))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModern_V4_HeaderMismatch(t *testing.T) {
	srv := modernServer(t)
	headers := map[string]string{
		headerMCPProtocolVersion: "2025-06-18",
		headerMCPMethod:          "server/discover",
	}
	status, m := postJSON(t, srv, "/mcp", headers, modernBody(t, "server/discover", nil))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModern_V4_NonStringMetaVersion(t *testing.T) {
	// A non-string _meta protocolVersion can never equal the header
	// string, so the request fails header/body agreement (V4) rather
	// than version support (V5).
	srv := modernServer(t)
	meta := map[string]any{
		metaKeyProtocolVersion:    42,
		metaKeyClientCapabilities: map[string]any{},
	}
	headers := map[string]string{
		headerMCPProtocolVersion: "42",
		headerMCPMethod:          "server/discover",
	}
	status, m := postJSON(t, srv, "/mcp", headers,
		modernBodyWithMeta(t, "server/discover", nil, meta))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

// --- V5: version support ----------------------------------------------

func TestModern_V5_UnsupportedVersion(t *testing.T) {
	srv := modernServer(t)
	meta := map[string]any{
		metaKeyProtocolVersion:    "2025-03-26",
		metaKeyClientCapabilities: map[string]any{},
	}
	headers := map[string]string{
		headerMCPProtocolVersion: "2025-03-26", // agrees with _meta: V4 passes
		headerMCPMethod:          "server/discover",
	}
	status, m := postJSON(t, srv, "/mcp", headers,
		modernBodyWithMeta(t, "server/discover", nil, meta))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	if code := errCode(m); code != mcpErrUnsupportedVersion {
		t.Fatalf("code=%d want=%d (-32022)", code, mcpErrUnsupportedVersion)
	}
	data := errData(m)
	if data == nil {
		t.Fatal("expected error data payload")
	}
	if data["requested"] != "2025-03-26" {
		t.Errorf("data.requested=%v want=2025-03-26", data["requested"])
	}
	supported, _ := data["supported"].([]any)
	if len(supported) != 1 || supported[0] != mcpModernProtocolVersion {
		t.Errorf("data.supported=%v want=[%s]", supported, mcpModernProtocolVersion)
	}
}

func TestModern_V5_SupportedListExcludesLegacyVersion(t *testing.T) {
	// The legacy revision is only reachable through its handshake, so
	// -32022's supported list must not advertise it as a per-request
	// version.
	srv := modernServer(t)
	meta := map[string]any{
		metaKeyProtocolVersion:    "2024-11-05",
		metaKeyClientCapabilities: map[string]any{},
	}
	headers := map[string]string{
		headerMCPProtocolVersion: "2024-11-05",
		headerMCPMethod:          "tools/list",
	}
	status, m := postJSON(t, srv, "/mcp", headers,
		modernBodyWithMeta(t, "tools/list", nil, meta))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	if code := errCode(m); code != mcpErrUnsupportedVersion {
		t.Fatalf("code=%d want=%d (-32022)", code, mcpErrUnsupportedVersion)
	}
	supported, _ := errData(m)["supported"].([]any)
	for _, v := range supported {
		if v == "2024-11-05" {
			t.Errorf("supported list %v must not include the handshake-only legacy version", supported)
		}
	}
}

// --- V6: Mcp-Method header slot ---------------------------------------

func TestModern_V6_MethodHeaderNotYetEnforced(t *testing.T) {
	// Header/body agreement for Mcp-Method is a validation slot that
	// currently accepts every request: a missing or mismatched header
	// does not fail the call today.
	srv := modernServer(t)

	// Absent Mcp-Method header (request routes modern via _meta M3).
	status, m := postJSON(t, srv, "/mcp", map[string]string{
		headerMCPProtocolVersion: mcpModernProtocolVersion,
	}, modernBody(t, "server/discover", nil))
	if status != http.StatusOK {
		t.Fatalf("absent header: status=%d want=200: %v", status, m)
	}

	// Mismatched Mcp-Method header.
	status, m = postJSON(t, srv, "/mcp", map[string]string{
		headerMCPProtocolVersion: mcpModernProtocolVersion,
		headerMCPMethod:          "tools/call",
	}, modernBody(t, "server/discover", nil))
	if status != http.StatusOK {
		t.Fatalf("mismatched header: status=%d want=200: %v", status, m)
	}
}

// --- V8: method routing -----------------------------------------------

func TestModern_V8_UnknownMethod404(t *testing.T) {
	srv := modernServer(t)
	for _, method := range []string{"nope/nowhere", "prompts/list", "resources/list", "tasks/get"} {
		t.Run(method, func(t *testing.T) {
			status, m := postJSON(t, srv, "/mcp", modernHeaders(method, ""),
				modernBody(t, method, nil))
			if status != http.StatusNotFound {
				t.Fatalf("status=%d want=404", status)
			}
			if code := errCode(m); code != mcpErrMethodNotFound {
				t.Errorf("code=%d want=%d (-32601)", code, mcpErrMethodNotFound)
			}
			if msg := errMessage(m); !strings.Contains(msg, method) {
				t.Errorf("message=%q must name method %q", msg, method)
			}
		})
	}
}

func TestModern_V8_KnownMethodsServed(t *testing.T) {
	srv := modernServer(t)
	for _, method := range []string{"server/discover", "tools/list"} {
		t.Run(method, func(t *testing.T) {
			status, m := postJSON(t, srv, "/mcp", modernHeaders(method, ""),
				modernBody(t, method, nil))
			if status != http.StatusOK {
				t.Fatalf("status=%d want=200: %v", status, m)
			}
			if _, ok := m["result"].(map[string]any); !ok {
				t.Fatalf("expected result: %v", m)
			}
		})
	}
	// tools/call served: covered end-to-end in
	// surface_mcp_modern_call_test.go.
}

// --- server/discover ---------------------------------------------------

func TestModern_Discover_HappyPath(t *testing.T) {
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("server/discover", ""),
		modernBody(t, "server/discover", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", m)
	}
	if res["resultType"] != "complete" {
		t.Errorf("resultType=%v want=complete", res["resultType"])
	}
	sv, _ := res["supportedVersions"].([]any)
	if len(sv) != 1 || sv[0] != mcpModernProtocolVersion {
		t.Errorf("supportedVersions=%v want=[%s]", sv, mcpModernProtocolVersion)
	}
	caps, _ := res["capabilities"].(map[string]any)
	if caps == nil {
		t.Fatal("capabilities missing")
	}
	if _, ok := caps["tools"].(map[string]any); !ok {
		t.Errorf("capabilities.tools=%v want={}", caps["tools"])
	}
	if _, ok := caps["extensions"]; ok {
		t.Errorf("capabilities.extensions must be omitted (none supported): %v", caps)
	}
	if _, ok := res["instructions"]; ok {
		t.Errorf("instructions must be omitted: %v", res)
	}
	// Cache-hint defaults: ttlMs 0, cacheScope private.
	if ttl, _ := res["ttlMs"].(float64); ttl != 0 {
		t.Errorf("ttlMs=%v want=0", res["ttlMs"])
	}
	if res["cacheScope"] != "private" {
		t.Errorf("cacheScope=%v want=private", res["cacheScope"])
	}
	// Result _meta serverInfo carries the mount defaults.
	meta, _ := res["_meta"].(map[string]any)
	si, _ := meta[metaKeyServerInfo].(map[string]any)
	if si == nil {
		t.Fatalf("_meta serverInfo missing: %v", res)
	}
	if si["name"] != defaultMCPServerName || si["version"] != defaultMCPServerVersion {
		t.Errorf("serverInfo=%v want name=%s version=%s", si, defaultMCPServerName, defaultMCPServerVersion)
	}
}

func TestModern_Discover_ServerInfoOverride(t *testing.T) {
	srv := modernServer(t, WithMCPServerInfo("acme", "9.9.9"))
	_, m := postJSON(t, srv, "/mcp", modernHeaders("server/discover", ""),
		modernBody(t, "server/discover", nil))
	res, _ := m["result"].(map[string]any)
	meta, _ := res["_meta"].(map[string]any)
	si, _ := meta[metaKeyServerInfo].(map[string]any)
	if si["name"] != "acme" || si["version"] != "9.9.9" {
		t.Errorf("serverInfo=%v want name=acme version=9.9.9", si)
	}
}

func TestModern_Discover_CacheHintsFromOption(t *testing.T) {
	srv := modernServer(t, WithMCPCacheHints(5*time.Second, MCPCacheScopePublic))
	_, m := postJSON(t, srv, "/mcp", modernHeaders("server/discover", ""),
		modernBody(t, "server/discover", nil))
	res, _ := m["result"].(map[string]any)
	if ttl, _ := res["ttlMs"].(float64); ttl != 5000 {
		t.Errorf("ttlMs=%v want=5000", res["ttlMs"])
	}
	if res["cacheScope"] != "public" {
		t.Errorf("cacheScope=%v want=public", res["cacheScope"])
	}
}

// --- Origin allowlist ---------------------------------------------------

func TestModern_Origin_DisallowedOriginRejected403(t *testing.T) {
	srv := modernServer(t, WithMCPOriginAllowlist("https://app.example.com"))
	headers := modernHeaders("server/discover", "")
	headers["Origin"] = "https://evil.example.net"
	status, _ := postJSON(t, srv, "/mcp", headers, modernBody(t, "server/discover", nil))
	if status != http.StatusForbidden {
		t.Fatalf("status=%d want=403", status)
	}
}

func TestModern_Origin_AllowedOriginServed(t *testing.T) {
	srv := modernServer(t, WithMCPOriginAllowlist("https://app.example.com"))
	headers := modernHeaders("server/discover", "")
	headers["Origin"] = "https://app.example.com"
	status, m := postJSON(t, srv, "/mcp", headers, modernBody(t, "server/discover", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
}

func TestModern_Origin_NoOriginHeaderNeverRefused(t *testing.T) {
	srv := modernServer(t, WithMCPOriginAllowlist("https://app.example.com"))
	status, m := postJSON(t, srv, "/mcp", modernHeaders("server/discover", ""),
		modernBody(t, "server/discover", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
}

func TestModern_Origin_NoAllowlistNoCheck(t *testing.T) {
	srv := modernServer(t)
	headers := modernHeaders("server/discover", "")
	headers["Origin"] = "https://anything.example.net"
	status, m := postJSON(t, srv, "/mcp", headers, modernBody(t, "server/discover", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
}

func TestModern_Origin_LegacyPathUnaffected(t *testing.T) {
	// The allowlist gates the modern path only; a legacy request from
	// a disallowed Origin is served exactly as today.
	srv := modernServer(t, WithMCPOriginAllowlist("https://app.example.com"))
	status, m := postJSON(t, srv, "/mcp", map[string]string{
		"Origin": "https://evil.example.net",
	}, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200 (legacy route): %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	if res["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion=%v want=2024-11-05", res["protocolVersion"])
	}
}

// --- initialize rejection text -----------------------------------------

func TestModern_InitializeRejection_MessageNamesSupportedVersions(t *testing.T) {
	// Modern-only mount: initialize takes the normal validation chain.
	srv := modernServer(t, WithMCPSpecVersions(MCPSpec20260728))

	// Bare legacy initialize fails V3; the message names the
	// supported versions.
	status, m := postJSON(t, srv, "/mcp", nil, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	if code := errCode(m); code != mcpErrInvalidParams {
		t.Errorf("code=%d want=%d (-32602)", code, mcpErrInvalidParams)
	}
	if msg := errMessage(m); !strings.Contains(msg, mcpModernProtocolVersion) {
		t.Errorf("message=%q must name %q", msg, mcpModernProtocolVersion)
	}

	// initialize with a complete modern envelope survives to V8
	// (method not served) — still named as rejected-initialize.
	status, m = postJSON(t, srv, "/mcp", modernHeaders("initialize", ""),
		modernBody(t, "initialize", nil))
	if status != http.StatusNotFound {
		t.Fatalf("status=%d want=404", status)
	}
	if code := errCode(m); code != mcpErrMethodNotFound {
		t.Errorf("code=%d want=%d (-32601)", code, mcpErrMethodNotFound)
	}
	if msg := errMessage(m); !strings.Contains(msg, mcpModernProtocolVersion) {
		t.Errorf("message=%q must name %q", msg, mcpModernProtocolVersion)
	}
}

func TestModern_NonInitializeRejection_NoVersionSuffix(t *testing.T) {
	// The supported-versions hint is appended only when rejecting an
	// initialize request; other failures keep their plain messages.
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", map[string]string{
		headerMCPMethod:          "tools/list",
		headerMCPProtocolVersion: mcpModernProtocolVersion,
	}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	if msg := errMessage(m); strings.Contains(msg, "supported protocol versions") {
		t.Errorf("message=%q must not carry the initialize-only version hint", msg)
	}
}
