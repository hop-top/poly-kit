package mcpserve

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
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/mcpsdk"
	"hop.top/kit/go/transport/transportsvc"
)

// ServiceName is the identifier the MCP service registers under. Like
// [cli.APIServiceName] it is a CLI word, a config key segment
// (services.mcp.*), and a bus payload value at once, so it is stable
// across releases.
const ServiceName = "mcp"

// Transports the service speaks, the values of services.mcp.transport
// and Config.Transport.
const (
	// TransportHTTP serves streamable HTTP on the service's own
	// listener. It is the default.
	TransportHTTP = "http"
	// TransportStdio serves the process's standard input and output,
	// for a host that spawns `<tool> serve mcp --stdio`.
	TransportStdio = "stdio"
)

// DefaultAddr is the HTTP listen address when neither Config.Addr,
// services.mcp.addr, nor --mcp-addr sets one. It is a loopback address
// for the same reason [cli.DefaultAPIAddr] is.
const DefaultAddr = "127.0.0.1:8081"

// DefaultPath is the HTTP endpoint path when neither Config.Path nor
// services.mcp.path sets one.
const DefaultPath = "/mcp"

// Service-owned config keys and flags.
const (
	keyPrefix              = "services." + ServiceName
	subkeyTransport        = ".transport"
	subkeyAddr             = ".addr"
	subkeyPath             = ".path"
	subkeyInsecureRemote   = ".insecure_remote"
	subkeyInsecureNoPolicy = ".insecure_no_policy"

	// stdioFlag selects the stdio transport for one run.
	stdioFlag = "stdio"
	// addrFlag overrides services.mcp.addr for one run.
	addrFlag = "mcp-addr"
)

// Config configures the MCP service.
//
// The service follows docs/contracts/serve-lifecycle.md §"The mcp
// service": everything the api and socket services get — the root
// factory, replayed operator flags, Expose/Hide/Policy, the permission
// gate, audit sinks, readiness and exit codes — pinned to the mcp
// surface, with the protocol carried by the official MCP Go SDK.
type Config struct {
	// Transport is TransportHTTP (the default) or TransportStdio.
	// services.mcp.transport overrides it, and --stdio overrides that.
	Transport string

	// Addr is the HTTP listen address (default [DefaultAddr], a
	// loopback address). services.mcp.addr overrides it, and
	// --mcp-addr overrides that. A non-loopback address is refused at
	// validation unless Auth is set or InsecureRemote opts in, and
	// refused again unless a --policy is in force or InsecureNoPolicy
	// opts in — the api service's rules, under this service's names.
	Addr string

	// Path is the HTTP endpoint path (default [DefaultPath]).
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

// With returns a Root option registering the `mcp` service: the
// tool's command tree served as MCP tools, one per invocable leaf,
// over streamable HTTP or stdio.
//
// It lives in its own package so that only a tool that serves MCP
// links the MCP SDK; go/console/cli itself does not depend on it.
//
// Like [cli.WithSocket], the service is NOT enabled by default. Start
// it with `<tool> serve mcp` (HTTP) or `<tool> serve mcp --stdio`, or
// set services.mcp.enabled.
func With(cfg Config) func(*cli.Root) {
	return with(cfg, nil)
}

// with is With with the stdio streams overridable, for tests.
func with(cfg Config, stdio *stdioStreams) func(*cli.Root) {
	return func(r *cli.Root) {
		cli.WithService(newService(r, &cfg, stdio))(r)
		mountServeFlags(r)
	}
}

// serveCmd returns the kit-owned serve parent, or nil before one is
// mounted.
func serveCmd(r *cli.Root) *cobra.Command {
	if r == nil || r.Cmd == nil {
		return nil
	}
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "serve" {
			return c
		}
	}
	return nil
}

// mountServeFlags puts --stdio and --mcp-addr on the serve parent, the
// way --socket reaches the socket service. They are inert unless the
// mcp service is the one running.
func mountServeFlags(r *cli.Root) {
	c := serveCmd(r)
	if c == nil {
		return
	}
	if c.Flags().Lookup(stdioFlag) == nil {
		c.Flags().Bool(stdioFlag, false,
			"Serve the mcp service over standard input and output")
	}
	if c.Flags().Lookup(addrFlag) == nil {
		c.Flags().String(addrFlag, "",
			"Listen address for the mcp service's HTTP transport")
	}
}

// service is the transport service with a class that depends on the
// transport resolved for the run: a listener over HTTP, no network at
// all over stdio.
type service struct {
	*transportsvc.TransportService
	root  *cli.Root
	cfg   *Config
	stdio *stdioStreams
}

// Class implements [serve.Classified].
func (s *service) Class() (sideEffect, network string) {
	if s.transport() == TransportStdio {
		return string(cli.SideEffectWriteShared), "none"
	}
	return string(cli.SideEffectWriteShared), "listen"
}

var (
	_ serve.Service    = (*service)(nil)
	_ serve.Validator  = (*service)(nil)
	_ serve.Addressed  = (*service)(nil)
	_ serve.Classified = (*service)(nil)
)

// newService builds the service on the transport seam, with the
// bridge wiring every kit-shipped service gets: the adopter's Policy,
// then [cli.ServeBridgeOptions] — the root factory's runner, the
// shared permission gate and audit sinks — resolved at Start.
func newService(root *cli.Root, cfg *Config, stdio *stdioStreams) *service {
	s := &service{root: root, cfg: cfg, stdio: stdio}
	tr := &transport{svc: s}

	opts := []transportsvc.TransportOption{
		transportsvc.Expose("*"),
		transportsvc.WithBridgeOptions(cmdsurface.WithPolicy(cfg.Policy)),
		transportsvc.WithBridgeOptionsFunc(func() []cmdsurface.Option {
			shared, err := cli.ServeBridgeOptions(root)
			if err != nil {
				// Validate has already refused a --policy that cannot
				// load, so this path is unreachable in practice.
				return nil
			}
			return shared
		}),
		transportsvc.WithValidate(s.validate),
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

	s.TransportService = transportsvc.NewTransportService(
		ServiceName, root.Cmd, cmdsurface.SurfaceMCP, tr, opts...,
	)
	return s
}

// flag returns this run's value of a serve flag and whether the
// operator set it. Flags are read from the parsed serve command at
// use, so a re-executed root never inherits a previous run's value.
func (s *service) flag(name string) (string, bool) {
	c := serveCmd(s.root)
	if c == nil {
		return "", false
	}
	f := c.Flags().Lookup(name)
	if f == nil || !f.Changed {
		return "", false
	}
	return f.Value.String(), true
}

// transport resolves the transport for this run: --stdio, then
// services.mcp.transport, then Config.Transport, then http.
func (s *service) transport() string {
	if v, set := s.flag(stdioFlag); set && v == "true" {
		return TransportStdio
	}
	if s.root.Viper != nil {
		if v := s.root.Viper.GetString(keyPrefix + subkeyTransport); v != "" {
			return strings.ToLower(strings.TrimSpace(v))
		}
	}
	if s.cfg.Transport != "" {
		return strings.ToLower(strings.TrimSpace(s.cfg.Transport))
	}
	return TransportHTTP
}

// addr resolves the HTTP listen address: --mcp-addr, then
// services.mcp.addr, then Config.Addr, then [DefaultAddr].
func (s *service) addr() string {
	if v, set := s.flag(addrFlag); set && v != "" {
		return v
	}
	if s.root.Viper != nil {
		if v := s.root.Viper.GetString(keyPrefix + subkeyAddr); v != "" {
			return v
		}
	}
	if s.cfg.Addr != "" {
		return s.cfg.Addr
	}
	return DefaultAddr
}

// path resolves the HTTP endpoint path: services.mcp.path, then
// Config.Path, then [DefaultPath].
func (s *service) path() string {
	if s.root.Viper != nil {
		if v := s.root.Viper.GetString(keyPrefix + subkeyPath); v != "" {
			return v
		}
	}
	if s.cfg.Path != "" {
		return s.cfg.Path
	}
	return DefaultPath
}

// optIn resolves a boolean opt-in: the config key when set, else the
// code value. There is no flag layer.
func (s *service) optIn(subkey string, code bool) bool {
	if s.root.Viper != nil {
		key := keyPrefix + subkey
		if s.root.Viper.IsSet(key) {
			return s.root.Viper.GetBool(key)
		}
	}
	return code
}

// validate is the service's configuration gate (serve-lifecycle.md §"The
// override rule"): every refusal here is a usage error at exit 2,
// before anything binds.
func (s *service) validate() error {
	switch transport := s.transport(); transport {
	case TransportStdio:
		if _, set := s.flag(addrFlag); set {
			return errors.New(
				"addr: --mcp-addr applies to the http transport, and --stdio selects stdio; drop one of them",
			)
		}
	case TransportHTTP:
		if err := s.validateHTTP(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("transport: unknown transport %q; use %q or %q",
			transport, TransportHTTP, TransportStdio)
	}
	return cli.ValidateServeBridge(s.root)
}

// validateHTTP checks the address and path, then applies the api
// service's two exposure refusals under this service's names.
func (s *service) validateHTTP() error {
	addr := s.addr()
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("addr: %w", err)
	}
	if p := s.path(); !strings.HasPrefix(p, "/") {
		return fmt.Errorf("path: %q must begin with \"/\"", p)
	}
	if cli.IsLoopbackAddr(addr) {
		return nil
	}
	if s.cfg.Auth == nil && !s.optIn(subkeyInsecureRemote, s.cfg.InsecureRemote) {
		return fmt.Errorf(
			"addr: %q is not a loopback address and the mcp service has no authentication; "+
				"set mcpserve.Config.Auth, listen on 127.0.0.1, or set services.mcp.insecure_remote: true "+
				"to serve unauthenticated beyond loopback",
			addr,
		)
	}
	if !cli.ServePolicyConfigured(s.root) && !s.optIn(subkeyInsecureNoPolicy, s.cfg.InsecureNoPolicy) {
		return fmt.Errorf(
			"addr: %q is not a loopback address and no delegation policy is configured; "+
				"set --policy, listen on 127.0.0.1, or set services.mcp.insecure_no_policy: true "+
				"to serve every command beyond loopback",
			addr,
		)
	}
	return nil
}

// serving is one transport's half of a run: acquire, serve a surface,
// release.
type serving interface {
	bind(ctx context.Context) (addr string, err error)
	serve(ctx context.Context, s *mcpsdk.Surface) error
	close(ctx context.Context) error
}

// transport is the [transportsvc.Transport] behind the service. It
// resolves the transport at Bind, so flags parsed after construction
// win, and builds the SDK surface at Serve over the bridge the seam
// reflected at Start.
type transport struct {
	svc *service

	mu   sync.Mutex
	impl serving
}

func (t *transport) Bind(ctx context.Context) (string, error) {
	var impl serving
	switch t.svc.transport() {
	case TransportStdio:
		impl = newStdio(t.svc.stdio)
	default:
		impl = newHTTP(t.svc)
	}
	addr, err := impl.bind(ctx)
	if err != nil {
		return "", err
	}
	t.mu.Lock()
	t.impl = impl
	t.mu.Unlock()
	return addr, nil
}

func (t *transport) Serve(ctx context.Context, _ transportsvc.Invoker) error {
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

func (t *transport) Close(ctx context.Context) error {
	t.mu.Lock()
	impl := t.impl
	t.mu.Unlock()
	if impl == nil {
		return nil
	}
	return impl.close(ctx)
}

// surface builds the SDK surface over the service's bridge. Every
// call is admitted by Bridge.Admit before it runs, so the policy,
// invocability and permission gates the seam promises hold on both
// transports.
func (t *transport) surface(ctx context.Context, impl serving) (*mcpsdk.Surface, error) {
	b := t.svc.Bridge()
	if b == nil {
		return nil, errors.New("mcp: no bridge; the service has not started")
	}
	withholdFromCatalog(ctx, b, t.svc.root)

	cfg := t.svc.cfg
	opts := append([]mcpsdk.Option(nil), cfg.ServerOptions...)
	name, version := t.svc.root.Config.Name, t.svc.root.Config.Version
	if name == "" {
		name = "kit"
	}
	if version == "" {
		version = "0.0.0"
	}
	opts = append(opts, mcpsdk.WithServerInfo(name, version))
	if cfg.Instructions != "" {
		opts = append(opts, mcpsdk.WithInstructions(cfg.Instructions))
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

// withholdFromCatalog takes off the tool list exactly what the REST
// projection withholds at mount: every command the reflector judges
// non-invocable for a served surface — interactive, management-only,
// self-hosting — under the same reflection REST uses (the Root's
// reserved verbs, no Allow* options), then destructive leaves the
// policy refuses on mcp, and leaves the permission gate refuses for
// every caller. The list is advisory — a call still meets every gate —
// but a model reads it before it calls anything, and a tool that can
// only ever refuse is noise it will act on.
//
// The bridge itself reflects more permissively (it describes reserved
// verbs as leaves, the way the socket service reaches them); the
// catalog is where the MCP surface narrows to REST's set.
func withholdFromCatalog(ctx context.Context, b *cmdsurface.Bridge, root *cli.Root) {
	served := cmdreflect.Reflect(root.Cmd, cmdreflect.WithReserved(root))
	for _, leaf := range b.Leaves() {
		if !leaf.Enabled[cmdsurface.SurfaceMCP] {
			continue
		}
		if catalogWithholds(ctx, b, served, leaf) {
			b.Hide(leaf.PathKey(), cmdsurface.SurfaceMCP)
		}
	}
}

func catalogWithholds(ctx context.Context, b *cmdsurface.Bridge, served *cmdreflect.Tree, leaf *cmdsurface.Leaf) bool {
	if d := served.Lookup(leaf.PathKey()); d == nil || !d.Invocable {
		return true
	}
	if !b.Policy().Allowed(leaf.Class, cmdsurface.SurfaceMCP) {
		return true
	}
	dec := b.Permission(ctx, cmdsurface.Meta{Surface: cmdsurface.SurfaceMCP}, leaf)
	return !dec.Allowed && dec.CallerIndependent
}

// clientName is the client's self-reported name from the MCP
// handshake: provenance, never a credential.
func clientName(req *mcp.CallToolRequest) string {
	if req == nil || req.Session == nil {
		return ""
	}
	p := req.Session.InitializeParams()
	if p == nil || p.ClientInfo == nil {
		return ""
	}
	return p.ClientInfo.Name
}
