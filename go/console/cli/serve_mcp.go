package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/mcpsdk"
	"hop.top/kit/go/transport/transportsvc"
)

// MCPServiceName is the identifier the built-in MCP service registers
// under. Like [APIServiceName] it is a CLI word, a config key segment
// (services.mcp.*), and a bus payload value at once, so it is stable
// across releases.
const MCPServiceName = "mcp"

// Transports the mcp service speaks, the values of
// services.mcp.transport and MCPConfig.Transport.
const (
	// MCPTransportHTTP serves streamable HTTP on the service's own
	// listener. It is the default.
	MCPTransportHTTP = "http"
	// MCPTransportStdio serves the process's standard input and
	// output, for a host that spawns `<tool> serve mcp --stdio`.
	MCPTransportStdio = "stdio"
)

// DefaultMCPAddr is the mcp service's HTTP listen address when neither
// MCPConfig.Addr, services.mcp.addr, nor --mcp-addr sets one. It is a
// loopback address for the same reason [DefaultAPIAddr] is.
const DefaultMCPAddr = "127.0.0.1:8081"

// DefaultMCPPath is the HTTP endpoint path when neither MCPConfig.Path
// nor services.mcp.path sets one.
const DefaultMCPPath = "/mcp"

// Service-owned config keys and flags for the mcp service.
const (
	mcpSubkeyTransport        = ".transport"
	mcpSubkeyAddr             = ".addr"
	mcpSubkeyPath             = ".path"
	mcpSubkeyInsecureRemote   = ".insecure_remote"
	mcpSubkeyInsecureNoPolicy = ".insecure_no_policy"

	// mcpStdioFlag selects the stdio transport for one run.
	mcpStdioFlag = "stdio"
	// mcpAddrFlag overrides services.mcp.addr for one run.
	mcpAddrFlag = "mcp-addr"
)

// MCPConfig configures the built-in `mcp` service.
//
// The service follows docs/contracts/serve-lifecycle.md §"The mcp
// service": everything the api and socket services get — the root
// factory, replayed operator flags, Expose/Hide/Policy, the permission
// gate, audit sinks, readiness and exit codes — pinned to the mcp
// surface, with the protocol carried by the official MCP Go SDK.
type MCPConfig struct {
	// Transport is MCPTransportHTTP (the default) or
	// MCPTransportStdio. services.mcp.transport overrides it, and
	// --stdio overrides that.
	Transport string

	// Addr is the HTTP listen address (default [DefaultMCPAddr], a
	// loopback address). services.mcp.addr overrides it, and
	// --mcp-addr overrides that. A non-loopback address is refused at
	// validation unless Auth is set or InsecureRemote opts in, and
	// refused again unless a --policy is in force or InsecureNoPolicy
	// opts in — the api service's rules, under this service's names.
	Addr string

	// Path is the HTTP endpoint path (default [DefaultMCPPath]).
	// services.mcp.path overrides it. It must begin with "/".
	Path string

	// Auth authenticates every HTTP request before the MCP layer sees
	// it; see [api.AuthFunc]. Its claims attribute each call, and it
	// is what admits a leaf declaring kit/auth-required over HTTP: a
	// bare Authorization header is not authentication. It does not
	// apply to stdio, whose peer is the process that spawned the
	// service.
	Auth api.AuthFunc

	// InsecureRemote permits serving HTTP WITHOUT authentication on a
	// non-loopback address. services.mcp.insecure_remote sets the
	// same thing. There is no flag: the --insecure-remote flag names
	// the api service.
	InsecureRemote bool

	// InsecureNoPolicy permits serving HTTP on a non-loopback address
	// with NO delegation policy in force.
	// services.mcp.insecure_no_policy sets the same thing.
	InsecureNoPolicy bool

	// Expose lists the command patterns the service may invoke, in
	// the pattern language of [cmdsurface.Bridge.Expose]. Empty
	// exposes the whole tree; the destructive ceiling still applies
	// on top.
	Expose []string

	// Hide carves exceptions out of Expose, applied after it.
	Hide []string

	// Policy gates which commands the service may invoke. The zero
	// value withholds every destructive command. To permit them over
	// MCP, name the surface:
	//
	//	Policy: cmdsurface.Policy{
	//		AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceMCP},
	//	}
	//
	// Permitting them does not skip the command's own confirmation:
	// the call still carries its `confirm` argument.
	Policy cmdsurface.Policy

	// Instructions is offered to connecting clients during
	// initialization.
	Instructions string

	// ServerOptions are passed to [mcpsdk.New] before the options the
	// service sets itself — server identity, provenance, the auth
	// verdict, elicited confirmation — so those always win. They are
	// the hook for prompts, resources and the rest of the SDK surface
	// (mcpsdk.WithServerConfigurator); what they register runs outside
	// kit's gates, as the mcpsdk package documents.
	ServerOptions []mcpsdk.Option
}

// WithMCP returns a Root option registering the built-in `mcp`
// service: the tool's command tree served as MCP tools, one per
// invocable leaf, over streamable HTTP or stdio.
//
// Like [WithSocket], the service is NOT enabled by default. Start it
// with `<tool> serve mcp` (HTTP) or `<tool> serve mcp --stdio`, or set
// services.mcp.enabled.
func WithMCP(cfg MCPConfig) func(*Root) {
	return func(r *Root) {
		r.ensureServeRegistry()
		r.mcpCfg = &cfg
		r.serveReg.Register(newMCPService(r, &cfg))
		r.mountMCPServeFlags()
	}
}

// mountMCPServeFlags puts --stdio and --mcp-addr on the serve parent,
// the way --socket reaches the socket service. They are inert unless
// the mcp service is the one running.
func (r *Root) mountMCPServeFlags() {
	for _, c := range r.Cmd.Commands() {
		if c.Name() != "serve" {
			continue
		}
		if c.Flags().Lookup(mcpStdioFlag) == nil {
			c.Flags().Bool(mcpStdioFlag, false,
				"Serve the mcp service over standard input and output")
		}
		if c.Flags().Lookup(mcpAddrFlag) == nil {
			c.Flags().String(mcpAddrFlag, "",
				"Listen address for the mcp service's HTTP transport")
		}
		return
	}
}

// applyMCPFlags records this run's --stdio and --mcp-addr. Both are
// set from scratch every run, so a re-executed root never inherits a
// previous run's transport.
func applyMCPFlags(cmd *cobra.Command, root *Root) {
	if root.mcpCfg == nil {
		return
	}
	root.mcpStdioFlag = false
	root.mcpAddrFlag = ""
	if f := cmd.Flags().Lookup(mcpStdioFlag); f != nil && f.Changed {
		root.mcpStdioFlag, _ = cmd.Flags().GetBool(mcpStdioFlag)
	}
	if f := cmd.Flags().Lookup(mcpAddrFlag); f != nil && f.Changed {
		root.mcpAddrFlag, _ = cmd.Flags().GetString(mcpAddrFlag)
	}
}

// mcpService is the transport service with a class that depends on
// the transport resolved for the run: a listener over HTTP, no network
// at all over stdio.
type mcpService struct {
	*transportsvc.TransportService
	root *Root
	cfg  *MCPConfig
}

// Class implements [serve.Classified].
func (m *mcpService) Class() (sideEffect, network string) {
	if m.root.mcpTransport(m.cfg) == MCPTransportStdio {
		return string(SideEffectWriteShared), "none"
	}
	return string(SideEffectWriteShared), "listen"
}

var (
	_ serve.Service    = (*mcpService)(nil)
	_ serve.Validator  = (*mcpService)(nil)
	_ serve.Addressed  = (*mcpService)(nil)
	_ serve.Classified = (*mcpService)(nil)
)

// newMCPService builds the mcp service on the transport seam, with the
// socket service's bridge wiring: the adopter's Policy, the root
// factory's runner, the shared permission gate and audit sinks, all
// resolved at Start.
func newMCPService(root *Root, cfg *MCPConfig) *mcpService {
	tr := &mcpTransport{root: root, cfg: cfg}

	opts := []transportsvc.TransportOption{
		transportsvc.Expose("*"),
		transportsvc.WithBridgeOptions(cmdsurface.WithPolicy(cfg.Policy)),
		transportsvc.WithBridgeOptionsFunc(func() []cmdsurface.Option {
			shared, err := root.serveBridgeOptions()
			if err != nil {
				return nil
			}
			return append(root.serveRunnerOptions(), shared...)
		}),
		transportsvc.WithValidate(func() error { return root.validateMCP(cfg) }),
	}
	if len(cfg.Expose) > 0 {
		// A non-empty Expose narrows the whole-tree default: hide
		// everything, then expose what was named.
		opts = append(opts, transportsvc.Hide("*"))
	}
	for _, p := range cfg.Expose {
		opts = append(opts, transportsvc.Expose(p))
	}
	for _, p := range cfg.Hide {
		opts = append(opts, transportsvc.Hide(p))
	}

	svc := transportsvc.NewTransportService(
		MCPServiceName, root.Cmd, cmdsurface.SurfaceMCP, tr, opts...,
	)
	tr.svc = svc
	return &mcpService{TransportService: svc, root: root, cfg: cfg}
}

// mcpTransport resolves the transport for this run: --stdio, then
// services.mcp.transport, then MCPConfig.Transport, then http.
func (r *Root) mcpTransport(cfg *MCPConfig) string {
	if r.mcpStdioFlag {
		return MCPTransportStdio
	}
	if r.Viper != nil {
		if v := r.Viper.GetString(serveKeyPrefix + MCPServiceName + mcpSubkeyTransport); v != "" {
			return strings.ToLower(strings.TrimSpace(v))
		}
	}
	if cfg != nil && cfg.Transport != "" {
		return strings.ToLower(strings.TrimSpace(cfg.Transport))
	}
	return MCPTransportHTTP
}

// mcpAddr resolves the HTTP listen address: --mcp-addr, then
// services.mcp.addr, then MCPConfig.Addr, then [DefaultMCPAddr].
func (r *Root) mcpAddr(cfg *MCPConfig) string {
	if r.mcpAddrFlag != "" {
		return r.mcpAddrFlag
	}
	if r.Viper != nil {
		if v := r.Viper.GetString(serveKeyPrefix + MCPServiceName + mcpSubkeyAddr); v != "" {
			return v
		}
	}
	if cfg != nil && cfg.Addr != "" {
		return cfg.Addr
	}
	return DefaultMCPAddr
}

// mcpPath resolves the HTTP endpoint path: services.mcp.path, then
// MCPConfig.Path, then [DefaultMCPPath].
func (r *Root) mcpPath(cfg *MCPConfig) string {
	if r.Viper != nil {
		if v := r.Viper.GetString(serveKeyPrefix + MCPServiceName + mcpSubkeyPath); v != "" {
			return v
		}
	}
	if cfg != nil && cfg.Path != "" {
		return cfg.Path
	}
	return DefaultMCPPath
}

// mcpBool resolves a boolean opt-in: the config key when set, else the
// code value. There is no flag layer.
func (r *Root) mcpBool(subkey string, code bool) bool {
	if r.Viper != nil {
		key := serveKeyPrefix + MCPServiceName + subkey
		if r.Viper.IsSet(key) {
			return r.Viper.GetBool(key)
		}
	}
	return code
}

// validateMCP is the service's configuration gate (contract §"The
// override rule"): every refusal here is a usage error at exit 2,
// before anything binds.
func (r *Root) validateMCP(cfg *MCPConfig) error {
	switch transport := r.mcpTransport(cfg); transport {
	case MCPTransportHTTP:
		if err := r.validateMCPHTTP(cfg); err != nil {
			return err
		}
	default:
		return fmt.Errorf("transport: unknown transport %q; use %q or %q",
			transport, MCPTransportHTTP, MCPTransportStdio)
	}
	if _, err := r.servePermission(); err != nil {
		return err
	}
	return r.validateRootFactory()
}

// validateMCPHTTP checks the address and path, then applies the api
// service's two exposure refusals under this service's names.
func (r *Root) validateMCPHTTP(cfg *MCPConfig) error {
	addr := r.mcpAddr(cfg)
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("addr: %w", err)
	}
	if p := r.mcpPath(cfg); !strings.HasPrefix(p, "/") {
		return fmt.Errorf("path: %q must begin with \"/\"", p)
	}
	if isLoopbackAddr(addr) {
		return nil
	}
	if cfg.Auth == nil && !r.mcpBool(mcpSubkeyInsecureRemote, cfg.InsecureRemote) {
		return fmt.Errorf(
			"addr: %q is not a loopback address and the mcp service has no authentication; "+
				"set MCPConfig.Auth, listen on 127.0.0.1, or set services.mcp.insecure_remote: true "+
				"to serve unauthenticated beyond loopback",
			addr,
		)
	}
	if !r.servePolicyConfigured() && !r.mcpBool(mcpSubkeyInsecureNoPolicy, cfg.InsecureNoPolicy) {
		return fmt.Errorf(
			"addr: %q is not a loopback address and no delegation policy is configured; "+
				"set --policy, listen on 127.0.0.1, or set services.mcp.insecure_no_policy: true "+
				"to serve every command beyond loopback",
			addr,
		)
	}
	return nil
}

// mcpServing is one transport's half of a run: acquire, serve a
// surface, release.
type mcpServing interface {
	bind(ctx context.Context) (addr string, err error)
	serve(ctx context.Context, s *mcpsdk.Surface) error
	close(ctx context.Context) error
}

// mcpTransport is the [transportsvc.Transport] behind the service. It
// resolves the transport at Bind, so flags parsed after construction
// win, and builds the SDK surface at Serve over the bridge the seam
// reflected at Start.
type mcpTransport struct {
	root *Root
	cfg  *MCPConfig
	svc  *transportsvc.TransportService

	mu   sync.Mutex
	impl mcpServing
}

func (t *mcpTransport) Bind(ctx context.Context) (string, error) {
	var impl mcpServing = newMCPHTTP(t.root, t.cfg, t.svc)
	addr, err := impl.bind(ctx)
	if err != nil {
		return "", err
	}
	t.mu.Lock()
	t.impl = impl
	t.mu.Unlock()
	return addr, nil
}

func (t *mcpTransport) Serve(ctx context.Context, _ transportsvc.Invoker) error {
	t.mu.Lock()
	impl := t.impl
	t.mu.Unlock()
	if impl == nil {
		return errors.New("mcp: Serve called before Bind")
	}
	s, err := t.surface(ctx, impl)
	if err != nil {
		_ = impl.close(context.Background())
		return err
	}
	return impl.serve(ctx, s)
}

func (t *mcpTransport) Close(ctx context.Context) error {
	t.mu.Lock()
	impl := t.impl
	t.mu.Unlock()
	if impl == nil {
		return nil
	}
	return impl.close(ctx)
}

// surface builds the SDK surface over the service's bridge. Every
// call dispatches through Bridge.Invoke (or InvokeStream), so the
// policy, invocability and permission gates the seam promises hold on
// both transports.
func (t *mcpTransport) surface(ctx context.Context, impl mcpServing) (*mcpsdk.Surface, error) {
	b := t.svc.Bridge()
	if b == nil {
		return nil, errors.New("mcp: no bridge; the service has not started")
	}
	withholdFromCatalog(ctx, b)

	opts := append([]mcpsdk.Option(nil), t.cfg.ServerOptions...)
	name, version := t.root.Config.Name, t.root.Config.Version
	if name == "" {
		name = "kit"
	}
	if version == "" {
		version = "0.0.0"
	}
	opts = append(opts, mcpsdk.WithServerInfo(name, version))
	if t.cfg.Instructions != "" {
		opts = append(opts, mcpsdk.WithInstructions(t.cfg.Instructions))
	}
	gates, ok := impl.(interface {
		callMeta(context.Context, *mcp.CallToolRequest) cmdsurface.Meta
		authenticated(context.Context, *mcp.CallToolRequest) bool
	})
	if ok {
		opts = append(opts,
			mcpsdk.WithCallMeta(gates.callMeta),
			mcpsdk.WithAuthenticated(gates.authenticated),
		)
	}
	opts = append(opts, mcpsdk.WithConfirmationElicitation(nil))
	return mcpsdk.New(b, opts...)
}

// withholdFromCatalog takes off the tool list what the REST projection
// withholds at mount: interactive leaves, destructive leaves the
// policy refuses on mcp, and leaves the permission gate refuses for
// every caller. The list is advisory — a call still meets every gate —
// but a model reads it before it calls anything, and a tool that can
// only ever refuse is noise it will act on.
func withholdFromCatalog(ctx context.Context, b *cmdsurface.Bridge) {
	for _, leaf := range b.Leaves() {
		if !leaf.Enabled[cmdsurface.SurfaceMCP] {
			continue
		}
		if catalogWithholds(ctx, b, leaf) {
			b.Hide(leaf.PathKey(), cmdsurface.SurfaceMCP)
		}
	}
}

func catalogWithholds(ctx context.Context, b *cmdsurface.Bridge, leaf *cmdsurface.Leaf) bool {
	if leaf.Descriptor != nil && leaf.Descriptor.Safety.Tier == cmdreflect.TierInteractive {
		return true
	}
	if !b.Policy().Allowed(leaf.Class, cmdsurface.SurfaceMCP) {
		return true
	}
	dec := b.Permission(ctx, cmdsurface.Meta{Surface: cmdsurface.SurfaceMCP}, leaf)
	return !dec.Allowed && dec.CallerIndependent
}

// mcpClientName is the client's self-reported name from the MCP
// handshake: provenance, never a credential.
func mcpClientName(req *mcp.CallToolRequest) string {
	if req == nil || req.Session == nil {
		return ""
	}
	p := req.Session.InitializeParams()
	if p == nil || p.ClientInfo == nil {
		return ""
	}
	return p.ClientInfo.Name
}
