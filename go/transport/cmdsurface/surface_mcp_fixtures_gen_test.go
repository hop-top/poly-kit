package cmdsurface

// Cross-language MCP wire-fixture generator.
//
// Emits sdk/tests/cross-lang/fixtures/mcp-wire.json by driving the
// real surface over httptest and capturing the exact response bytes.
// Generating from the live surface — rather than transcribing the
// lock suites by hand — is what makes the fixtures byte-identical to
// Go by construction: a transcription error would silently redefine
// the parity contract, and every port would then conform to the
// error.
//
// The lock suites (surface_mcp_legacy_lock_test.go,
// surface_mcp_modern_lock_test.go) remain the Go-side guarantee and
// are frozen; this file is additive and never edits them. Both must
// agree — RunFixtureSelfCheck below re-drives every emitted case and
// fails if the surface has drifted from the committed fixture.
//
// Regenerate with:
//
//	go test ./go/transport/cmdsurface/ -run TestGenerateMCPWireFixtures -update-mcp-fixtures
//
// Without the flag this test only self-checks the committed file, so
// CI fails on drift instead of silently rewriting the contract.

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// rawPOSTURL is rawPOST against an explicit base URL. rawPOST takes a
// *httptest.Server; the generator holds several servers at once and
// captures against whichever the case needs.
func rawPOSTURL(t *testing.T, baseURL, path string, headers map[string]string, body []byte) (int, string, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+path, rdr)
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
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), raw
}

var updateMCPFixtures = flag.Bool("update-mcp-fixtures", false,
	"rewrite sdk/tests/cross-lang/fixtures/mcp-wire.json from the live surface")

// mcpFixtureCase is one request/response pair. Field order here is the
// emitted JSON key order (encoding/json follows struct order).
type mcpFixtureCase struct {
	Name    string            `json:"name"`
	Era     string            `json:"era"`
	Why     string            `json:"why,omitempty"`
	Mount   []string          `json:"mount,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Request is a string, not json.RawMessage: one case posts
	// deliberately malformed JSON to pin the -32700 response, and
	// runners must post these bytes verbatim rather than re-encode.
	Request  string `json:"request"`
	Status   int    `json:"status"`
	Response string `json:"response"`
}

// mcpFixtureDoc is the emitted file.
type mcpFixtureDoc struct {
	Comment []string         `json:"_comment"`
	Tree    string           `json:"command_tree"`
	Cases   []mcpFixtureCase `json:"cases"`
}

// fixtureComment documents the contract for every runner author.
func fixtureComment() []string {
	return []string{
		"Cross-language MCP wire conformance cases. Generated from the Go",
		"surface — do not hand-edit. Regenerate with:",
		"  go test ./go/transport/cmdsurface/ -run TestGenerateMCPWireFixtures \\",
		"      -update-mcp-fixtures",
		"",
		"Each case posts `request` (verbatim bytes) to the mount with",
		"`headers` applied, and asserts the response is byte-identical to",
		"`response` and the HTTP status equals `status`. Byte-identical",
		"means exactly that: no JSON decode/re-encode before comparing.",
		"Go emits objects with lexicographically sorted keys and a trailing",
		"newline; a runtime whose serializer differs must reorder to match,",
		"not normalize the comparison.",
		"",
		"`mount` lists MountMCP options for the case, as `name=value`",
		"tokens. Absent means default mount: both eras, path /mcp, empty",
		"origin allowlist, cache hints ttlMs=0 / cacheScope=private.",
		"",
		"`era` is which handler must serve the request (legacy = 2024-11-05,",
		"modern = 2026-07-28). It is documentation for the runner author,",
		"not an input: era is detected per-request from the markers in",
		"ADR 0042, and a port that routes a case to the wrong handler will",
		"fail on the response bytes anyway.",
		"",
		"See ADR 0043 for the polyglot surface design and ADR 0042 for the",
		"normative era-detection rules.",
	}
}

// mcpFixtureCases drives the surface and captures real wire bytes.
// Every case here mirrors one locked behavior from the lock suites.
func mcpFixtureCases(t *testing.T) []mcpFixtureCase {
	t.Helper()
	var out []mcpFixtureCase

	// --- legacy era (2024-11-05) ---
	legacy := func() string { return legacyLockServer(t, nil).URL }

	// A FRESH server per case. Cobra attaches help flags lazily on first
	// execution, so a long-lived server lets an earlier tools/call leak a
	// "help" property into a later tools/list — two identical requests
	// then produce different bytes purely by position. That is a
	// generator artifact, not surface behavior, and encoding it would
	// force every port to reproduce a cobra quirk.
	capture := func(name, era, why string, mount []string,
		headers map[string]string, body string, newSrv func() string,
	) {
		status, _, raw := rawPOSTURL(t, newSrv(), "/mcp", headers, []byte(body))
		out = append(out, mcpFixtureCase{
			Name:     name,
			Era:      era,
			Why:      why,
			Mount:    mount,
			Headers:  headers,
			Request:  body,
			Status:   status,
			Response: string(raw),
		})
	}

	capture("legacy/initialize/defaults", "legacy",
		"handshake response pins protocolVersion + default serverInfo",
		nil, nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`, legacy)

	capture("legacy/initialize/null-id", "legacy",
		"id round-trips verbatim including null",
		nil, nil,
		`{"jsonrpc":"2.0","id":null,"method":"initialize"}`, legacy)

	capture("legacy/tools-list", "legacy",
		"tool enumeration, hidden and deprecated flags excluded",
		nil, nil,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, legacy)

	capture("legacy/tools-call/read", "legacy",
		"non-destructive leaf invokes and returns stdout content block",
		nil, nil,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ping"}}`,
		legacy)

	capture("legacy/tools-call/destructive-blocked", "legacy",
		"destructive leaf blocked by default policy: isError result at HTTP 200",
		nil, nil,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"widget.delete"}}`,
		legacy)

	capture("legacy/tools-call/auth-required", "legacy",
		"auth-required leaf without Authorization header",
		nil, nil,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"secret"}}`,
		legacy)

	capture("legacy/error/method-not-found", "legacy",
		"-32601 for an unknown JSON-RPC method",
		nil, nil,
		`{"jsonrpc":"2.0","id":6,"method":"nope"}`, legacy)

	capture("legacy/error/parse", "legacy",
		"-32700 at HTTP 400 for unparseable JSON, regardless of headers",
		nil, nil,
		`{not json`, legacy)

	capture("legacy/meta-progress-token-is-not-modern", "legacy",
		"bare params._meta is NOT a modern marker: progressToken stays legacy",
		nil, nil,
		`{"jsonrpc":"2.0","id":7,"method":"initialize","params":{"_meta":{"progressToken":"p1"}}}`,
		legacy)

	// The progressToken case above uses `initialize`, which D2
	// short-circuits BEFORE M3 is consulted — so it does not actually
	// exercise the non-marker. This case uses tools/list, where M3 is
	// the only rule that could route it modern: a port that treats bare
	// _meta as a marker fails here and nowhere else.
	capture("legacy/meta-progress-token-on-tools-list", "legacy",
		"bare params._meta on a NON-initialize method: M3 must not fire",
		nil, nil,
		`{"jsonrpc":"2.0","id":9,"method":"tools/list","params":{"_meta":{"progressToken":"p2"}}}`,
		legacy)

	capture("legacy/protocol-version-header-is-not-modern", "legacy",
		"MCP-Protocol-Version header alone must NOT route modern (predates 2026-07-28)",
		nil, map[string]string{"MCP-Protocol-Version": "2025-06-18"},
		`{"jsonrpc":"2.0","id":8,"method":"tools/list"}`, legacy)

	// --- modern era (2026-07-28) ---
	modern := func() string { return modernLockServer(t, nil).URL }

	modernMeta := `"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}`

	capture("modern/server-discover", "modern",
		"server/discover is a modern marker on its own (M4)",
		nil, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "server/discover",
		},
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{`+modernMeta+`}}`,
		modern)

	capture("modern/tools-list", "modern",
		"cacheable list result carries ttlMs + cacheScope",
		nil, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "tools/list",
		},
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{`+modernMeta+`}}`,
		modern)

	capture("modern/tools-call/read", "modern",
		"Mcp-Name header required on tools/call and must agree with the body",
		nil, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "tools/call",
			"Mcp-Name":             "ping",
		},
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ping",`+modernMeta+`}}`,
		modern)

	capture("modern/error/header-mismatch", "modern",
		"-32020 HeaderMismatch when Mcp-Name disagrees with params.name",
		nil, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "tools/call",
			"Mcp-Name":             "widget_add",
		},
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"ping",`+modernMeta+`}}`,
		modern)

	capture("modern/error/unsupported-version", "modern",
		"-32022 UnsupportedProtocolVersion for an unknown _meta protocolVersion",
		nil, map[string]string{
			"MCP-Protocol-Version": "2099-01-01",
			"Mcp-Method":           "tools/list",
		},
		`{"jsonrpc":"2.0","id":5,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2099-01-01"}}}`,
		modern)

	capture("modern/tools-call/destructive-blocked", "modern",
		"ErrDestructiveBlocked renders as isError at HTTP 200, same as legacy",
		nil, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "tools/call",
			"Mcp-Name":             "widget.delete",
		},
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"widget.delete",`+modernMeta+`}}`,
		modern)

	capture("modern/initialize-is-legacy", "modern",
		"D2: initialize routes legacy even when modern markers are present",
		nil, map[string]string{
			"MCP-Protocol-Version": "2026-07-28",
			"Mcp-Method":           "initialize",
		},
		`{"jsonrpc":"2.0","id":7,"method":"initialize","params":{`+modernMeta+`}}`,
		modern)

	return out
}

func fixturePath(t *testing.T) string {
	t.Helper()
	// go/transport/cmdsurface -> repo root
	return filepath.Join("..", "..", "..", "sdk", "tests", "cross-lang",
		"fixtures", "mcp-wire.json")
}

func TestGenerateMCPWireFixtures(t *testing.T) {
	cases := mcpFixtureCases(t)
	if len(cases) == 0 {
		t.Fatal("no fixture cases generated")
	}
	doc := mcpFixtureDoc{
		Comment: fixtureComment(),
		Tree:    "legacyLockTree (see surface_mcp_legacy_lock_test.go)",
		Cases:   cases,
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	encoded = append(encoded, '\n')

	path := fixturePath(t)
	if *updateMCPFixtures {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		t.Logf("wrote %d cases to %s", len(cases), path)
		return
	}

	committed, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture not generated yet (%v); run with -update-mcp-fixtures", err)
	}
	if string(committed) != string(encoded) {
		t.Errorf("committed fixture is stale: the Go surface no longer "+
			"produces these bytes.\nRegenerate with -update-mcp-fixtures, "+
			"and treat the diff as a parity-contract change.\nfile: %s", path)
	}
}
