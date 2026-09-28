package mcpsdk

// Per-request routing between the SDK's two streamable HTTP handlers.
//
// The SDK serves protocol 2026-07-28 (per-request negotiation, no
// initialize) only from a stateless handler, and a stateless handler
// keeps no session for the earlier revisions: no Mcp-Session-Id, no
// server-to-client requests. Neither handler serves both, so the
// default Handler holds one of each over the same *mcp.Server and
// routes every POST by the markers kit's MCP surfaces already use to
// tell the revisions apart (docs/adopters/guides/expose-cli-over-mcp.md,
// "Routing precedence"). The router only classifies; every response,
// error included, is the chosen SDK handler's.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Modern-revision markers, as the SDK and kit's other MCP surfaces
// spell them.
const (
	headerMCPMethod        = "Mcp-Method"
	headerMCPName          = "Mcp-Name"
	metaKeyProtocolVersion = "io.modelcontextprotocol/protocolVersion"
)

// revisionRouter sends each request to the session handler (protocol
// revisions through 2025-11-25) or the per-request handler (2026-07-28).
type revisionRouter struct {
	sessions   http.Handler // stateful: initialize, Mcp-Session-Id, GET stream, DELETE
	perRequest http.Handler // stateless: 2026-07-28
	maxBody    int64
}

// newRevisionRouter builds both handlers with the same body cap,
// maxBody (negative: none). The router peeks at most that much to
// classify a request; with no cap it peeks the SDK's default.
func newRevisionRouter(
	getServer func(*http.Request) *mcp.Server, jsonResponse bool, maxBody int64,
) *revisionRouter {
	peek := maxBody
	if peek <= 0 {
		peek = mcp.DefaultMaxRequestBodyBytes
	}
	return &revisionRouter{
		sessions: mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
			JSONResponse:        jsonResponse,
			MaxRequestBodyBytes: maxBody,
		}),
		perRequest: mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
			Stateless:           true,
			JSONResponse:        jsonResponse,
			MaxRequestBodyBytes: maxBody,
		}),
		maxBody: peek,
	}
}

// ServeHTTP routes POST by its body and headers; GET and DELETE exist
// only for sessions and go to the session handler.
func (rr *revisionRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost || req.Body == nil {
		rr.sessions.ServeHTTP(w, req)
		return
	}
	// Read at most one byte past the SDK's own limit: enough to
	// classify any body the SDK would accept. The body is then
	// restored whole — what was read followed by the unread rest — so
	// the chosen handler applies its own limit and parse errors.
	head, _ := io.ReadAll(io.LimitReader(req.Body, rr.maxBody+1))
	req.Body = readCloser{io.MultiReader(bytes.NewReader(head), req.Body), req.Body}

	if perRequestRevision(req.Header, head) {
		rr.perRequest.ServeHTTP(w, req)
		return
	}
	rr.sessions.ServeHTTP(w, req)
}

type readCloser struct {
	io.Reader
	io.Closer
}

// perRequestRevision reports whether a POST speaks protocol
// 2026-07-28. The rules are the routing precedence every kit MCP
// surface applies, first match wins:
//
//   - a body that is not one JSON-RPC object (unparseable, or a batch)
//     is a session request;
//   - method "initialize" is a session request, whatever else it
//     carries;
//   - any modern marker makes it per-request: method "server/discover",
//     an Mcp-Method or Mcp-Name header, or the reserved
//     io.modelcontextprotocol/protocolVersion key in params._meta;
//   - anything else is a session request.
//
// The MCP-Protocol-Version header alone is deliberately not a marker,
// and neither is a params._meta without the reserved key.
func perRequestRevision(h http.Header, body []byte) bool {
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if json.Unmarshal(body, &msg) != nil {
		return false
	}
	switch {
	case msg.Method == "initialize":
		return false
	case msg.Method == "server/discover":
		return true
	case h.Get(headerMCPMethod) != "", h.Get(headerMCPName) != "":
		return true
	}
	return hasProtocolVersionMeta(msg.Params)
}

// hasProtocolVersionMeta reports whether raw params carry a _meta
// object holding the reserved protocolVersion key. Only presence is
// tested; the SDK validates the value.
func hasProtocolVersionMeta(raw json.RawMessage) bool {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return false
	}
	_, ok := p.Meta[metaKeyProtocolVersion]
	return ok
}
