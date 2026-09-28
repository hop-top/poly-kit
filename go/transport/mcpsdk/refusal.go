package mcpsdk

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/cmdsurface"
)

// refusalResult is the isError tool result for a call the bridge
// refused. A refusal carrying a retry hint (rate_limited) starts its
// text with the code and carries it, with retry_after_ms, in the
// result's _meta under [cmdsurface.MCPRefusalMetaKey], so a client
// can back off without parsing prose. Any other error is its message.
func refusalResult(err error) *mcp.CallToolResult {
	text, refusal, ok := cmdsurface.MCPRefusal(err)
	if !ok {
		return errorResult(err.Error())
	}
	res := errorResult(text)
	res.Meta = mcp.Meta{cmdsurface.MCPRefusalMetaKey: refusal}
	return res
}
