package cmdsurface

// Modern (2026-07-28) MCP handler: the stateless request core behind
// the era dispatcher in surface_mcp_dispatch.go. This file implements
// ADR 0004's validation order V1-V8, server/discover, the modern
// error writers, result-envelope stamping (resultType + serverInfo
// _meta), and cache-hint application. tools/list lives in
// surface_mcp_modern_list.go; tools/call (V7/V9, pre-flight gates,
// invoke, render) in surface_mcp_modern_call.go.
//
// Statelessness is the core contract of the revision: every request
// carries protocol version, client identity, and capabilities in
// params._meta under reserved io.modelcontextprotocol/* keys; there
// is no initialize/initialized handshake and no Mcp-Session-Id. The
// handler holds only immutable mount-time configuration, so any
// instance can serve any request.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// mcpResultTypeComplete and mcpResultTypeInputRequired are the two
// resultType values a modern result envelope carries. Everything is
// "complete" — tool-execution errors included, per spec — except the
// interim results the MRTR confirmation flow produces
// (surface_mcp_modern_confirm.go), which are "input_required".
const (
	mcpResultTypeComplete      = "complete"
	mcpResultTypeInputRequired = "input_required"
)

// mcpModernHandler serves the 2026-07-28 protocol revision. It is
// reachable only through the era dispatcher (or the modern-only
// mount); it never reads req.Body — the dispatcher parses the body
// once and hands the decoded envelope to serveParsed. Stateless
// across requests; safe to share across goroutines (state lives on
// Bridge and Runner, same contract as mcpHandler).
type mcpModernHandler struct {
	b   *Bridge
	cfg mcpConfig
	// confirm is the strategy slot for the confirmation step in
	// tools/call (ADR 0004 "MRTR confirmation slot"). The default is
	// the X-Confirm-Token header gate mirroring the legacy handler;
	// mounts given key material via WithMCPConfirmationKey install
	// the MRTR elicitation gate (surface_mcp_modern_confirm.go)
	// instead.
	confirm mcpConfirmationGate
}

// newMCPModernHandler builds the 2026-07-28 handler for one mount.
func newMCPModernHandler(b *Bridge, cfg mcpConfig) *mcpModernHandler {
	h := &mcpModernHandler{b: b, cfg: cfg, confirm: mcpHeaderConfirmationGate}
	if len(cfg.confirmKey) > 0 {
		h.confirm = h.elicitationConfirmGate
	}
	return h
}

// modernCheckError is one validation-chain failure: JSON-RPC error
// code, message, HTTP status, and optional data payload.
type modernCheckError struct {
	code   int
	msg    string
	status int
	data   any
}

// modernRequestMeta is the decoded view of the reserved
// params._meta keys a modern request carries (V3).
type modernRequestMeta struct {
	// version is the io.modelcontextprotocol/protocolVersion value
	// when it is a JSON string; versionIsText reports whether it was.
	version       string
	versionIsText bool
	// versionRaw is the decoded value whatever its JSON type; V5's
	// error data echoes it back as "requested".
	versionRaw any
	// clientName / clientVersion come from the optional
	// io.modelcontextprotocol/clientInfo object; hasClientInfo
	// reports whether the key was present and decoded as an object.
	clientName    string
	clientVersion string
	hasClientInfo bool
}

// serveParsed is the modern entry point. The validation chain runs in
// ADR 0004's order V1-V9; the first failure responds and stops. HTTP
// status is 400/404 only where the spec mandates it; application-level
// JSON-RPC errors ride HTTP 200 (V9, in the per-method handlers).
func (h *mcpModernHandler) serveParsed(w http.ResponseWriter, req *http.Request, rpc jsonRPCRequest) {
	// Origin allowlist (opt-in, WithMCPOriginAllowlist): applies to
	// the modern path only, before any protocol validation.
	if !h.originAllowed(req) {
		h.writeModernError(w, rpc, &modernCheckError{
			code:   mcpErrInvalidRequest,
			msg:    "origin not allowed",
			status: http.StatusForbidden,
		})
		return
	}

	// V1 — jsonrpc member absent or "2.0" (same tolerance as legacy).
	if rpc.JSONRPC != "" && rpc.JSONRPC != "2.0" {
		h.writeModernError(w, rpc, &modernCheckError{
			code:   mcpErrInvalidRequest,
			msg:    "invalid jsonrpc version",
			status: http.StatusBadRequest,
		})
		return
	}

	// V2 — id present → request; absent → notification (HTTP 202,
	// empty body, discarded without processing); id MUST be a string
	// or integer (base JSON-RPC also allows null, but the spec
	// explicitly forbids it here) — null, bool, float, object, and
	// array ids are all malformed.
	if len(rpc.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !validModernRequestID(rpc.ID) {
		h.writeModernError(w, rpc, &modernCheckError{
			code:   mcpErrInvalidRequest,
			msg:    "invalid request id: must be a string or integer, got " + string(rpc.ID),
			status: http.StatusBadRequest,
		})
		return
	}

	// V3 — params._meta carries the required reserved keys.
	meta, checkErr := parseModernMeta(rpc.Params)
	if checkErr != nil {
		h.writeModernError(w, rpc, checkErr)
		return
	}

	// V4 — MCP-Protocol-Version header present and equal to the
	// _meta protocolVersion value. Conflicting duplicate headers are a
	// mismatch in their own right (singleHeaderValue).
	hdr, headerOK := singleHeaderValue(req, headerMCPProtocolVersion)
	if !headerOK {
		h.writeModernError(w, rpc, &modernCheckError{
			code:   mcpErrHeaderMismatch,
			msg:    headerMCPProtocolVersion + " header sent with conflicting duplicate values",
			status: http.StatusBadRequest,
		})
		return
	}
	if hdr == "" {
		h.writeModernError(w, rpc, &modernCheckError{
			code:   mcpErrHeaderMismatch,
			msg:    "missing " + headerMCPProtocolVersion + " header",
			status: http.StatusBadRequest,
		})
		return
	}
	// A non-string _meta value can never equal the header string.
	if !meta.versionIsText || hdr != meta.version {
		h.writeModernError(w, rpc, &modernCheckError{
			code: mcpErrHeaderMismatch,
			msg: fmt.Sprintf("%s header %q does not match _meta protocolVersion %v",
				headerMCPProtocolVersion, hdr, meta.versionRaw),
			status: http.StatusBadRequest,
		})
		return
	}

	// V5 — requested version supported. This handler supports exactly
	// "2026-07-28"; the supported list deliberately excludes
	// "2024-11-05", which is only reachable through its handshake
	// (dispatch rules D2/D4).
	if meta.version != mcpModernProtocolVersion {
		h.writeModernError(w, rpc, &modernCheckError{
			code:   mcpErrUnsupportedVersion,
			msg:    "unsupported protocol version: " + meta.version,
			status: http.StatusBadRequest,
			data: map[string]any{
				"supported": []string{mcpModernProtocolVersion},
				"requested": meta.versionRaw,
			},
		})
		return
	}

	// V6 — Mcp-Method header agreement (slot; see below).
	if e := h.validateMethodHeader(req, rpc); e != nil {
		h.writeModernError(w, rpc, e)
		return
	}

	// V8 — method routing. V7 (Mcp-Name) and V9 (per-method params)
	// run inside the method handlers.
	switch rpc.Method {
	case "server/discover":
		h.handleDiscover(w, rpc)
	case "tools/list":
		h.handleToolsList(w, rpc)
	case "tools/call":
		h.handleToolsCall(w, req, rpc, meta)
	default:
		h.writeModernError(w, rpc, &modernCheckError{
			code:   mcpErrMethodNotFound,
			msg:    "method not found: " + rpc.Method,
			status: http.StatusNotFound,
		})
	}
}

// singleHeaderValue reads all occurrences of a header (case-insensitive
// name, per RFC 9110) and reduces them to one value for comparison
// against the request body. A header sent once, or sent multiple times
// with byte-identical values (benign proxy/intermediary duplication),
// resolves to that value. A header sent multiple times with differing
// values is a validation failure in its own right: MCP-Protocol-Version,
// Mcp-Method, and Mcp-Name exist precisely so gateways and the server
// agree on one routing signal, and conflicting duplicates are the
// multiple-sources-of-truth hazard that agreement check exists to
// close — ok=false so the caller rejects with -32020 without ever
// comparing a value that was never actually singular.
func singleHeaderValue(req *http.Request, name string) (value string, ok bool) {
	vals := req.Header.Values(name)
	switch len(vals) {
	case 0:
		return "", true
	case 1:
		return vals[0], true
	}
	for _, v := range vals[1:] {
		if v != vals[0] {
			return "", false
		}
	}
	return vals[0], true
}

// validModernRequestID reports whether raw (a non-empty rpc.ID) is a
// JSON string or a JSON number with no fractional part — the only two
// shapes the modern id rule (V2) permits. Base JSON-RPC additionally
// allows null, but the spec explicitly forbids null ids here; boolean,
// object, and array ids are rejected the same way a fractional number
// is, since none of them satisfy "string or integer".
func validModernRequestID(raw json.RawMessage) bool {
	// json.Unmarshal treats a JSON "null" source as a no-op for both
	// string and float64 destinations (the destination is left at its
	// zero value with no error), so null must be rejected explicitly
	// before the type probes below, or it would be misread as "".
	if bytes.Equal(raw, []byte("null")) {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return true
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return false
	}
	return f == float64(int64(f))
}

// validateMethodHeader is the V6 step: Mcp-Method header presence and
// header/body agreement (-32020 @ 400 on failure, per ADR 0004).
// Header names are matched case-insensitively per RFC 9110; header
// values are compared case-sensitively against the body method.
// Conflicting duplicate headers are a mismatch in their own right
// (singleHeaderValue); byte-identical duplicates are tolerated.
func (h *mcpModernHandler) validateMethodHeader(req *http.Request, rpc jsonRPCRequest) *modernCheckError {
	hdr, ok := singleHeaderValue(req, headerMCPMethod)
	if !ok {
		return &modernCheckError{
			code:   mcpErrHeaderMismatch,
			msg:    headerMCPMethod + " header sent with conflicting duplicate values",
			status: http.StatusBadRequest,
		}
	}
	if hdr == "" {
		return &modernCheckError{
			code:   mcpErrHeaderMismatch,
			msg:    "missing " + headerMCPMethod + " header",
			status: http.StatusBadRequest,
		}
	}
	if hdr != rpc.Method {
		return &modernCheckError{
			code: mcpErrHeaderMismatch,
			msg: fmt.Sprintf("%s header %q does not match body method %q",
				headerMCPMethod, hdr, rpc.Method),
			status: http.StatusBadRequest,
		}
	}
	return nil
}

// parseModernMeta decodes the reserved params._meta keys (V3). The
// required keys are io.modelcontextprotocol/protocolVersion and
// io.modelcontextprotocol/clientCapabilities; clientInfo is optional.
// A missing/non-object params or _meta fails the same way as missing
// keys: -32602 @ 400.
func parseModernMeta(rawParams json.RawMessage) (modernRequestMeta, *modernCheckError) {
	var m modernRequestMeta
	fail := func(msg string) *modernCheckError {
		return &modernCheckError{
			code:   mcpErrInvalidParams,
			msg:    msg,
			status: http.StatusBadRequest,
		}
	}
	if len(rawParams) == 0 {
		return m, fail("missing required params._meta")
	}
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(rawParams, &p); err != nil || p.Meta == nil {
		return m, fail("missing required params._meta")
	}
	verRaw, ok := p.Meta[metaKeyProtocolVersion]
	if !ok {
		return m, fail("missing required _meta key: " + metaKeyProtocolVersion)
	}
	if _, ok := p.Meta[metaKeyClientCapabilities]; !ok {
		return m, fail("missing required _meta key: " + metaKeyClientCapabilities)
	}
	if err := json.Unmarshal(verRaw, &m.version); err == nil {
		m.versionIsText = true
		m.versionRaw = m.version
	} else {
		var v any
		_ = json.Unmarshal(verRaw, &v)
		m.versionRaw = v
	}
	// clientInfo only feeds audit metadata (Meta.Extra); a value that
	// does not decode as an object is treated as absent rather than
	// rejected, since V3 does not require the key at all.
	if ciRaw, ok := p.Meta[metaKeyClientInfo]; ok {
		var ci struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(ciRaw, &ci); err == nil {
			m.hasClientInfo = true
			m.clientName = ci.Name
			m.clientVersion = ci.Version
		}
	}
	return m, nil
}

// handleDiscover serves server/discover, the mandatory modern
// discovery method. The result carries no listChanged flag
// (notifications are not implemented), no extensions map (none
// supported), and no instructions.
func (h *mcpModernHandler) handleDiscover(w http.ResponseWriter, rpc jsonRPCRequest) {
	res := map[string]any{
		"supportedVersions": []string{mcpModernProtocolVersion},
		"capabilities":      map[string]any{"tools": map[string]any{}},
	}
	h.applyCacheHints(res)
	h.stampResultEnvelope(res)
	writeJSONRPCResult(w, rpc.ID, res, http.StatusOK)
}

// stampResultEnvelope adds the members every modern result envelope
// carries: resultType and a result-level _meta with
// io.modelcontextprotocol/serverInfo built from the mount's
// configured server identity (the same values the legacy initialize
// reports). resultType defaults to "complete"; a producer that has
// already chosen one keeps it (the MRTR confirmation gate is the only
// such producer, stamping "input_required" on its interim results).
// Returns m for chaining.
func (h *mcpModernHandler) stampResultEnvelope(m map[string]any) map[string]any {
	if _, ok := m["resultType"]; !ok {
		m["resultType"] = mcpResultTypeComplete
	}
	m["_meta"] = map[string]any{
		metaKeyServerInfo: map[string]any{
			"name":    h.cfg.serverName,
			"version": h.cfg.serverVersion,
		},
	}
	return m
}

// applyCacheHints adds ttlMs and cacheScope to a cacheable
// complete-result — server/discover and tools/list only; tools/call
// results carry no cache hints. Values come from WithMCPCacheHints;
// absent configuration yields ttlMs 0 (immediately stale — Expose /
// Hide can mutate the leaf set at runtime and no list_changed
// notification exists) and cacheScope "private". Returns m for
// chaining.
func (h *mcpModernHandler) applyCacheHints(m map[string]any) map[string]any {
	scope := h.cfg.cacheScope
	if scope == "" {
		scope = MCPCacheScopePrivate
	}
	m["ttlMs"] = h.cfg.cacheTTL.Milliseconds()
	m["cacheScope"] = string(scope)
	return m
}

// originAllowed applies the opt-in Origin allowlist to the modern
// path. No allowlist configured (the default) → no check; a request
// without an Origin header is never refused; otherwise the Origin
// must exactly match one allowlist entry.
func (h *mcpModernHandler) originAllowed(req *http.Request) bool {
	if len(h.cfg.originAllowlist) == 0 {
		return true
	}
	origin := req.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, allowed := range h.cfg.originAllowlist {
		if origin == allowed {
			return true
		}
	}
	return false
}

// writeModernError writes a JSON-RPC error envelope for the modern
// path. When the rejected request's method is "initialize" the
// message additionally names the supported protocol versions: a
// legacy client has no fall-forward mechanism, so the version list in
// the error text is its only recovery hint (spec SHOULD for
// modern-only servers; see ADR 0004).
func (h *mcpModernHandler) writeModernError(w http.ResponseWriter, rpc jsonRPCRequest, e *modernCheckError) {
	msg := e.msg
	if rpc.Method == "initialize" {
		msg += "; supported protocol versions: " + mcpModernProtocolVersion
	}
	if e.data != nil {
		writeJSONRPCErrorWithData(w, rpc.ID, e.code, msg, e.status, e.data)
		return
	}
	writeJSONRPCError(w, rpc.ID, e.code, msg, e.status)
}

// writeJSONRPCErrorWithData writes a JSON-RPC error envelope carrying
// a data payload, mirroring writeJSONRPCError but with the additional
// Data field the modern error codes (-3202x) use.
func writeJSONRPCErrorWithData(w http.ResponseWriter, id json.RawMessage, code int, msg string, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: msg, Data: data},
	})
}

// mcpSentinelPrefix and mcpSentinelSuffix delimit the Base64 sentinel
// encoding a header value carries when it cannot be represented as a
// safe plain-ASCII header value. Markers are case-sensitive and must
// appear exactly as shown (spec: Value Encoding).
const (
	mcpSentinelPrefix = "=?base64?"
	mcpSentinelSuffix = "?="
)

// decodeMCPSentinel decodes a header value that may carry the Base64
// sentinel encoding, returning the effective value to compare against
// the request body. A value that is not sentinel-wrapped is returned
// unchanged (conforming tool/resource names are header-safe ASCII and
// are sent plain). A sentinel-wrapped value that fails to decode as
// base64 is reported via ok=false so the caller can fail closed with
// -32020, per spec: servers "MUST decode ... before comparing"; a
// malformed encoding can never legitimately match the body value.
func decodeMCPSentinel(v string) (decoded string, ok bool) {
	if !strings.HasPrefix(v, mcpSentinelPrefix) || !strings.HasSuffix(v, mcpSentinelSuffix) {
		return v, true
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(v, mcpSentinelPrefix), mcpSentinelSuffix)
	raw, err := base64.StdEncoding.DecodeString(inner)
	if err != nil {
		return "", false
	}
	return string(raw), true
}
