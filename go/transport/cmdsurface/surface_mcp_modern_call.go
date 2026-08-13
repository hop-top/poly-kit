package cmdsurface

// Modern tools/call: V7 (Mcp-Name slot), V9 (per-method params),
// pre-flight gates (auth, confirmation slot), Bridge.Invoke, render.
// The pre-flight gates mirror the legacy handler exactly (same
// conditions, same HTTP statuses); the shared Bridge.Invoke path
// keeps the policy gate identical on both eras — there is no way to
// reach a leaf here that the legacy path would have blocked.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// mcpConfirmationGate decides whether a leaf classified
// RequiresConfirmation may proceed. A nil refusal means proceed;
// otherwise refusal is written as the call's JSON-RPC result with the
// returned HTTP status (after modern envelope stamping). The rpc
// envelope is passed so a gate can read request params (the MRTR
// confirmation flow reads inputResponses and clientCapabilities from
// them; the default header gate does not).
type mcpConfirmationGate func(req *http.Request, leaf *Leaf, rpc jsonRPCRequest) (refusal map[string]any, status int)

// mcpHeaderConfirmationGate is the default confirmation gate: a
// RequiresConfirmation leaf needs the X-Confirm-Token header, exactly
// as on the legacy path.
func mcpHeaderConfirmationGate(req *http.Request, leaf *Leaf, _ jsonRPCRequest) (map[string]any, int) {
	if leaf.Class.RequiresConfirmation && req.Header.Get("X-Confirm-Token") == "" {
		return errorResultBlock("confirmation required"), http.StatusPreconditionRequired
	}
	return nil, 0
}

// handleToolsCall decodes a modern tools/call request, applies V7 and
// V9, mirrors the legacy pre-flight gates, and dispatches via the
// bridge. Error mapping matches the legacy handler: unknown /
// not-enabled tool → -32602 @ 200; destructive blocks, runner errors,
// and non-zero exit codes → isError result envelopes (all stamped
// resultType "complete"). tools/call results carry no cache hints.
//
// V7 runs against a raw peek of params.name (rawToolCallName) before
// the full params decode, so a header-validation failure (missing
// Mcp-Name, empty-after-decode, absent params.name, mismatch) is
// reported as -32020@400 even when the rest of params is unparseable
// — ADR 0004: V7 precedes V9's params decode, and does not require
// the body to have decoded to run.
func (h *mcpModernHandler) handleToolsCall(w http.ResponseWriter, req *http.Request, rpc jsonRPCRequest, meta modernRequestMeta) {
	// V7 — Mcp-Name header agreement (slot; see below). Runs against a
	// pre-decode peek of params.name so it never depends on the rest
	// of params being well-formed.
	rawName, namePresent, nameIsString := rawToolCallName(rpc.Params)
	if e := h.validateNameHeader(req, rawName, namePresent, nameIsString); e != nil {
		h.writeModernError(w, rpc, e)
		return
	}

	var p callParams
	if len(rpc.Params) > 0 {
		if err := json.Unmarshal(rpc.Params, &p); err != nil {
			writeJSONRPCError(w, rpc.ID, mcpErrInvalidParams, "invalid params: "+err.Error(), http.StatusOK)
			return
		}
	}

	// V9 — per-method params. Unreachable through a conforming HTTP
	// request now that V7 requires params.name to be present and
	// non-empty and to match a required, non-empty Mcp-Name header
	// (ADR 0004): any request that could reach this branch would
	// already have failed V7. Kept as a defensive internal check for
	// any future caller of this method that bypasses the V7 gate
	// above.
	if p.Name == "" {
		writeJSONRPCError(w, rpc.ID, mcpErrInvalidParams, "missing tool name", http.StatusOK)
		return
	}
	leaf, err := h.b.resolveLeaf(pathFromToolName(p.Name))
	if err != nil || !leaf.Enabled[SurfaceMCP] {
		writeJSONRPCError(w, rpc.ID, mcpErrInvalidParams, "unknown tool: "+p.Name, http.StatusOK)
		return
	}

	// Pre-flight gates, mirroring legacy: isError on the result
	// envelope so MCP-aware clients see the failure while HTTP-only
	// clients see the matching status code.
	if leaf.Class.AuthRequired && req.Header.Get("Authorization") == "" {
		h.writeCallError(w, rpc, "authentication required", http.StatusUnauthorized)
		return
	}
	gate := h.confirm
	if gate == nil {
		gate = mcpHeaderConfirmationGate
	}
	if refusal, status := gate(req, leaf, rpc); refusal != nil {
		writeJSONRPCResult(w, rpc.ID, h.stampResultEnvelope(refusal), status)
		return
	}

	inv := Invocation{
		Path:  append([]string(nil), leaf.Path...),
		Flags: p.Arguments,
		Meta: Meta{
			Surface:     SurfaceMCP,
			RequestedAt: time.Now(),
			Extra:       modernInvocationExtra(meta),
		},
	}

	res, err := h.b.Invoke(req.Context(), inv)
	if err != nil {
		switch {
		case errors.Is(err, ErrUnknownCommand),
			errors.Is(err, ErrSurfaceNotEnabled):
			writeJSONRPCError(w, rpc.ID, mcpErrInvalidParams, "unknown tool: "+p.Name, http.StatusOK)
		default:
			// ErrDestructiveBlocked and every other invoke failure are
			// complete isError results at HTTP 200, as on legacy.
			h.writeCallError(w, rpc, err.Error(), http.StatusOK)
		}
		return
	}

	// Content blocks reuse the legacy renderer exactly (stdout block,
	// "[stderr] " block when present, JSON text block when Result.Data
	// is present — the text block doubles as the serialized fallback
	// for structuredContent).
	out := renderCallResult(res)
	if res.Data != nil {
		out["structuredContent"] = res.Data
	}
	writeJSONRPCResult(w, rpc.ID, h.stampResultEnvelope(out), http.StatusOK)
}

// rawToolCallName peeks the params.name *key* out of a tools/call
// request's raw params without requiring the rest of params to
// decode, so V7 can run (and fail on a genuinely absent name)
// independently of whatever else is wrong with the body. present
// reports whether params is a JSON object carrying a "name" key at
// all (ADR 0004 V7: "params.name absent" is a header-validation
// failure, checked ahead of the full params decode). When present is
// true but the key's value is not a JSON string, name is "" and
// isString is false — V7 cannot compare a non-string value against
// the header, so that case is left for V9's params decode to reject
// as a shape error (mirrors legacy's existing type-mismatch handling)
// rather than being folded into V7's "absent" case.
func rawToolCallName(rawParams json.RawMessage) (name string, present, isString bool) {
	if len(rawParams) == 0 {
		return "", false, false
	}
	var p struct {
		Name *json.RawMessage `json:"name"`
	}
	if err := json.Unmarshal(rawParams, &p); err != nil || p.Name == nil {
		return "", false, false
	}
	if err := json.Unmarshal(*p.Name, &name); err != nil {
		return "", true, false
	}
	return name, true, true
}

// validateNameHeader is the V7 step: on tools/call, Mcp-Name header
// MUST be present, non-empty after Base64-sentinel (=?base64?...?=)
// decoding, and byte-equal to params.name, which MUST itself be
// present (-32020 @ 400 on any violation, per ADR 0004). Conflicting
// duplicate Mcp-Name headers are a violation in their own right
// (singleHeaderValue); byte-identical duplicates are tolerated. A
// header value that merely looks like the sentinel (starts with the
// prefix and ends with the suffix) is always treated as encoded, so a
// decode failure fails closed rather than falling back to a literal
// comparison.
//
// rawName/namePresent/nameIsString come from a pre-decode peek of
// params.name (rawToolCallName): namePresent=false (the "name" key is
// altogether missing) is the V7 "params.name absent" violation.
// namePresent=true with nameIsString=false (the key exists but isn't
// a JSON string — e.g. "name": 12) is a different failure category:
// V7 has no string to compare against the header, so it defers rather
// than guessing, and the request falls through to V9's params decode,
// which rejects the type mismatch as a shape error.
func (h *mcpModernHandler) validateNameHeader(req *http.Request, rawName string, namePresent, nameIsString bool) *modernCheckError {
	hdr, ok := singleHeaderValue(req, headerMCPName)
	if !ok {
		return &modernCheckError{
			code:   mcpErrHeaderMismatch,
			msg:    headerMCPName + " header sent with conflicting duplicate values",
			status: http.StatusBadRequest,
		}
	}
	if hdr == "" {
		return &modernCheckError{
			code:   mcpErrHeaderMismatch,
			msg:    "missing " + headerMCPName + " header",
			status: http.StatusBadRequest,
		}
	}
	decoded, ok := decodeMCPSentinel(hdr)
	if !ok {
		return &modernCheckError{
			code:   mcpErrHeaderMismatch,
			msg:    headerMCPName + " header value is not valid base64-sentinel encoded",
			status: http.StatusBadRequest,
		}
	}
	if decoded == "" {
		return &modernCheckError{
			code:   mcpErrHeaderMismatch,
			msg:    headerMCPName + " header decodes to an empty value",
			status: http.StatusBadRequest,
		}
	}
	if !namePresent {
		return &modernCheckError{
			code:   mcpErrHeaderMismatch,
			msg:    headerMCPName + " header present but body params.name is absent",
			status: http.StatusBadRequest,
		}
	}
	if !nameIsString {
		// params.name exists but is not a JSON string: V7 cannot
		// evaluate agreement, so it defers to V9's params decode.
		return nil
	}
	if decoded != rawName {
		return &modernCheckError{
			code: mcpErrHeaderMismatch,
			msg: fmt.Sprintf("%s header %q does not match body params.name %q",
				headerMCPName, decoded, rawName),
			status: http.StatusBadRequest,
		}
	}
	return nil
}

// writeCallError writes an isError tools/call result envelope with
// the modern envelope members stamped on.
func (h *mcpModernHandler) writeCallError(w http.ResponseWriter, rpc jsonRPCRequest, msg string, status int) {
	writeJSONRPCResult(w, rpc.ID, h.stampResultEnvelope(errorResultBlock(msg)), status)
}

// modernInvocationExtra builds the Meta.Extra audit bag for a modern
// invocation: the spec version always, and the client identity when
// the request carried io.modelcontextprotocol/clientInfo.
func modernInvocationExtra(meta modernRequestMeta) map[string]string {
	extra := map[string]string{"mcp_spec_version": mcpModernProtocolVersion}
	if meta.hasClientInfo {
		extra["mcp_client_name"] = meta.clientName
		extra["mcp_client_version"] = meta.clientVersion
	}
	return extra
}
