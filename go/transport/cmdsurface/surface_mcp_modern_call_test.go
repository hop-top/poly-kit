package cmdsurface

// Coverage for the modern tools/call handler
// (surface_mcp_modern_call.go): result envelope (resultType,
// structuredContent, content blocks, isError), V9 params validation,
// the V7 Mcp-Name slot, pre-flight gates (auth 401, confirmation 428,
// destructive block), safety-gate parity with the policy, and the
// Meta.Extra audit stamps. Fixtures come from modernTestTree
// (surface_mcp_modern_test.go).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// callBody renders a modern tools/call request for the given tool.
func callBody(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	params := map[string]any{"name": name}
	if args != nil {
		params["arguments"] = args
	}
	return modernBody(t, "tools/call", params)
}

func TestModernCall_HappyPath_RealExec(t *testing.T) {
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "ping"),
		callBody(t, "ping", nil))
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
	if res["isError"] != false {
		t.Errorf("isError=%v want=false", res["isError"])
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content blocks: %v", res)
	}
	if text := content[0].(map[string]any)["text"]; text != "pong\n" {
		t.Errorf("content[0].text=%q want=%q", text, "pong\n")
	}
	meta, _ := res["_meta"].(map[string]any)
	if _, ok := meta[metaKeyServerInfo].(map[string]any); !ok {
		t.Errorf("_meta serverInfo missing: %v", res)
	}
	// tools/call is not a cacheable operation: no cache hints.
	if _, ok := res["ttlMs"]; ok {
		t.Errorf("ttlMs must not appear on tools/call results: %v", res)
	}
	if _, ok := res["cacheScope"]; ok {
		t.Errorf("cacheScope must not appear on tools/call results: %v", res)
	}
	// No structured payload was produced.
	if _, ok := res["structuredContent"]; ok {
		t.Errorf("structuredContent must be omitted when Result.Data is nil: %v", res)
	}
}

func TestModernCall_ArgumentMapping_RealExec(t *testing.T) {
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "widget.add"),
		callBody(t, "widget.add", map[string]any{"name": "gizmo", "count": 3}))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if text := content[0].(map[string]any)["text"]; text != "added gizmo x3\n" {
		t.Errorf("content[0].text=%q want=%q", text, "added gizmo x3\n")
	}
}

func TestModernCall_StructuredContent(t *testing.T) {
	data := map[string]any{"id": float64(42), "name": "x"}
	b := New(modernTestTree(), WithRunner(&fakeRunner{
		run: func(context.Context, Invocation) (Result, error) {
			return Result{Stdout: "ok", Data: data}, nil
		},
	}))
	srv := modernServerFor(t, b)
	_, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "ping"),
		callBody(t, "ping", nil))
	res, _ := m["result"].(map[string]any)

	sc, ok := res["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent missing: %v", res)
	}
	if sc["id"] != float64(42) || sc["name"] != "x" {
		t.Errorf("structuredContent=%v want=%v", sc, data)
	}
	// The JSON text block (serialized fallback) is also present.
	content, _ := res["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content len=%d want=2: %v", len(content), content)
	}
	text, _ := content[1].(map[string]any)["text"].(string)
	var roundTrip map[string]any
	if err := json.Unmarshal([]byte(text), &roundTrip); err != nil {
		t.Fatalf("data block not valid JSON: %q: %v", text, err)
	}
}

func TestModernCall_StderrBlock(t *testing.T) {
	b := New(modernTestTree(), WithRunner(&fakeRunner{
		run: func(context.Context, Invocation) (Result, error) {
			return Result{Stdout: "partial", Stderr: "warning: low disk"}, nil
		},
	}))
	srv := modernServerFor(t, b)
	_, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "ping"),
		callBody(t, "ping", nil))
	res, _ := m["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content len=%d want=2: %v", len(content), content)
	}
	if text := content[1].(map[string]any)["text"]; text != "[stderr] warning: low disk" {
		t.Errorf("stderr block=%q", text)
	}
}

func TestModernCall_NonZeroExitIsError(t *testing.T) {
	b := New(modernTestTree(), WithRunner(&fakeRunner{
		run: func(context.Context, Invocation) (Result, error) {
			return Result{Stdout: "boom", ExitCode: 2}, nil
		},
	}))
	srv := modernServerFor(t, b)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "ping"),
		callBody(t, "ping", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	res, _ := m["result"].(map[string]any)
	if res["isError"] != true {
		t.Errorf("isError=%v want=true (ExitCode=2)", res["isError"])
	}
	if res["resultType"] != "complete" {
		t.Errorf("resultType=%v want=complete (tool errors are complete results)", res["resultType"])
	}
}

// --- V9: per-method params ---------------------------------------------

func TestModernCall_V9_MissingName_FailsV7First(t *testing.T) {
	// params.name is absent from the body and no Mcp-Name header is
	// sent. V7 requires the header unconditionally on tools/call and
	// runs before V9's params decode, so this is a header-mismatch
	// failure rather than reaching V9's own missing-name check (V7 <
	// V9 in the pinned validation order). By ADR 0004's V7 rule
	// (header present, non-empty after decoding, params.name present,
	// and the two byte-equal — any violation is -32020@400, checked
	// ahead of any params-shape error), V9's "missing tool name"
	// branch is reachable only when V7 has already been satisfied,
	// which requires params.name to already be present and
	// non-empty — so no conforming HTTP request reaches it; it is
	// retained as a defensive internal check, not because no test can
	// exercise the surrounding rule (the empty-name and absent-name
	// cases V7 itself owns are covered below).
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", ""),
		modernBody(t, "tools/call", nil))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModernCall_V7_EmptyRawHeaderValueRejected(t *testing.T) {
	// Mcp-Name sent as a literal empty string (not sentinel-encoded):
	// empty after "decoding" (a no-op for a non-sentinel value) is a
	// header-validation failure per the amended ADR V7 rule, same as
	// an absent header.
	srv := modernServer(t)
	headers := map[string]string{
		headerMCPProtocolVersion: mcpModernProtocolVersion,
		headerMCPMethod:          "tools/call",
		headerMCPName:            "",
	}
	status, m := postJSON(t, srv, "/mcp", headers, callBody(t, "ping", nil))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400: %v", status, m)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModernCall_V7_EmptySentinelDecodeRejected(t *testing.T) {
	// =?base64??= is a validly-formed sentinel wrapping zero payload
	// bytes: it decodes successfully to "". Paired with a body
	// params.name that is ALSO "" (rather than a non-matching name
	// like "ping"), this fixture uniquely isolates the decoded=="" gate:
	// if that gate were removed, "" == "" would pass the byte-equality
	// comparison too and the request would fall through to V9,
	// regressing to 200/-32602 instead of staying 400/-32020 — so this
	// specific pairing is required to prove the empty-decode gate itself
	// is doing the rejecting, not the ordinary mismatch check.
	srv := modernServer(t)
	headers := map[string]string{
		headerMCPProtocolVersion: mcpModernProtocolVersion,
		headerMCPMethod:          "tools/call",
		headerMCPName:            "=?base64??=",
	}
	status, m := postJSON(t, srv, "/mcp", headers, callBody(t, "", nil))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400: %v", status, m)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModernCall_V7_HeaderPresentParamsNameAbsentRejected(t *testing.T) {
	// Mcp-Name is present, non-empty, and well-formed, but params.name
	// is altogether absent from the body: a header claiming a name
	// the body never supplied is a header-validation failure per the
	// amended ADR V7 rule, not a V9 params error.
	srv := modernServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "ping"), body)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400: %v", status, m)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModernCall_V9_UnknownTool(t *testing.T) {
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "no.such.tool"),
		callBody(t, "no.such.tool", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200 (application-level error)", status)
	}
	if code := errCode(m); code != mcpErrInvalidParams {
		t.Errorf("code=%d want=%d (-32602)", code, mcpErrInvalidParams)
	}
	if msg := errMessage(m); !strings.Contains(msg, "no.such.tool") {
		t.Errorf("message=%q must name the tool", msg)
	}
}

func TestModernCall_V9_UnparseableParams(t *testing.T) {
	// params.name of the wrong JSON type (present, but not a string):
	// V7 cannot compare a non-string value against the header, so it
	// defers; a non-empty Mcp-Name header must still be supplied to
	// pass V7's own presence/non-empty checks before deferral is
	// possible, then the tools/call params decode fails on the type
	// mismatch: -32602 at HTTP 200, mirroring legacy.
	srv := modernServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":12,"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "whatever"), body)
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200 (application-level error)", status)
	}
	if code := errCode(m); code != mcpErrInvalidParams {
		t.Errorf("code=%d want=%d (-32602)", code, mcpErrInvalidParams)
	}
}

func TestModernCall_V9_UnparseableParams_MissingHeaderFailsV7First(t *testing.T) {
	// Same malformed body as above, but with no Mcp-Name header: V7's
	// presence check runs before the params decode and rejects first
	// (-32020 @ 400), per ADR 0004 — a missing required header is a
	// header-validation failure regardless of what else is wrong with
	// the body.
	srv := modernServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":12,"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", ""), body)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", status)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModernCall_V9_SurfaceNotEnabled(t *testing.T) {
	b := New(modernTestTree())
	b.Hide("ping", SurfaceMCP)
	srv := modernServerFor(t, b)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "ping"),
		callBody(t, "ping", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	if code := errCode(m); code != mcpErrInvalidParams {
		t.Errorf("code=%d want=%d (unknown tool)", code, mcpErrInvalidParams)
	}
}

// --- V7: Mcp-Name slot --------------------------------------------------

func TestModernCall_V7_MatchingHeaderPasses(t *testing.T) {
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "ping"),
		callBody(t, "ping", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 || content[0].(map[string]any)["text"] != "pong\n" {
		t.Errorf("expected ping output: %v", res)
	}
}

func TestModernCall_V7_MissingHeaderRejected(t *testing.T) {
	srv := modernServer(t)
	headers := map[string]string{
		headerMCPProtocolVersion: mcpModernProtocolVersion,
		headerMCPMethod:          "tools/call",
	}
	status, m := postJSON(t, srv, "/mcp", headers, callBody(t, "ping", nil))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400: %v", status, m)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModernCall_V7_MismatchedHeaderRejected(t *testing.T) {
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "widget.list"),
		callBody(t, "ping", nil))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400: %v", status, m)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModernCall_V7_EmptyHeaderValueRejected(t *testing.T) {
	srv := modernServer(t)
	headers := map[string]string{
		headerMCPProtocolVersion: mcpModernProtocolVersion,
		headerMCPMethod:          "tools/call",
		headerMCPName:            "",
	}
	status, m := postJSON(t, srv, "/mcp", headers, callBody(t, "ping", nil))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400: %v", status, m)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModernCall_V7_SentinelDecodedBeforeCompare(t *testing.T) {
	// "ping" Base64-encoded, wrapped in the sentinel markers, must
	// compare equal to the plain body value after decoding.
	srv := modernServer(t)
	headers := map[string]string{
		headerMCPProtocolVersion: mcpModernProtocolVersion,
		headerMCPMethod:          "tools/call",
		headerMCPName:            "=?base64?cGluZw==?=",
	}
	status, m := postJSON(t, srv, "/mcp", headers, callBody(t, "ping", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 || content[0].(map[string]any)["text"] != "pong\n" {
		t.Errorf("expected ping output via decoded sentinel: %v", res)
	}
}

func TestModernCall_V7_MalformedSentinelRejected(t *testing.T) {
	// Wrapped in sentinel markers but not valid base64 inside: must
	// fail closed rather than falling back to a literal comparison.
	srv := modernServer(t)
	headers := map[string]string{
		headerMCPProtocolVersion: mcpModernProtocolVersion,
		headerMCPMethod:          "tools/call",
		headerMCPName:            "=?base64?not-valid-base64!!!?=",
	}
	status, m := postJSON(t, srv, "/mcp", headers, callBody(t, "ping", nil))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400: %v", status, m)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModernCall_V7_LiteralSentinelLookalikeMustBeEncoded(t *testing.T) {
	// A plain-ASCII tool name that merely looks like the sentinel
	// pattern is always treated as encoded per spec; comparing it
	// literally against a matching plain body value must still fail
	// unless the client base64-encoded it as required.
	srv := modernServer(t)
	headers := map[string]string{
		headerMCPProtocolVersion: mcpModernProtocolVersion,
		headerMCPMethod:          "tools/call",
		headerMCPName:            "=?base64?literal?=",
	}
	status, m := postJSON(t, srv, "/mcp", headers, callBody(t, "=?base64?literal?=", nil))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400 (lookalike must fail closed as malformed base64): %v", status, m)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModernCall_V7_DuplicateHeaderConflictingValuesRejected(t *testing.T) {
	// A duplicate Mcp-Name with differing values is itself a
	// validation failure, same rule as V6's Mcp-Method duplicates.
	srv := modernServer(t)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp",
		strings.NewReader(callBody(t, "ping", nil)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerMCPProtocolVersion, mcpModernProtocolVersion)
	req.Header.Set(headerMCPMethod, "tools/call")
	req.Header.Add(headerMCPName, "ping")
	req.Header.Add(headerMCPName, "widget.list")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want=400", resp.StatusCode)
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

func TestModernCall_V7_DuplicateHeaderIdenticalValuesTolerated(t *testing.T) {
	// Byte-identical duplicate Mcp-Name values are tolerated.
	srv := modernServer(t)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp",
		strings.NewReader(callBody(t, "ping", nil)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerMCPProtocolVersion, mcpModernProtocolVersion)
	req.Header.Set(headerMCPMethod, "tools/call")
	req.Header.Add(headerMCPName, "ping")
	req.Header.Add(headerMCPName, "ping")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want=200", resp.StatusCode)
	}
}

func TestModernCall_V7_CaseVariantHeaderNameAccepted(t *testing.T) {
	// Header names are case-insensitive (RFC 9110); a differently-cased
	// Mcp-Name still satisfies V7 when the value agrees.
	srv := modernServer(t)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp",
		strings.NewReader(callBody(t, "ping", nil)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerMCPProtocolVersion, mcpModernProtocolVersion)
	req.Header.Set(headerMCPMethod, "tools/call")
	req.Header.Set("mcp-name", "ping") // lowercase variant
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want=200", resp.StatusCode)
	}
}

func TestModernCall_V7_UppercaseSentinelMarkerTreatedLiteral(t *testing.T) {
	// Sentinel markers are case-sensitive and MUST appear exactly as
	// shown (lowercase) per spec; an uppercase variant is not
	// recognized as the sentinel and is compared literally, so it
	// mismatches a body name of "ping".
	srv := modernServer(t)
	headers := map[string]string{
		headerMCPProtocolVersion: mcpModernProtocolVersion,
		headerMCPMethod:          "tools/call",
		headerMCPName:            "=?BASE64?cGluZw==?=",
	}
	status, m := postJSON(t, srv, "/mcp", headers, callBody(t, "ping", nil))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=400: %v", status, m)
	}
	if code := errCode(m); code != mcpErrHeaderMismatch {
		t.Errorf("code=%d want=%d (-32020)", code, mcpErrHeaderMismatch)
	}
}

// --- Pre-flight gates ---------------------------------------------------

func TestModernCall_AuthRequired401(t *testing.T) {
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "auth-op"),
		callBody(t, "auth-op", nil))
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d want=401", status)
	}
	res, _ := m["result"].(map[string]any)
	if res["isError"] != true {
		t.Errorf("isError=%v want=true", res["isError"])
	}
	if res["resultType"] != "complete" {
		t.Errorf("resultType=%v want=complete", res["resultType"])
	}
	meta, _ := res["_meta"].(map[string]any)
	if _, ok := meta[metaKeyServerInfo].(map[string]any); !ok {
		t.Errorf("_meta serverInfo missing on gate refusal: %v", res)
	}
}

func TestModernCall_AuthHeaderPasses(t *testing.T) {
	srv := modernServer(t)
	headers := modernHeaders("tools/call", "auth-op")
	headers["Authorization"] = "Bearer token"
	status, m := postJSON(t, srv, "/mcp", headers, callBody(t, "auth-op", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	if res["isError"] != false {
		t.Errorf("isError=%v want=false: %v", res["isError"], res)
	}
}

func TestModernCall_ConfirmationRequired428(t *testing.T) {
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "confirm-op"),
		callBody(t, "confirm-op", nil))
	if status != http.StatusPreconditionRequired {
		t.Fatalf("status=%d want=428", status)
	}
	res, _ := m["result"].(map[string]any)
	if res["isError"] != true {
		t.Errorf("isError=%v want=true", res["isError"])
	}
	if res["resultType"] != "complete" {
		t.Errorf("resultType=%v want=complete", res["resultType"])
	}
}

func TestModernCall_ConfirmTokenPasses(t *testing.T) {
	srv := modernServer(t)
	headers := modernHeaders("tools/call", "confirm-op")
	headers["X-Confirm-Token"] = "yes"
	status, m := postJSON(t, srv, "/mcp", headers, callBody(t, "confirm-op", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	if res["isError"] != false {
		t.Errorf("isError=%v want=false: %v", res["isError"], res)
	}
}

func TestModernCall_DestructiveBlockedByDefault(t *testing.T) {
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "nuke"),
		callBody(t, "nuke", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	res, _ := m["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("isError=%v want=true (destructive blocked): %v", res["isError"], res)
	}
	content, _ := res["content"].([]any)
	text, _ := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "destructive") {
		t.Errorf("block message=%q must mention destructive", text)
	}
}

func TestModernCall_DestructiveAllowedWhenPolicyOptsIn(t *testing.T) {
	b := New(modernTestTree(), WithPolicy(Policy{
		AllowDestructiveOn: []Surface{SurfaceMCP},
	}))
	srv := modernServerFor(t, b)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "nuke"),
		callBody(t, "nuke", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	res, _ := m["result"].(map[string]any)
	if res["isError"] != false {
		t.Fatalf("isError=%v want=false (policy opted in): %v", res["isError"], res)
	}
	content, _ := res["content"].([]any)
	if text := content[0].(map[string]any)["text"]; text != "boom\n" {
		t.Errorf("content[0].text=%q want=%q", text, "boom\n")
	}
}

// --- Meta / audit stamps ------------------------------------------------

func TestModernCall_MetaStamps(t *testing.T) {
	captured := make(chan Invocation, 1)
	b := New(modernTestTree(), WithRunner(&fakeRunner{
		run: func(_ context.Context, inv Invocation) (Result, error) {
			captured <- inv
			return Result{Stdout: "ok"}, nil
		},
	}))
	srv := modernServerFor(t, b)
	_, _ = postJSON(t, srv, "/mcp", modernHeaders("tools/call", "ping"),
		callBody(t, "ping", nil))

	inv := <-captured
	if inv.Meta.Surface != SurfaceMCP {
		t.Errorf("Surface=%v want=%v", inv.Meta.Surface, SurfaceMCP)
	}
	if inv.Meta.RequestedAt.IsZero() {
		t.Error("RequestedAt is zero")
	}
	if got := inv.Meta.Extra["mcp_spec_version"]; got != mcpModernProtocolVersion {
		t.Errorf("Extra[mcp_spec_version]=%q want=%q", got, mcpModernProtocolVersion)
	}
	if _, ok := inv.Meta.Extra["mcp_client_name"]; ok {
		t.Error("mcp_client_name set without clientInfo in the request")
	}
	if _, ok := inv.Meta.Extra["mcp_client_version"]; ok {
		t.Error("mcp_client_version set without clientInfo in the request")
	}
}

func TestModernCall_MetaStamps_ClientInfo(t *testing.T) {
	captured := make(chan Invocation, 1)
	b := New(modernTestTree(), WithRunner(&fakeRunner{
		run: func(_ context.Context, inv Invocation) (Result, error) {
			captured <- inv
			return Result{Stdout: "ok"}, nil
		},
	}))
	srv := modernServerFor(t, b)

	meta := modernMeta()
	meta[metaKeyClientInfo] = map[string]any{"name": "clientx", "version": "1.2.0"}
	body := modernBodyWithMeta(t, "tools/call", map[string]any{"name": "ping"}, meta)
	_, _ = postJSON(t, srv, "/mcp", modernHeaders("tools/call", "ping"), body)

	inv := <-captured
	if got := inv.Meta.Extra["mcp_client_name"]; got != "clientx" {
		t.Errorf("Extra[mcp_client_name]=%q want=clientx", got)
	}
	if got := inv.Meta.Extra["mcp_client_version"]; got != "1.2.0" {
		t.Errorf("Extra[mcp_client_version]=%q want=1.2.0", got)
	}
}
