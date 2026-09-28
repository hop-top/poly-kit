package mcpsdk

import (
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/cmdsurface"
)

// callIdempotencyKey is the key the call carries under
// params._meta, which wins over the transport's Idempotency-Key
// header; fallback is that header's value (empty over stdio).
func callIdempotencyKey(req *mcp.CallToolRequest, fallback string) string {
	if req != nil && req.Params != nil {
		if k, ok := req.Params.Meta[cmdsurface.MCPMetaIdempotencyKey].(string); ok && strings.TrimSpace(k) != "" {
			return k
		}
	}
	return fallback
}

// markReplayed sets the replay marker on a result answered from the
// idempotency record.
func markReplayed(out *mcp.CallToolResult, replayed bool) {
	if !replayed {
		return
	}
	if out.Meta == nil {
		out.Meta = mcp.Meta{}
	}
	out.Meta[cmdsurface.MCPMetaIdempotentReplayed] = true
}
