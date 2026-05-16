package cmdsurface

import (
	"encoding/json"
	"errors"
	"net/http"
)

// callParams is the params shape for tools/call.
type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
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
	// matching status code.
	if leaf.Class.AuthRequired && req.Header.Get("Authorization") == "" {
		writeJSONRPCResult(w, rpc.ID, errorResultBlock("authentication required"), http.StatusUnauthorized)
		return
	}
	if leaf.Class.RequiresConfirmation && req.Header.Get("X-Confirm-Token") == "" {
		writeJSONRPCResult(w, rpc.ID, errorResultBlock("confirmation required"), http.StatusPreconditionRequired)
		return
	}

	inv := Invocation{
		Path:  append([]string(nil), leaf.Path...),
		Flags: p.Arguments,
		Meta:  Meta{Surface: SurfaceMCP},
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
			writeJSONRPCResult(w, rpc.ID, errorResultBlock(err.Error()), http.StatusOK)
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
	return map[string]any{
		"content": content,
		"isError": res.ExitCode != 0,
	}
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
