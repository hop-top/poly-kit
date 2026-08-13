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
	"encoding/json"
	"fmt"
	"net/http"
)

// mcpResultTypeComplete is the resultType every modern result
// envelope carries. MRTR "input_required" results are not produced;
// tool-execution errors are complete results per spec.
const mcpResultTypeComplete = "complete"

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
	// the X-Confirm-Token header gate mirroring the legacy handler.
	confirm mcpConfirmationGate
}

// newMCPModernHandler builds the 2026-07-28 handler for one mount.
func newMCPModernHandler(b *Bridge, cfg mcpConfig) *mcpModernHandler {
	return &mcpModernHandler{b: b, cfg: cfg, confirm: mcpHeaderConfirmationGate}
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
	// empty body, discarded without processing); null → malformed.
	if len(rpc.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if bytes.Equal(rpc.ID, []byte("null")) {
		h.writeModernError(w, rpc, &modernCheckError{
			code:   mcpErrInvalidRequest,
			msg:    "invalid request id: null",
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
	// _meta protocolVersion value.
	hdr := req.Header.Get(headerMCPProtocolVersion)
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

// validateMethodHeader is the V6 step: Mcp-Method header presence and
// header/body agreement (-32020 @ 400 on failure, per ADR 0004).
// Enforcement is not yet implemented — every request is currently
// accepted.
func (h *mcpModernHandler) validateMethodHeader(*http.Request, jsonRPCRequest) *modernCheckError {
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
// carries: resultType "complete" and a result-level _meta with
// io.modelcontextprotocol/serverInfo built from the mount's
// configured server identity (the same values the legacy initialize
// reports). Returns m for chaining.
func (h *mcpModernHandler) stampResultEnvelope(m map[string]any) map[string]any {
	m["resultType"] = mcpResultTypeComplete
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
