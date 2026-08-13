package cmdsurface

// Era detection + dispatch for the dual-spec MCP surface. This file
// implements ADR 0004's precedence rules D1-D4 and the option/config
// surface that selects which spec version(s) MountMCP serves.
//
// The modern (2026-07-28) handler itself is NOT implemented here — a
// later task lands surface_mcp_modern.go / surface_mcp_modern_list.go
// / surface_mcp_modern_call.go with the full V1-V9 validation chain.
// This file lands the seam: mcpDispatcher routes each request to
// either the legacy handler (byte-for-byte unchanged) or an
// unexported modernHandler hook. The placeholder implementation of
// that hook (placeholderModernHandler below) returns a minimal
// spec-shaped -32022 UnsupportedProtocolVersion error — it is
// intentionally NOT a full modern implementation and is replaced
// wholesale by the next task. Tests in this package assert routing
// decisions (which handler was selected) and legacy byte-identity,
// not the placeholder's wire bytes beyond "well-formed JSON-RPC
// error".

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// MCPSpecVersion identifies a served MCP protocol revision.
type MCPSpecVersion string

// Supported MCP protocol revisions.
const (
	MCPSpec20241105 MCPSpecVersion = "2024-11-05"
	MCPSpec20260728 MCPSpecVersion = "2026-07-28"
)

// MCPCacheScope is the cacheScope value attached to modern cacheable
// list results (server/discover, tools/list).
type MCPCacheScope string

// Recognized cache scopes.
const (
	MCPCacheScopePublic  MCPCacheScope = "public"
	MCPCacheScopePrivate MCPCacheScope = "private"
)

// Unexported anchors shared by the dispatcher and (later) the modern
// handler. Pinned by ADR 0004 so later work and tests agree on names.
const (
	headerMCPProtocolVersion = "MCP-Protocol-Version"
	headerMCPMethod          = "Mcp-Method"
	headerMCPName            = "Mcp-Name"

	metaKeyProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaKeyClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaKeyClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaKeyServerInfo         = "io.modelcontextprotocol/serverInfo"

	mcpErrHeaderMismatch          = -32020
	mcpErrMissingClientCapability = -32021
	mcpErrUnsupportedVersion      = -32022

	mcpModernProtocolVersion = "2026-07-28"
)

// mcpEra identifies which handler a request routes to.
type mcpEra int

const (
	mcpEraLegacy mcpEra = iota
	mcpEraModern
)

// modernMarkerParams is the minimal params shape the dispatcher reads
// to test for M3, without decoding the full modern params structure
// (that belongs to the modern handler's V3 check).
type modernMarkerParams struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// detectMCPEra implements ADR 0004's per-request era detection,
// precedence D1-D4. It is called once per request by mcpDispatcher
// after the body has already been parsed into rpc (D1 — parse — is
// the caller's responsibility: detectMCPEra never fails, it only
// classifies an already-valid jsonRPCRequest).
//
// Markers (ADR 0004 "Modern markers"):
//
//	M1 — HTTP header Mcp-Method present
//	M2 — HTTP header Mcp-Name present
//	M3 — body params._meta contains the reserved key
//	     "io.modelcontextprotocol/protocolVersion" (key presence
//	     only)
//	M4 — body method == "server/discover"
//
// Deliberate non-markers: bare params._meta presence (without the
// reserved key) and the MCP-Protocol-Version header (any value) are
// NOT markers — see ADR 0004 for the full rationale, locked
// separately by the legacy conformance suite.
func detectMCPEra(req *http.Request, rpc jsonRPCRequest) mcpEra {
	// D2 — initialize is legacy, unconditionally, even when modern
	// markers are present.
	if rpc.Method == "initialize" {
		return mcpEraLegacy
	}

	// M4 — method == "server/discover".
	if rpc.Method == "server/discover" {
		return mcpEraModern
	}

	// M1 / M2 — header presence.
	if req.Header.Get(headerMCPMethod) != "" {
		return mcpEraModern
	}
	if req.Header.Get(headerMCPName) != "" {
		return mcpEraModern
	}

	// M3 — params._meta carries the reserved protocolVersion key.
	if hasModernMetaMarker(rpc.Params) {
		return mcpEraModern
	}

	// D4 — no markers: legacy.
	return mcpEraLegacy
}

// hasModernMetaMarker reports whether raw params JSON carries a
// params._meta object containing the reserved
// "io.modelcontextprotocol/protocolVersion" key. Only key presence is
// tested (M3); the value is never inspected at detection time.
// Malformed / non-object params or _meta are treated as "no marker" —
// the legacy path's own params decoding (or the modern handler's V3)
// is responsible for surfacing shape errors; detection never fails.
func hasModernMetaMarker(rawParams json.RawMessage) bool {
	if len(rawParams) == 0 {
		return false
	}
	var p modernMarkerParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return false
	}
	if len(p.Meta) == 0 {
		return false
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(p.Meta, &meta); err != nil {
		return false
	}
	_, ok := meta[metaKeyProtocolVersion]
	return ok
}

// mcpEnabledSet is the resolved, deduplicated set of spec versions a
// mount serves, plus convenience predicates the dispatcher and
// MountMCP consult.
type mcpEnabledSet struct {
	legacy bool
	modern bool
}

// errMCPNoSpecVersions is returned by MountMCP when
// WithMCPSpecVersions is called with zero arguments (an explicit
// empty set), per the ADR's "mount-time refusal" rule.
var errMCPNoSpecVersions = errors.New("cmdsurface: WithMCPSpecVersions: at least one spec version required")

// resolveMCPSpecVersions dedupes and validates the versions passed to
// WithMCPSpecVersions. Callers only invoke this when the option was
// actually supplied (mcpConfig.specVersionsSet); "option not
// supplied" is handled by MountMCP defaulting to both versions
// enabled before this function is ever called. An explicit empty call
// (WithMCPSpecVersions() with zero args) or any unrecognized version
// is a mount-time error.
func resolveMCPSpecVersions(versions []MCPSpecVersion) (mcpEnabledSet, error) {
	if len(versions) == 0 {
		return mcpEnabledSet{}, errMCPNoSpecVersions
	}
	var set mcpEnabledSet
	seen := make(map[MCPSpecVersion]bool, len(versions))
	for _, v := range versions {
		if seen[v] {
			continue
		}
		seen[v] = true
		switch v {
		case MCPSpec20241105:
			set.legacy = true
		case MCPSpec20260728:
			set.modern = true
		default:
			return mcpEnabledSet{}, fmt.Errorf("cmdsurface: WithMCPSpecVersions: unrecognized version %q", v)
		}
	}
	return set, nil
}

// mcpDispatcher is the era-routing http.Handler mounted when both
// spec versions are enabled. It parses the request body once to
// classify it (detectMCPEra), then delegates to exactly one of the
// two handlers. It holds no mutable per-request state; both handlers
// are safe to share across goroutines (same contract as mcpHandler).
//
// Design note: mcpHandler.serveHTTP (surface_mcp.go) is frozen by the
// legacy conformance lock — it is called directly, unexported, by
// TestLegacyLock_ErrorCode_InternalError32603_UnreadableBody, so its
// body-read-then-parse sequence cannot be refactored into a shared
// "parse once" helper without changing that call's shape. Instead the
// dispatcher does its OWN read for classification purposes, then
// rewinds req.Body (via io.NopCloser over the buffered bytes) before
// delegating to h.legacy.serveHTTP, which re-reads and re-parses the
// body exactly as it does today. This costs one extra read+parse on
// the legacy-detected path but keeps mcpHandler.serveHTTP's observable
// behavior — including its -32603/-32700 error text — byte-for-byte
// unchanged, satisfying the "legacy files untouched" constraint
// exactly.
type mcpDispatcher struct {
	legacy *mcpHandler
	modern *mcpModernHandlerSeam
}

// ServeHTTP implements D1 (read + parse once for classification) then
// routes per detectMCPEra (D2-D4). A body-read or JSON-parse failure
// here is reported with the same codes/messages the legacy handler
// would produce (mcpErrInternal / mcpErrParse), matching D1's
// "byte-identical to today's responses, regardless of any headers
// present" rule — but the legacy path is additionally given the
// chance to render its own copy of the same failure by rewinding the
// body and delegating, so mcpHandler.serveHTTP's exact wording is
// always what a legacy-classified (i.e. every parse-failure) request
// receives on the wire.
func (d *mcpDispatcher) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		// Cannot rewind an unreadable body; render the same -32603
		// shape the legacy handler would have produced from this
		// exact failure (empty body, so req.Body is simply left
		// unreadable for the delegate too — but there is no delegate
		// on this branch since detection is impossible).
		writeJSONRPCError(w, nil, mcpErrInternal, "read request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Rewind so the delegate (legacy or modern) sees an unconsumed
	// body, exactly as if the dispatcher had never read it.
	req.Body = io.NopCloser(bytes.NewReader(body))

	var rpc jsonRPCRequest
	if err := json.Unmarshal(body, &rpc); err != nil {
		// Unparseable JSON: D1 says this is byte-identical to today's
		// response regardless of headers. Delegate to the legacy
		// handler so the wire bytes come from the single frozen
		// implementation of that error path.
		d.legacy.serveHTTP(w, req)
		return
	}

	switch detectMCPEra(req, rpc) {
	case mcpEraModern:
		d.modern.serveParsed(w, req, rpc)
	default:
		d.legacy.serveHTTP(w, req)
	}
}

// modernOnlyServeHTTP handles the "modern only" enabled-set case
// (ADR 0004 "Interaction with enabled versions"): every request
// routes to the modern handler and is handled per the normal V1-V9
// order, no special-casing of initialize anywhere — a bare legacy
// initialize therefore fails the modern handler's own validation
// rather than being demoted to legacy (there is no legacy handler
// mounted in this configuration). D1 (parse) still applies: an
// unreadable body or unparseable JSON gets the same -32603/-32700
// shape the legacy path would have produced, since that failure
// happens before any method-specific handling, modern or legacy.
func modernOnlyServeHTTP(modern *mcpModernHandlerSeam, w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		writeJSONRPCError(w, nil, mcpErrInternal, "read request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var rpc jsonRPCRequest
	if err := json.Unmarshal(body, &rpc); err != nil {
		writeJSONRPCError(w, nil, mcpErrParse, "parse error: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	modern.serveParsed(w, req, rpc)
}

// mcp405Handler answers GET/DELETE at the mount path with HTTP 405,
// per ADR 0004 "HTTP verbs": registered only when the modern version
// is enabled (post-session servers respond 405 to the session-era
// verbs). The POST route is unaffected.
func mcp405Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusMethodNotAllowed)
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		Error: &jsonRPCError{
			Code:    mcpErrInvalidRequest,
			Message: "method not allowed",
		},
	})
}

// --- modern-route placeholder (replaced by the next task) -----------

// mcpModernHandlerSeam is the unexported hook the next task fills
// with the real 2026-07-28 handler (mcpModernHandler, V1-V9
// validation, server/discover, tools/list, tools/call). This task
// implements only enough to prove the dispatch seam: every request
// classified modern reaches serveParsed and receives a well-formed
// modern-shaped JSON-RPC error. Production placeholder behavior per
// the task-2 controller ruling: -32022 UnsupportedProtocolVersion
// naming the supported set, since the modern handler does not exist
// yet to serve any modern method.
type mcpModernHandlerSeam struct {
	cfg mcpConfig
}

// serveParsed is the placeholder modern entry point. It intentionally
// does not implement V1-V9 — that is the next task's scope — and
// always answers -32022 regardless of the request shape. The response
// carries a well-formed JSON-RPC error envelope (jsonrpc 2.0, id
// echoed when present) so a modern-aware client can at least identify
// a modern-capable server, per D3's rationale.
func (m *mcpModernHandlerSeam) serveParsed(w http.ResponseWriter, _ *http.Request, rpc jsonRPCRequest) {
	writeJSONRPCErrorWithData(
		w, rpc.ID, mcpErrUnsupportedVersion,
		"unsupported protocol version (modern handler not yet implemented)",
		http.StatusBadRequest,
		map[string]any{
			"supported": []string{mcpModernProtocolVersion},
		},
	)
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

// --- options -----------------------------------------------------------

// WithMCPSpecVersions replaces the set of MCP spec versions the mount
// serves. Absent (option not supplied) defaults to both versions
// enabled. An empty call (WithMCPSpecVersions() with zero arguments)
// or any unrecognized version causes MountMCP to return an error at
// mount time. Duplicate versions are deduplicated.
func WithMCPSpecVersions(versions ...MCPSpecVersion) MCPOption {
	return func(c *mcpConfig) {
		c.specVersionsSet = true
		c.specVersions = append([]MCPSpecVersion(nil), versions...)
	}
}

// WithMCPCacheHints sets the ttlMs and cacheScope values attached to
// modern cacheable list results (server/discover, tools/list
// complete-results). Absent defaults to ttlMs=0, cacheScope="private"
// (see ADR 0004 "Cache hints"). ttl is truncated to whole
// milliseconds; a negative ttl or an unrecognized scope causes
// MountMCP to return an error at mount time.
func WithMCPCacheHints(ttl time.Duration, scope MCPCacheScope) MCPOption {
	return func(c *mcpConfig) {
		c.cacheHintsSet = true
		c.cacheTTL = ttl
		c.cacheScope = scope
	}
}

// WithMCPOriginAllowlist enables Origin-header validation on the
// modern path: a request carrying an Origin header not in origins is
// rejected with HTTP 403. Absent (default) performs no Origin check —
// see ADR 0004 "Acknowledged quirks" for the opt-in rationale.
func WithMCPOriginAllowlist(origins ...string) MCPOption {
	return func(c *mcpConfig) {
		c.originAllowlist = append([]string(nil), origins...)
	}
}
