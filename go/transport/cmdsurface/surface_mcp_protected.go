package cmdsurface

import (
	"net/http"

	"hop.top/kit/go/transport/api"
)

// WithMCPProtectedResource puts the MCP authorization flow (MCP
// authorization spec, RFC 9728) in front of the mounted endpoint, as
// mcpsdk.WithProtectedResource does for the SDK surface: pr's
// metadata document answers at its path to anyone, and every request
// to the endpoint must carry a credential fn accepts, or is refused
// 401 with a WWW-Authenticate challenge naming the document. The
// caller fn verifies is the call's established identity. Use it when
// the router does not authenticate the endpoint itself.
//
// Deprecated: an option of MountMCP, which is deprecated; see
// mcpsdk.WithProtectedResource.
func WithMCPProtectedResource(pr *api.ProtectedResource, fn api.AuthFunc) MCPOption {
	return func(c *mcpConfig) {
		c.protected = pr
		c.protectAuth = fn
		c.protectSet = true
	}
}

// mcpProtect returns the wrapper WithMCPProtectedResource puts around
// each MCP handler: the identity without the option.
func mcpProtect(cfg mcpConfig) func(http.HandlerFunc) http.HandlerFunc {
	if cfg.protected == nil {
		return func(h http.HandlerFunc) http.HandlerFunc { return h }
	}
	mw := cfg.protected.Guard(cfg.protectAuth)
	return func(h http.HandlerFunc) http.HandlerFunc { return mw(h).ServeHTTP }
}
