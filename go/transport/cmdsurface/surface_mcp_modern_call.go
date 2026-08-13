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
func (h *mcpModernHandler) handleToolsCall(w http.ResponseWriter, req *http.Request, rpc jsonRPCRequest, meta modernRequestMeta) {
	var p callParams
	if len(rpc.Params) > 0 {
		if err := json.Unmarshal(rpc.Params, &p); err != nil {
			writeJSONRPCError(w, rpc.ID, mcpErrInvalidParams, "invalid params: "+err.Error(), http.StatusOK)
			return
		}
	}

	// V7 — Mcp-Name header agreement (slot; see below).
	if e := h.validateNameHeader(req, p.Name); e != nil {
		h.writeModernError(w, rpc, e)
		return
	}

	// V9 — per-method params.
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

// validateNameHeader is the V7 step: on tools/call, Mcp-Name header
// presence and agreement with params.name after Base64-sentinel
// (=?base64?...?=) decoding (-32020 @ 400 on failure, per ADR 0004).
// A header value that merely looks like the sentinel (starts with the
// prefix and ends with the suffix) is always treated as encoded, so a
// decode failure fails closed rather than falling back to a literal
// comparison.
func (h *mcpModernHandler) validateNameHeader(req *http.Request, name string) *modernCheckError {
	hdr := req.Header.Get(headerMCPName)
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
	if decoded != name {
		return &modernCheckError{
			code: mcpErrHeaderMismatch,
			msg: fmt.Sprintf("%s header %q does not match body params.name %q",
				headerMCPName, decoded, name),
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
