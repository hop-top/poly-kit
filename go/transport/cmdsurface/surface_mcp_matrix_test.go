package cmdsurface

// Dual-version matrix: one mount, both spec versions enabled,
// interleaved legacy (2024-11-05) and modern (2026-07-28) exchanges
// against the SAME httptest.Server / SAME Bridge instance, proving era
// isolation with no cross-talk — the central claim of ADR 0004 ("one
// mount, per-request version detection... both spec versions are
// served from one mount").
//
// Fixture: this file mounts on legacyLockTree() (frozen,
// surface_mcp_legacy_lock_test.go) via legacyLockServer(), reused
// read-only exactly as every legacy-lock test in that file reads it —
// no edit to the frozen file. Every "legacy" step's wantBody below is
// therefore not merely SHAPED like the legacy lock's own goldens, it
// is byte-for-byte copied from them (TestLegacyLock_Initialize_Defaults,
// TestLegacyLock_ToolsList_ExactDescriptors,
// TestLegacyLock_ToolsCall_HappyPath_RealExec, the -32601@200 quirk
// test, and the addendum's mid-era-header non-marker test): same
// tree, same requests, same expected bytes, so the claim "legacy bytes
// identical to the legacy lock's goldens" is enforced by running both
// suites against the identical fixture, not merely asserted in prose.
//
// "Modern" steps run against this SAME legacyLockTree mount (not a
// separately declared tree), so the matrix genuinely proves the two
// eras coexist on one Bridge/mount rather than proving two different
// mounts each work in isolation. Modern wire bytes were derived by
// running the modern handler against legacyLockTree() directly (its
// widget.add carries force+tag beyond modernLockTree's narrower
// shape) and are asserted byte-exact here.

import (
	"testing"
)

// TestMatrix_LegacyAndModernInterleaved_NoCrossTalk drives a sequence
// of legacy and modern exchanges against one mount (legacyLockTree),
// alternating eras request-by-request, and asserts every response
// byte-exact — legacy steps against the legacy lock's own goldens
// (identity, not re-derivation), modern steps against golden bytes
// derived from this same tree. Passing requires: (a) the era
// dispatcher classifies every request correctly regardless of what
// preceded it (no sticky/leaked state between requests, matching the
// "stateless across requests" contracts documented on mcpHandler and
// mcpModernHandler), and (b) neither handler's output is perturbed by
// the other having just served a request on the same server.
func TestMatrix_LegacyAndModernInterleaved_NoCrossTalk(t *testing.T) {
	srv := legacyLockServer(t, nil) // both versions enabled by default
	client := hermeticHTTPClient()

	type step struct {
		name string
		gx   goldenExchange
	}

	steps := []step{
		// 1. legacy initialize — identical request+response to
		//    TestLegacyLock_Initialize_Defaults (same tree, same
		//    mount pattern: legacyLockServer(t, nil), same body).
		{"1-legacy-initialize", goldenExchange{
			name:       "legacy initialize",
			body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`),
			wantStatus: 200,
			wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"tools":{}},"protocolVersion":"2024-11-05","serverInfo":{"name":"cmdsurface","version":"0.0.0"}}}` + "\n"),
		}},
		// 2. modern server/discover, immediately after a legacy
		//    request on the same server — proves no era leakage.
		{"2-modern-discover", goldenExchange{
			name:       "modern server/discover",
			headers:    stdModernHeaders("server/discover", ""),
			body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
			wantStatus: 200,
			wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"cacheScope":"private","capabilities":{"tools":{}},"resultType":"complete","supportedVersions":["2026-07-28"],"ttlMs":0}}` + "\n"),
		}},
		// 3. legacy tools/list — identical request+response to
		//    TestLegacyLock_ToolsList_ExactDescriptors (verified by
		//    running both against legacyLockTree(): same bytes,
		//    including widget.add's force+tag flags and the
		//    hidden/deprecated-flag exclusions).
		{"3-legacy-tools-list", goldenExchange{
			name:       "legacy tools/list",
			body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`),
			wantStatus: 200,
			wantBody: []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[` +
				`{"description":"Deploy","inputSchema":{"properties":{},"type":"object"},"name":"deploy"},` +
				`{"description":"Ping the server","inputSchema":{"properties":{},"type":"object"},"name":"ping"},` +
				`{"description":"Locked","inputSchema":{"properties":{},"type":"object"},"name":"secret"},` +
				`{"description":"Add a widget","inputSchema":{"properties":{"count":{"description":"widget count","type":"integer"},"force":{"description":"force flag","type":"boolean"},"name":{"description":"widget name","type":"string"},"tag":{"description":"tag list","items":{"type":"string"},"type":"array"}},"required":["name"],"type":"object"},"name":"widget.add"},` +
				`{"description":"Delete a widget","inputSchema":{"properties":{},"type":"object"},"name":"widget.delete"}` +
				`]}}` + "\n"),
		}},
		// 4. modern tools/list, same server, right after legacy
		//    tools/list — cache hints + resultType/_meta present on
		//    modern only; the tools array carries the SAME force+tag
		//    widget.add schema as step 3 (both build via
		//    buildToolEnvelope over the identical tree), proving the
		//    two are genuinely separate response paths over one
		//    shared leaf set rather than one cached/shared response.
		{"4-modern-tools-list", goldenExchange{
			name:       "modern tools/list",
			headers:    stdModernHeaders("tools/list", ""),
			body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
			wantStatus: 200,
			wantBody: []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"cacheScope":"private","resultType":"complete","tools":[` +
				`{"description":"Deploy","inputSchema":{"properties":{},"type":"object"},"name":"deploy"},` +
				`{"description":"Ping the server","inputSchema":{"properties":{},"type":"object"},"name":"ping"},` +
				`{"description":"Locked","inputSchema":{"properties":{},"type":"object"},"name":"secret"},` +
				`{"description":"Add a widget","inputSchema":{"properties":{"count":{"description":"widget count","type":"integer"},"force":{"description":"force flag","type":"boolean"},"name":{"description":"widget name","type":"string"},"tag":{"description":"tag list","items":{"type":"string"},"type":"array"}},"required":["name"],"type":"object"},"name":"widget.add"},` +
				`{"description":"Delete a widget","inputSchema":{"properties":{},"type":"object"},"name":"widget.delete"}` +
				`],"ttlMs":0}}` + "\n"),
		}},
		// 5. legacy tools/call happy path — real exec, identical
		//    request+response to
		//    TestLegacyLock_ToolsCall_HappyPath_RealExec.
		{"5-legacy-tools-call", goldenExchange{
			name:       "legacy tools/call ping",
			body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`),
			wantStatus: 200,
			wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"pong\n","type":"text"}],"isError":false}}` + "\n"),
		}},
		// 6. modern tools/call happy path, same leaf, same server,
		//    right after the legacy call on the identical leaf —
		//    proves real Bridge.Invoke execution on both eras without
		//    any shared per-request state bleeding through (each call
		//    re-executes the leaf; RunE has no memory of the prior
		//    invocation).
		{"6-modern-tools-call", goldenExchange{
			name:       "modern tools/call ping",
			headers:    stdModernHeaders("tools/call", "ping"),
			body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
			wantStatus: 200,
			wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"pong\n","type":"text"}],"isError":false,"resultType":"complete"}}` + "\n"),
		}},
		// 6b. modern tools/call on widget.add WITH force+tag
		//     arguments — exercises the exact flags step 3/4 proved
		//     absent from modernLockTree, so the matrix's "one shared
		//     leaf set, two eras" claim is proven for a leaf whose
		//     schema differs between the two lock files' own trees,
		//     not just for the flagless ping leaf.
		{"6b-modern-tools-call-widget-add", goldenExchange{
			name:       "modern tools/call widget.add with force+tag",
			headers:    stdModernHeaders("tools/call", "widget.add"),
			body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"widget.add","arguments":{"name":"gizmo","count":3,"force":true,"tag":["a","b"]},"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
			wantStatus: 200,
			wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"added\n","type":"text"}],"isError":false,"resultType":"complete"}}` + "\n"),
		}},
		// 7. legacy unknown method — the -32601@200 quirk, unaffected
		//    by any modern traffic on the same mount. Identical to
		//    TestLegacyLock_ErrorCode_MethodNotFound32601.
		{"7-legacy-unknown-method", goldenExchange{
			name:       "legacy unknown method quirk",
			body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"nope/anywhere"}`),
			wantStatus: 200,
			wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found: nope/anywhere"}}` + "\n"),
		}},
		// 8. modern unknown method — -32601@404, the deliberately
		//    DIFFERENT status from step 7, on the same mount,
		//    immediately after. If era detection or status mapping
		//    ever leaked between handlers, this is where it would
		//    show: same error code, asymmetric status.
		{"8-modern-unknown-method", goldenExchange{
			name:       "modern unknown method 404",
			headers:    stdModernHeaders("nope/anywhere", ""),
			body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"nope/anywhere","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
			wantStatus: 404,
			wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found: nope/anywhere"}}` + "\n"),
		}},
		// 9. legacy tools/call with a mid-era MCP-Protocol-Version
		//    header (the non-marker case ADR 0004 and the legacy
		//    lock's addendum both pin) — served on a mount where the
		//    modern handler IS active for other requests, proving the
		//    non-marker rule holds under real dual-serving, not just
		//    in isolation. Identical to
		//    TestLegacyLock_NonMarker_MidEraProtocolVersionHeader's
		//    tools/call case.
		{"9-legacy-nonmarker-header", goldenExchange{
			name:       "legacy tools/call with mid-era protocol-version header (non-marker)",
			headers:    map[string]string{"MCP-Protocol-Version": "2024-11-05"},
			body:       []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping"}}`),
			wantStatus: 200,
			wantBody:   []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"text":"pong\n","type":"text"}],"isError":false}}` + "\n"),
		}},
		// 10. modern tools/call again, closing the interleave back on
		//     the modern path, confirming the dispatcher is still
		//     correctly routing M1/M2 markers after nine prior
		//     requests spanning both eras.
		{"10-modern-tools-call-again", goldenExchange{
			name:       "modern tools/call ping (closing)",
			headers:    stdModernHeaders("tools/call", "ping"),
			body:       []byte(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"ping","_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
			wantStatus: 200,
			wantBody:   []byte(`{"jsonrpc":"2.0","id":9,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"content":[{"text":"pong\n","type":"text"}],"isError":false,"resultType":"complete"}}` + "\n"),
		}},
	}

	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			runGoldenExchange(t, srv, client, s.gx)
		})
	}
}

// TestMatrix_BothEnabledIsDefault_NoOptionNeeded proves the matrix
// property holds with the exact zero-option MountMCP call every
// pre-dual-spec caller already makes (ADR 0004: "Default configuration
// enables both versions; existing MountMCP calls compile and behave
// unchanged") — this is the same mount surface_mcp_dispatch_test.go's
// TestMountMCP_ExistingCallsCompileAndBehaveUnchanged exercises for a
// single legacy request; here both a legacy and a modern request are
// driven against it to confirm the default already IS the dual-serving
// configuration, not an opt-in.
func TestMatrix_BothEnabledIsDefault_NoOptionNeeded(t *testing.T) {
	srv := legacyLockServer(t, nil)
	client := hermeticHTTPClient()

	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "default mount serves legacy",
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`),
		wantStatus: 200,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"tools":{}},"protocolVersion":"2024-11-05","serverInfo":{"name":"cmdsurface","version":"0.0.0"}}}` + "\n"),
	})
	runGoldenExchange(t, srv, client, goldenExchange{
		name:       "default mount serves modern",
		headers:    stdModernHeaders("server/discover", ""),
		body:       []byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`),
		wantStatus: 200,
		wantBody:   []byte(`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"cmdsurface","version":"0.0.0"}},"cacheScope":"private","capabilities":{"tools":{}},"resultType":"complete","supportedVersions":["2026-07-28"],"ttlMs":0}}` + "\n"),
	})
}
