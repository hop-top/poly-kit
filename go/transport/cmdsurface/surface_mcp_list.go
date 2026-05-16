package cmdsurface

import (
	"net/http"
)

// handleToolsList builds the MCP tools/list response by iterating the
// bridge leaves and emitting one tool envelope per leaf where
// SurfaceMCP is enabled.
func (h *mcpHandler) handleToolsList(w http.ResponseWriter, rpc jsonRPCRequest) {
	leaves := h.b.Leaves()
	tools := make([]map[string]any, 0, len(leaves))
	for _, leaf := range leaves {
		if !leaf.Enabled[SurfaceMCP] {
			continue
		}
		tools = append(tools, buildToolEnvelope(leaf))
	}
	writeJSONRPCResult(w, rpc.ID, map[string]any{"tools": tools}, http.StatusOK)
}

// buildToolEnvelope renders one leaf as an MCP tool descriptor.
func buildToolEnvelope(leaf *Leaf) map[string]any {
	props, required := collectFlags(leaf.Cmd)
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return map[string]any{
		"name":        toolName(leaf.Path),
		"description": leaf.Cmd.Short,
		"inputSchema": schema,
	}
}
