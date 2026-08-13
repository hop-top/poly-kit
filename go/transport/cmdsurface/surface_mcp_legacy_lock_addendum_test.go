package cmdsurface

// Legacy MCP surface conformance lock — addendum (protocol
// 2024-11-05), fixing three coverage gaps flagged in review of the
// original lock (surface_mcp_legacy_lock_test.go):
//
//  1. Era-misroute vectors: ADR 0004 ("docs/adr/0004-mcp-dual-spec-
//     surface.md") names deliberate NON-markers a future era
//     dispatcher must NOT treat as signals to route a request to the
//     modern handler — a bare params._meta without the reserved
//     "io.modelcontextprotocol/protocolVersion" key, and mid-era
//     legacy-negotiated SDK clients sending MCP-Protocol-Version /
//     Mcp-Session-Id headers on every post-handshake request. Every
//     such shape must produce today's exact legacy bytes; this file
//     locks that so a future detectMCPEra bug that reclassifies any
//     of them fails loud here instead of bricking real clients
//     silently.
//  2. -32601 breadth: the original lock only pins the unknown-method
//     quirk for a method name ("nope/anywhere") no refactor will ever
//     grow real handling for. This file pins the same HTTP-200 quirk
//     for real MCP method names a dual-spec implementation is most
//     tempted to start answering on the legacy path.
//  3. Inherited/persistent flags: collectFlags (surface_mcp.go)
//     walks cmd.InheritedFlags(), which merges ancestor
//     PersistentFlags(). The original fixture tree has no persistent
//     flag anywhere, so that branch was exercised only by its
//     "local overrides inherited" dedup path, never by an actual
//     inherited flag reaching the schema. This file adds an isolated
//     fixture tree with a root-level persistent flag and locks its
//     appearance in a leaf's tools/list schema byte-exact.
//
// This file is additive-only, matching the original lock's file-
// header freeze policy: it does not edit surface_mcp_legacy_lock_test.go
// or its legacyLockTree/legacyLockServer/rawPOST/assertByteExact
// helpers (all reused as-is), and it is itself frozen the same way —
// keep it green; do not edit it for the dual-spec work. Add new
// coverage for the 2026-07-28 surface in new files instead.
//
// Byte-exactness rationale is identical to the parent file: response
// bodies are built from map[string]any (encoding/json.Marshal sorts
// keys), no timestamps/IDs are echoed, so every assertion below is a
// raw-byte comparison against a literal Go string constant.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
)

// --- (1) era-misroute non-marker vectors ------------------------------
//
// Reuses legacyLockTree/legacyLockServer/rawPOST/assertByteExact from
// surface_mcp_legacy_lock_test.go (same package, same file freeze
// tier).

// TestLegacyLock_NonMarker_BareMetaProgressToken locks today's bytes
// for a tools/call whose params._meta carries a legitimate 2024-11-05
// field (progressToken) and NOT the reserved modern key
// "io.modelcontextprotocol/protocolVersion". ADR 0004 explicitly
// calls this out as a deliberate non-marker (M3 requires the
// *reserved* key; mere params._meta presence must not route modern).
// Today's legacy handler doesn't read params._meta at all — callParams
// only has Name/Arguments — so it is silently ignored and the call
// proceeds exactly as if _meta were absent.
func TestLegacyLock_NonMarker_BareMetaProgressToken(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"progressToken":"abc123"}}}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"pong\n","type":"text"}],"isError":false}}` + "\n"
	assertByteExact(t, "bare params._meta.progressToken (non-marker)", raw, []byte(want))
}

// TestLegacyLock_NonMarker_MidEraProtocolVersionHeader locks today's
// bytes for tools/list and tools/call sent with the
// MCP-Protocol-Version header a legacy-negotiated SDK client sends on
// every request after a successful 2024-11-05 initialize handshake.
// ADR 0004 is explicit that header *presence* must never be treated
// as a modern-routing signal (would "brick their sessions"). Today's
// legacy handler never inspects this header at all, so it is
// silently tolerated.
func TestLegacyLock_NonMarker_MidEraProtocolVersionHeader(t *testing.T) {
	srv := legacyLockServer(t, nil)
	hdrs := map[string]string{"MCP-Protocol-Version": "2024-11-05"}

	status, _, raw := rawPOST(t, srv, "/mcp", hdrs,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if status != http.StatusOK {
		t.Fatalf("tools/list status=%d want=200", status)
	}
	wantList := `{"jsonrpc":"2.0","id":1,"result":{"tools":[` +
		`{"description":"Deploy","inputSchema":{"properties":{},"type":"object"},"name":"deploy"},` +
		`{"description":"Ping the server","inputSchema":{"properties":{},"type":"object"},"name":"ping"},` +
		`{"description":"Locked","inputSchema":{"properties":{},"type":"object"},"name":"secret"},` +
		`{"description":"Add a widget","inputSchema":{"properties":{"count":{"description":"widget count","type":"integer"},"force":{"description":"force flag","type":"boolean"},"name":{"description":"widget name","type":"string"},"tag":{"description":"tag list","items":{"type":"string"},"type":"array"}},"required":["name"],"type":"object"},"name":"widget.add"},` +
		`{"description":"Delete a widget","inputSchema":{"properties":{},"type":"object"},"name":"widget.delete"}` +
		`]}}` + "\n"
	assertByteExact(t, "tools/list with mid-era MCP-Protocol-Version header (non-marker)", raw, []byte(wantList))

	status, _, raw = rawPOST(t, srv, "/mcp", hdrs,
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping"}}`))
	if status != http.StatusOK {
		t.Fatalf("tools/call status=%d want=200", status)
	}
	wantCall := `{"jsonrpc":"2.0","id":2,"result":{"content":[{"text":"pong\n","type":"text"}],"isError":false}}` + "\n"
	assertByteExact(t, "tools/call with mid-era MCP-Protocol-Version header (non-marker)", raw, []byte(wantCall))
}

// TestLegacyLock_NonMarker_MidEraSessionAndLastEventIDHeaders locks
// today's bytes for a request additionally carrying Mcp-Session-Id
// and Last-Event-ID — both named in ADR 0004 as headers a dual-era
// server must ignore (never minted, never echoed) rather than treat
// as routing signals. Legacy code today reads neither.
func TestLegacyLock_NonMarker_MidEraSessionAndLastEventIDHeaders(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", map[string]string{
		"MCP-Protocol-Version": "2024-11-05",
		"Mcp-Session-Id":       "sess-abc",
		"Last-Event-ID":        "42",
	}, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"pong\n","type":"text"}],"isError":false}}` + "\n"
	assertByteExact(t, "tools/call with Mcp-Session-Id + Last-Event-ID (ignored headers)", raw, []byte(want))
}

// TestLegacyLock_NonMarker_InitializeFullLegacyParams locks today's
// bytes for an initialize carrying the full params shape every real
// 2024-11-05 client actually sends (protocolVersion, capabilities,
// clientInfo) — the original lock only exercised bare initialize
// (no params at all). handleInitialize never reads rpc.Params, so
// the response is byte-identical to the no-params case; that
// invariant is exactly what's worth pinning; a future dispatcher
// that starts inspecting these params for era routing must not
// change this response.
func TestLegacyLock_NonMarker_InitializeFullLegacyParams(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"roots":{"listChanged":true},"sampling":{}},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"tools":{}},"protocolVersion":"2024-11-05","serverInfo":{"name":"cmdsurface","version":"0.0.0"}}}` + "\n"
	assertByteExact(t, "initialize with full legacy params", raw, []byte(want))
}

// --- (2) -32601 breadth: real MCP method names ------------------------

// TestLegacyLock_MethodNotFound_RealMCPMethodNames locks the current
// -32601@200 quirk (see the parent lock's
// TestLegacyLock_ErrorCode_MethodNotFound32601 and ADR 0004's
// "Acknowledged quirks") for method names a dual-spec implementation
// is most likely to start handling on the legacy path: the
// post-handshake notification every real client sends, plus three
// list-style methods a naive "just add more methods to the shared
// switch" change might introduce without era-gating it. If legacy-
// path handling appears for any of these, wire behavior changes for
// every real 2024-11-05 client and this test catches it.
func TestLegacyLock_MethodNotFound_RealMCPMethodNames(t *testing.T) {
	srv := legacyLockServer(t, nil)
	methods := []string{
		"notifications/initialized",
		"ping",
		"resources/list",
		"prompts/list",
	}
	for i, method := range methods {
		id := strconv.Itoa(i + 1)
		status, _, raw := rawPOST(t, srv, "/mcp", nil,
			[]byte(`{"jsonrpc":"2.0","id":`+id+`,"method":"`+method+`"}`))
		if status != http.StatusOK {
			t.Errorf("method=%q status=%d want=200 (legacy quirk: -32601 rides HTTP 200)", method, status)
			continue
		}
		want := `{"jsonrpc":"2.0","id":` + id + `,"error":{"code":-32601,"message":"method not found: ` + method + `"}}` + "\n"
		assertByteExact(t, "method not found -32601: "+method, raw, []byte(want))
	}
}

// --- (3) inherited/persistent flags ------------------------------------

// persistentFlagTree builds a tree isolated from legacyLockTree with
// a root-level PersistentFlags entry, so collectFlags' InheritedFlags
// walk is exercised by a real ancestor-declared flag reaching a
// leaf's schema, not just the "local overrides inherited" dedup path
// the original fixture covered.
//
//	root (persistent flag: --verbose bool "increase output verbosity")
//	└── status  (read; local flag: --format string)
func persistentFlagTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().Bool("verbose", false, "increase output verbosity")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("ok")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	status.Flags().String("format", "text", "output format")
	root.AddCommand(status)

	return root
}

// TestLegacyLock_ToolsList_InheritedPersistentFlag locks the exact
// tools/list schema for a leaf that inherits a root PersistentFlags
// entry alongside its own local flag: both "verbose" (inherited) and
// "format" (local) must appear in inputSchema.properties, byte-exact,
// proving collectFlags' cmd.InheritedFlags().VisitAll walk
// (surface_mcp.go) is actually exercised end-to-end through the wire
// response — not just present in the source.
func TestLegacyLock_ToolsList_InheritedPersistentFlag(t *testing.T) {
	root := persistentFlagTree()
	b := New(root)
	r := api.NewRouter()
	if err := MountMCP(b, r); err != nil {
		t.Fatalf("MountMCP: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	want := `{"jsonrpc":"2.0","id":1,"result":{"tools":[` +
		`{"description":"Show status","inputSchema":{"properties":{"format":{"description":"output format","type":"string"},"verbose":{"description":"increase output verbosity","type":"boolean"}},"type":"object"},"name":"status"}` +
		`]}}` + "\n"
	assertByteExact(t, "tools/list inherited persistent flag", raw, []byte(want))
}
