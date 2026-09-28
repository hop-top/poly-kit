package cmdsurface

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"hop.top/kit/go/transport/api"
)

// callParams is the params shape for tools/call.
type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Meta      map[string]any `json:"_meta,omitempty"`
}

// idempotencyKey is the key a tools/call carries, from the sources the
// SDK surface reads: params._meta[MCPMetaIdempotencyKey], else the
// request's Idempotency-Key header.
func (p callParams) idempotencyKey(req *http.Request) string {
	if k, ok := p.Meta[MCPMetaIdempotencyKey].(string); ok && strings.TrimSpace(k) != "" {
		return k
	}
	return req.Header.Get(api.HeaderIdempotencyKey)
}

// handleToolsCall decodes a tools/call request, looks up the leaf,
// applies pre-flight auth + confirmation gating, and dispatches via
// the bridge. Errors are mapped per the surface contract: unknown /
// not-enabled → JSON-RPC error; everything else (bridge failures,
// destructive blocks, runner errors, non-zero exit codes) →
// result.isError=true.
func (h *mcpHandler) handleToolsCall(w http.ResponseWriter, req *http.Request, rpc jsonRPCRequest) {
	var p callParams
	if len(rpc.Params) > 0 {
		if err := json.Unmarshal(rpc.Params, &p); err != nil {
			writeJSONRPCError(w, rpc.ID, mcpErrInvalidParams, "invalid params: "+err.Error(), http.StatusOK)
			return
		}
	}
	if p.Name == "" {
		writeJSONRPCError(w, rpc.ID, mcpErrInvalidParams, "missing tool name", http.StatusOK)
		return
	}

	path := pathFromToolName(p.Name)
	leaf, err := h.b.resolveLeaf(path)
	if err != nil || !leaf.Enabled[SurfaceMCP] {
		writeJSONRPCError(w, rpc.ID, mcpErrInvalidParams, "unknown tool: "+p.Name, http.StatusOK)
		return
	}

	// Auth + confirmation gating, mirrored on the result envelope so
	// MCP-aware clients see isError while HTTP-only clients see the
	// matching status code. Only an api.Auth on the router
	// authenticates: a bare Authorization header does not.
	meta := establishHTTP(Meta{}, req)
	if leaf.Class.AuthRequired && !meta.Authenticated() {
		w.Header().Set("WWW-Authenticate", api.DefaultAuthChallenge)
		writeJSONRPCResult(w, rpc.ID, errorResultBlock("authentication required"), http.StatusUnauthorized)
		return
	}
	// Arguments are checked before the confirmation gate: a person
	// is never asked to approve a call that cannot run as sent.
	flags, args, err := MCPSplitArguments(leaf, p.Arguments)
	if err != nil {
		writeJSONRPCResult(w, rpc.ID, errorResultBlock(err.Error()), http.StatusOK)
		return
	}
	if leaf.Class.RequiresConfirmation && req.Header.Get("X-Confirm-Token") == "" {
		writeJSONRPCResult(w, rpc.ID, errorResultBlock("confirmation required"), http.StatusPreconditionRequired)
		return
	}

	meta.Surface = SurfaceMCP
	meta.RequestedAt = time.Now()
	meta.IdempotencyKey = p.idempotencyKey(req)
	inv := Invocation{
		Path:  append([]string(nil), leaf.Path...),
		Args:  args,
		Flags: flags,
		Meta:  meta,
	}

	res, err := h.b.Invoke(req.Context(), inv)
	if err != nil {
		switch {
		case errors.Is(err, ErrUnknownCommand),
			errors.Is(err, ErrSurfaceNotEnabled):
			writeJSONRPCError(w, rpc.ID, mcpErrInvalidParams, "unknown tool: "+p.Name, http.StatusOK)
			return
		case errors.Is(err, ErrDestructiveBlocked):
			writeJSONRPCResult(w, rpc.ID, errorResultBlock(err.Error()), http.StatusOK)
			return
		default:
			writeJSONRPCResult(w, rpc.ID, mcpRefusalBlock(err), mcpRefusalStatus(w, req, err))
			return
		}
	}

	writeJSONRPCResult(w, rpc.ID, renderCallResult(res), http.StatusOK)
}

// renderCallResult maps a bridge Result to the MCP tools/call result
// envelope. The content list always contains at least one block (the
// stdout text, possibly empty); stderr and structured Data each add
// an additional block when present.
func renderCallResult(res Result) map[string]any {
	content := []map[string]any{
		{"type": "text", "text": res.Stdout},
	}
	if res.Stderr != "" {
		content = append(content, map[string]any{
			"type": "text",
			"text": "[stderr] " + res.Stderr,
		})
	}
	if res.Data != nil {
		if encoded, err := json.Marshal(res.Data); err == nil {
			content = append(content, map[string]any{
				"type": "text",
				"text": string(encoded),
			})
		}
	}
	out := map[string]any{
		"content": content,
		"isError": res.ExitCode != 0,
	}
	if res.Replayed {
		out["_meta"] = map[string]any{MCPMetaIdempotentReplayed: true}
	}
	return out
}

// errorResultBlock returns a tools/call result envelope flagged
// isError:true with a single text content block carrying msg.
func errorResultBlock(msg string) map[string]any {
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": msg},
		},
		"isError": true,
	}
}

// mcpRefusalStatus is the HTTP status a refused tools/call is answered
// with alongside its isError result: 403 for a scope refusal of a
// caller who presented a bearer token, with the RFC 6750 §3.1
// challenge the MCP authorization spec asks for set on w (the scopes
// the tool needs, and resource_metadata behind a protected resource),
// so the client can step up its authorization; 200 for every other
// refusal, which a new token cannot lift.
func mcpRefusalStatus(w http.ResponseWriter, req *http.Request, err error) int {
	if !errors.Is(err, ErrInsufficientScope) || !api.BearerPresented(req) {
		return http.StatusOK
	}
	scopes, _ := RequiredScopes(err)
	w.Header().Set("WWW-Authenticate", api.ScopeChallenge(req, scopes))
	return http.StatusForbidden
}
