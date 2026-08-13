package mcpsdk

import (
	"context"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// Default identity for the MCP server, mirroring the hand-rolled
// surface's defaults so adopters switching implementations see a
// familiar shape.
const (
	defaultServerName    = "mcpsdk"
	defaultServerVersion = "0.0.0"
	defaultPath          = "/mcp"
)

// config is the internal options bag set by Option funcs.
type config struct {
	path          string
	serverName    string
	serverVersion string
	instructions  string
	stateless     bool
	jsonResponse  bool
}

// Option configures the surface built by NewServer / Handler / Mount.
type Option func(*config)

// WithPath overrides the default mount path ("/mcp"). Only Mount
// consults it; Handler returns a path-agnostic http.Handler.
func WithPath(path string) Option {
	return func(c *config) { c.path = path }
}

// WithServerInfo sets the server identity advertised during
// initialization. Defaults: name="mcpsdk", version="0.0.0".
func WithServerInfo(name, version string) Option {
	return func(c *config) {
		c.serverName = name
		c.serverVersion = version
	}
}

// WithInstructions sets the optional instructions string offered to
// connecting clients during initialization.
func WithInstructions(text string) Option {
	return func(c *config) { c.instructions = text }
}

// WithStateless serves the streamable HTTP transport in stateless
// mode: no Mcp-Session-Id header, a temporary session per request,
// and GET/DELETE rejected with 405. Suitable for serverless and
// load-balanced deployments where session affinity is unavailable.
func WithStateless() Option {
	return func(c *config) { c.stateless = true }
}

// WithJSONResponse makes streamable HTTP responses use
// application/json bodies instead of text/event-stream.
func WithJSONResponse() Option {
	return func(c *config) { c.jsonResponse = true }
}

func newConfig(opts ...Option) config {
	cfg := config{
		path:          defaultPath,
		serverName:    defaultServerName,
		serverVersion: defaultServerVersion,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// NewServer builds an *mcp.Server bound to the bridge: one MCP tool
// per leaf where SurfaceMCP is enabled at call time. The returned
// server is transport-agnostic — run it on any SDK transport
// (streamable HTTP via Handler/Mount, stdio via ServeStdio, or a
// custom transport via server.Run / server.Connect).
//
// Tool set is fixed at construction: leaves exposed on SurfaceMCP
// when NewServer runs become tools. Enablement is still re-checked
// on every call by Bridge.Invoke, so hiding a leaf after mount makes
// its tool fail; it does not unlist it.
func NewServer(b *cmdsurface.Bridge, opts ...Option) (*mcp.Server, error) {
	if b == nil {
		return nil, errors.New("mcpsdk: nil bridge")
	}
	cfg := newConfig(opts...)
	srv := mcp.NewServer(
		&mcp.Implementation{Name: cfg.serverName, Version: cfg.serverVersion},
		&mcp.ServerOptions{Instructions: cfg.instructions},
	)
	for _, leaf := range b.Leaves() {
		if !leaf.Enabled[cmdsurface.SurfaceMCP] {
			continue
		}
		srv.AddTool(toolFor(leaf), toolHandler(b, leaf))
	}
	return srv, nil
}

// Handler returns an http.Handler serving the bridge over the MCP
// streamable HTTP transport (stateful sessions by default; see
// WithStateless). All protocol handling — version negotiation,
// session lifecycle, message parsing, error shapes — is the SDK's.
func Handler(b *cmdsurface.Bridge, opts ...Option) (http.Handler, error) {
	srv, err := NewServer(b, opts...)
	if err != nil {
		return nil, err
	}
	cfg := newConfig(opts...)
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{
			Stateless:    cfg.stateless,
			JSONResponse: cfg.jsonResponse,
		},
	), nil
}

// Mount registers the streamable HTTP handler on the router at the
// configured path (default "/mcp") for the POST, GET, and DELETE
// methods the transport uses. Unsupported methods are rejected by
// the SDK handler itself.
func Mount(b *cmdsurface.Bridge, r *api.Router, opts ...Option) error {
	if r == nil {
		return errors.New("mcpsdk: Mount: nil router")
	}
	h, err := Handler(b, opts...)
	if err != nil {
		return err
	}
	cfg := newConfig(opts...)
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		r.Handle(method, cfg.path, h.ServeHTTP)
	}
	return nil
}

// ServeStdio runs the bridge-bound server on the stdio transport
// until ctx is canceled or the client disconnects. Note that stdio
// carries no HTTP headers, so leaves classified auth-required or
// requires-confirmation are never callable on this transport — the
// header-based gates fail closed.
func ServeStdio(ctx context.Context, b *cmdsurface.Bridge, opts ...Option) error {
	srv, err := NewServer(b, opts...)
	if err != nil {
		return err
	}
	return srv.Run(ctx, &mcp.StdioTransport{})
}
