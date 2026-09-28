package mcpsdk

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/cmdsurface"
)

// refusalResult is the isError tool result for a call the bridge
// refused or a run its deadline cut short. A refusal with a stable
// code (rate_limited, overloaded, insufficient_scope,
// deadline_exceeded, idempotency_conflict, idempotency_key_reused)
// starts its text with the code and carries it — with retry_after_ms
// when there is a retry hint — in the result's _meta under
// [cmdsurface.MCPRefusalMetaKey], so a client can branch or back off
// without parsing prose. Any other error is its message.
func refusalResult(err error) *mcp.CallToolResult {
	text, refusal, ok := cmdsurface.MCPRefusal(err)
	if !ok {
		return errorResult(err.Error())
	}
	res := errorResult(text)
	res.Meta = mcp.Meta{cmdsurface.MCPRefusalMetaKey: refusal}
	return res
}
