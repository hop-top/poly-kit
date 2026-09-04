package cmdsurface

import (
	"net/http"
)

// handleToolsList builds the modern tools/list result: the same tool
// envelopes the legacy handler emits (buildToolEnvelope — name,
// description, inputSchema; schema drift between eras cannot happen),
// wrapped in the modern complete-result envelope with cache hints.
// Optional 2026-07-28 descriptor fields (title, icons, outputSchema,
// annotations, x-mcp-header) are not emitted. Pagination is not
// implemented: a cursor param is ignored and no nextCursor is
// returned.
func (h *mcpModernHandler) handleToolsList(w http.ResponseWriter, rpc jsonRPCRequest) {
	leaves := h.b.Leaves()
	tools := make([]map[string]any, 0, len(leaves))
	for _, leaf := range leaves {
		if !leaf.Enabled[SurfaceMCP] {
			continue
		}
		tools = append(tools, buildToolEnvelope(leaf))
	}
	res := map[string]any{"tools": tools}
	h.applyCacheHints(res)
	h.stampResultEnvelope(res)
	writeJSONRPCResult(w, rpc.ID, res, http.StatusOK)
}
