package mcpsdk

import (
	"context"
	"errors"
	"net/http"
	"sync"

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
	serverOptions *mcp.ServerOptions
	configurators []func(*mcp.Server)
	toolDecorator func(*cmdsurface.Leaf, *mcp.Tool)
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

// WithServerOptions supplies the base *mcp.ServerOptions passed to
// the SDK verbatim — the full pass-through for everything this
// package does not manage itself: PageSize, SubscribeHandler /
// UnsubscribeHandler, CompletionHandler, Capabilities, KeepAlive,
// GetSessionID, and so on. A shallow copy is taken, and
// WithInstructions (when given) overrides the copy's Instructions
// field; every other field reaches mcp.NewServer untouched.
func WithServerOptions(o *mcp.ServerOptions) Option {
	return func(c *config) { c.serverOptions = o }
}

// WithServerConfigurator registers fn to run against the built
// *mcp.Server after kit's tools are bound. This is the adopter
// hook for the rest of the SDK's feature surface — AddPrompt,
// AddResource, AddResourceTemplate, custom methods — using the
// SDK's own APIs directly; kit wraps none of them. Capability
// advertisement follows automatically from what fn registers (SDK
// behavior). Repeatable; configurators run in registration order.
func WithServerConfigurator(fn func(*mcp.Server)) Option {
	return func(c *config) {
		if fn != nil {
			c.configurators = append(c.configurators, fn)
		}
	}
}

// WithToolDecorator registers fn to enrich each bound tool's
// descriptor before registration: title, annotations, output
// schema, icons — any optional mcp.Tool field. fn runs after kit
// populates the defaults (name, description, input schema,
// destructive hint) and may override them. Note kit cannot derive
// an outputSchema mechanically: bridge Results carry untyped Data,
// so output schemas are adopter knowledge and belong here.
func WithToolDecorator(fn func(*cmdsurface.Leaf, *mcp.Tool)) Option {
	return func(c *config) { c.toolDecorator = fn }
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

// Surface is a live binding between a bridge and an *mcp.Server. It
// tracks which leaves are currently registered as tools so that
// runtime enablement changes (Hide / Expose / Sync) translate into
// SDK tool add/remove calls, which in turn make connected sessions
// receive tools/list_changed notifications (SDK behavior).
type Surface struct {
	b   *cmdsurface.Bridge
	cfg config
	srv *mcp.Server

	mu         sync.Mutex
	registered map[string]bool // dotted tool name -> currently added
}

// New builds a Surface: one MCP tool per leaf where SurfaceMCP is
// enabled, then any WithServerConfigurator funcs against the built
// server. The returned Surface serves via Handler / Mount /
// ServeStdio, or adopters take Server() and wire any SDK transport
// themselves.
func New(b *cmdsurface.Bridge, opts ...Option) (*Surface, error) {
	if b == nil {
		return nil, errors.New("mcpsdk: nil bridge")
	}
	cfg := newConfig(opts...)

	so := &mcp.ServerOptions{}
	if cfg.serverOptions != nil {
		cp := *cfg.serverOptions
		so = &cp
	}
	if cfg.instructions != "" {
		so.Instructions = cfg.instructions
	}

	s := &Surface{
		b:   b,
		cfg: cfg,
		srv: mcp.NewServer(
			&mcp.Implementation{Name: cfg.serverName, Version: cfg.serverVersion},
			so,
		),
		registered: make(map[string]bool),
	}
	s.Sync()
	for _, fn := range cfg.configurators {
		fn(s.srv)
	}
	return s, nil
}

// Server returns the underlying *mcp.Server for direct SDK use
// (registering prompts/resources after construction, custom
// transports, ResourceUpdated notifications, ...).
func (s *Surface) Server() *mcp.Server { return s.srv }

// Sync reconciles the SDK tool set with the bridge's current
// SurfaceMCP enablement: newly enabled leaves are added, disabled
// ones removed. Connected sessions receive tools/list_changed for
// every effective change (SDK behavior). Call it after mutating
// enablement directly on the bridge; the Surface's own Hide /
// Expose call it automatically.
func (s *Surface) Sync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, leaf := range s.b.Leaves() {
		name := toolName(leaf.Path)
		enabled := leaf.Enabled[cmdsurface.SurfaceMCP]
		switch {
		case enabled && !s.registered[name]:
			s.srv.AddTool(s.toolFor(leaf), toolHandler(s.b, leaf))
			s.registered[name] = true
		case !enabled && s.registered[name]:
			s.srv.RemoveTools(name)
			delete(s.registered, name)
		}
	}
}

// toolFor builds the leaf's descriptor and applies the configured
// decorator, if any.
func (s *Surface) toolFor(leaf *cmdsurface.Leaf) *mcp.Tool {
	t := toolFor(leaf)
	if s.cfg.toolDecorator != nil {
		s.cfg.toolDecorator(leaf, t)
	}
	return t
}

// Expose enables SurfaceMCP on every leaf matching pattern (see
// cmdsurface.Bridge.Expose for pattern forms) and syncs the SDK
// tool set. Returns the receiver for chaining.
func (s *Surface) Expose(pattern string) *Surface {
	s.b.Expose(pattern, cmdsurface.SurfaceMCP)
	s.Sync()
	return s
}

// Hide disables SurfaceMCP on every leaf matching pattern and syncs
// the SDK tool set, unlisting the tools and notifying connected
// sessions. Returns the receiver for chaining.
func (s *Surface) Hide(pattern string) *Surface {
	s.b.Hide(pattern, cmdsurface.SurfaceMCP)
	s.Sync()
	return s
}

// Handler returns an http.Handler serving the Surface over the MCP
// streamable HTTP transport (stateful sessions by default; see
// WithStateless). All protocol handling — version negotiation,
// session lifecycle, message parsing, error shapes — is the SDK's.
func (s *Surface) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s.srv },
		&mcp.StreamableHTTPOptions{
			Stateless:    s.cfg.stateless,
			JSONResponse: s.cfg.jsonResponse,
		},
	)
}

// Mount registers the streamable HTTP handler on the router at the
// configured path (default "/mcp") for the POST, GET, and DELETE
// methods the transport uses. Unsupported methods are rejected by
// the SDK handler itself.
func (s *Surface) Mount(r *api.Router) error {
	if r == nil {
		return errors.New("mcpsdk: Mount: nil router")
	}
	h := s.Handler()
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		r.Handle(method, s.cfg.path, h.ServeHTTP)
	}
	return nil
}

// ServeStdio runs the Surface on the stdio transport until ctx is
// canceled or the client disconnects. Note that stdio carries no
// HTTP headers, so leaves classified auth-required or
// requires-confirmation are never callable on this transport — the
// header-based gates fail closed.
func (s *Surface) ServeStdio(ctx context.Context) error {
	return s.srv.Run(ctx, &mcp.StdioTransport{})
}

// NewServer builds an *mcp.Server bound to the bridge: one MCP tool
// per leaf where SurfaceMCP is enabled. Convenience for callers that
// only need the server; New returns the Surface handle with live
// Hide / Expose / Sync on top.
func NewServer(b *cmdsurface.Bridge, opts ...Option) (*mcp.Server, error) {
	s, err := New(b, opts...)
	if err != nil {
		return nil, err
	}
	return s.srv, nil
}

// Handler is the package-level convenience for New(...).Handler().
func Handler(b *cmdsurface.Bridge, opts ...Option) (http.Handler, error) {
	s, err := New(b, opts...)
	if err != nil {
		return nil, err
	}
	return s.Handler(), nil
}

// Mount is the package-level convenience for New(...).Mount(r).
func Mount(b *cmdsurface.Bridge, r *api.Router, opts ...Option) error {
	if r == nil {
		return errors.New("mcpsdk: Mount: nil router")
	}
	s, err := New(b, opts...)
	if err != nil {
		return err
	}
	return s.Mount(r)
}

// ServeStdio is the package-level convenience for
// New(...).ServeStdio(ctx).
func ServeStdio(ctx context.Context, b *cmdsurface.Bridge, opts ...Option) error {
	s, err := New(b, opts...)
	if err != nil {
		return err
	}
	return s.ServeStdio(ctx)
}
