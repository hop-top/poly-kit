package mcpsdk

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/cmdsurface"
)

// methodListTools is the MCP method whose result callerToolList
// narrows.
const methodListTools = "tools/list"

// callerToolList narrows a tools/list result to what its caller may
// call. The caller is identified as a tool call would be — the
// [WithCallMeta] provenance and the [WithAuthenticated] predicate,
// asked with the list request's session and HTTP extras — and, when
// the transport established it, every listed tool the permission gate
// (slot 6) would refuse that caller is left off, through
// [cmdsurface.Bridge.Verdict]. A list request whose caller nobody
// established gets the list every such caller gets.
//
// The list stays advisory: every call still meets every gate.
func (s *Surface) callerToolList(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		if err != nil || method != methodListTools {
			return res, err
		}
		list, ok := res.(*mcp.ListToolsResult)
		lreq, isList := req.(*mcp.ListToolsRequest)
		if !ok || !isList || list == nil {
			return res, err
		}
		call := &mcp.CallToolRequest{Session: lreq.Session, Extra: lreq.Extra}
		meta := s.callMeta(ctx, call)
		if !s.authenticated(ctx, call, &meta) || !meta.Authenticated() {
			return res, err
		}
		leaves := make(map[string]*cmdsurface.Leaf)
		for _, leaf := range s.b.Leaves() {
			leaves[toolName(leaf.Path)] = leaf
		}
		kept := make([]*mcp.Tool, 0, len(list.Tools))
		for _, t := range list.Tools {
			if t != nil && s.b.Verdict(ctx, meta, leaves[t.Name]) != nil {
				continue
			}
			kept = append(kept, t)
		}
		narrowed := *list
		narrowed.Tools = kept
		return &narrowed, nil
	}
}
