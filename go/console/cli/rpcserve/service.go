// Package rpcserve registers the kit-shipped `rpc` service: a kit CLI's
// command tree served as the published cmdsurface.v1.Commands service
// (contracts/proto/cmdsurface/v1/commands.proto) over Connect, gRPC and
// gRPC-Web, under the serve lifecycle and the gates every kit-shipped
// transport service shares.
//
// The normative specification is docs/contracts/serve-lifecycle.md
// §"The rpc service".
package rpcserve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/console/cli"
	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/rpc"
	"hop.top/kit/go/transport/transportsvc"
)

// ServiceName is the identifier the rpc service registers under. Like
// [cli.APIServiceName] it is a CLI word, a config key segment
// (services.rpc.*), and a bus payload value at once, so it is stable
// across releases.
const ServiceName = "rpc"

// DefaultAddr is the listen address when neither Config.Addr,
// services.rpc.addr, nor --rpc-addr sets one. It is a loopback address
// for the same reason [cli.DefaultAPIAddr] is.
const DefaultAddr = "127.0.0.1:8082"

// DefaultMaxBodyBytes bounds one request message when
// Config.MaxBodyBytes is zero: 4 MiB, the default receive limit of the
// gRPC implementations.
const DefaultMaxBodyBytes = 4 << 20

// Service-owned config keys and flags.
const (
	keyPrefix              = "services." + ServiceName
	subkeyAddr             = ".addr"
	subkeyInsecureRemote   = ".insecure_remote"
	subkeyInsecureNoPolicy = ".insecure_no_policy"
	// addrFlag overrides services.rpc.addr for one run.
	addrFlag = "rpc-addr"
)

// Config configures the `rpc` service added by [With]:
// the command tree served as the cmdsurface.v1.Commands service
// (contracts/proto/cmdsurface/v1/commands.proto) over Connect, gRPC
// and gRPC-Web on one port.
type Config struct {
	// Addr is the listen address (default [DefaultAddr], a
	// loopback address). services.rpc.addr overrides it, and
	// --rpc-addr overrides that. A non-loopback address is refused at
	// validation unless Auth is set or InsecureRemote opts in, and
	// refused again unless a --policy is in force or InsecureNoPolicy
	// opts in — the api service's rules, under this service's names.
	Addr string

	// Auth authenticates every call, unary and streaming, before it
	// reaches the command tree; see [api.AuthFunc]. It receives the
	// call's headers only (see [rpc.Authenticate]), so one AuthFunc
	// serves REST and RPC alike. Its claims attribute each call —
	// see [api.IdentityOf] — and it is what admits a leaf declaring
	// kit/auth-required: a bare Authorization header is not
	// authentication.
	Auth api.AuthFunc

	// InsecureRemote permits serving WITHOUT authentication on a
	// non-loopback address. services.rpc.insecure_remote sets the
	// same thing. There is no flag: --insecure-remote names the api
	// service.
	InsecureRemote bool

	// InsecureNoPolicy permits serving on a non-loopback address with
	// NO delegation policy in force. services.rpc.insecure_no_policy
	// sets the same thing.
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
	// RPC, name the surface:
	//
	//	Policy: cmdsurface.Policy{
	//		AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceRPC},
	//	}
	//
	// Permitting them does not skip confirmation: a command that
	// declares kit/requires-confirmation still needs the
	// X-Confirm-Token header on every call.
	Policy cmdsurface.Policy

	// MaxBodyBytes bounds one request message (default
	// [DefaultMaxBodyBytes]). A larger message is refused with
	// ResourceExhausted before it is decoded.
	MaxBodyBytes int

	// Interceptors are the adopter's Connect interceptors — metering,
	// quotas, tracing — run inside kit's gates: only once Auth, the
	// kit/auth-required and confirmation gates, exposure, the
	// destructive ceiling and the permission gate have admitted the
	// call, and around its run. A call kit refuses never reaches them
	// (kit audits it). A call one of them refuses does not run, and
	// is audited with its error. They apply in order, the first
	// outermost.
	//
	// The request they see is the body as the client sent it, so its
	// meta.caller is a claim: read identity from the verified claims,
	// [rpc.ClaimsFromContext] with [api.IdentityOf]. See
	// [cmdsurface.WithRPCAdmittedInterceptors].
	Interceptors []connect.Interceptor
}

// With returns a Root option registering the `rpc` service: the
// tool's command tree served as the published cmdsurface.v1.Commands
// service — Invoke (unary) and InvokeStream (server-streaming) — over
// Connect, gRPC and gRPC-Web on its own listener, HTTP/1.1 and h2c on
// one port.
//
// It lives in its own package so that only a tool that serves RPC
// links go/transport/rpc and the protobuf types it registers;
// go/console/cli itself does not depend on it.
//
// Like [cli.WithSocket], the service is NOT enabled by default. Start
// it with `<tool> serve rpc`, or set services.rpc.enabled.
func With(cfg Config) func(*cli.Root) {
	return with(cfg, nil)
}

// with is With with extra rpc server options, for tests.
func with(cfg Config, serverOpts []rpc.ServerOption) func(*cli.Root) {
	return func(r *cli.Root) {
		svc := newService(r, &cfg)
		svc.serverOpts = serverOpts
		cli.WithService(svc)(r)
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

// mountServeFlags puts --rpc-addr on the serve parent, the way
// --socket reaches the socket service. It is inert unless the rpc
// service is the one running.
func mountServeFlags(r *cli.Root) {
	if c := serveCmd(r); c != nil && c.Flags().Lookup(addrFlag) == nil {
		c.Flags().String(addrFlag, "", "Listen address for the rpc service")
	}
}

// rpcService is the transport service with the rpc service's
// configuration resolution.
type rpcService struct {
	*transportsvc.TransportService
	root *cli.Root
	cfg  *Config
	// serverOpts configure the rpc server. Not exposed: tests shorten
	// the write timeout with it.
	serverOpts []rpc.ServerOption
}

var (
	_ serve.Service    = (*rpcService)(nil)
	_ serve.Validator  = (*rpcService)(nil)
	_ serve.Addressed  = (*rpcService)(nil)
	_ serve.Classified = (*rpcService)(nil)
)

// newService builds the rpc service on the transport seam, with the
// bridge wiring every kit-shipped service gets: the adopter's Policy,
// then the root factory's runner, the shared permission gate and the
// audit sinks, resolved at Start.
func newService(root *cli.Root, cfg *Config) *rpcService {
	s := &rpcService{root: root, cfg: cfg}
	tr := &rpcTransport{svc: s}

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
		// Same class as the api service: it accepts requests that
		// mutate shared state, and it listens.
		transportsvc.WithClass(string(cli.SideEffectWriteShared), "listen"),
	}
	if len(cfg.Expose) > 0 {
		// A non-empty Expose narrows the whole-tree default.
		opts = append(opts, transportsvc.Hide("*"))
	}
	for _, p := range cfg.Expose {
		opts = append(opts, transportsvc.Expose(p))
	}
	for _, p := range cfg.Hide {
		opts = append(opts, transportsvc.Hide(p))
	}

	s.TransportService = transportsvc.NewTransportService(
		ServiceName, root.Cmd, cmdsurface.SurfaceRPC, tr, opts...,
	)
	return s
}

// addr resolves the listen address: --rpc-addr, then
// services.rpc.addr, then Config.Addr, then [DefaultAddr]. The
// flag is read from the parsed serve command at use, so a re-executed
// root never inherits a previous run's value.
func (s *rpcService) addr() string {
	if c := serveCmd(s.root); c != nil {
		if f := c.Flags().Lookup(addrFlag); f != nil && f.Changed && f.Value.String() != "" {
			return f.Value.String()
		}
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

// optIn resolves a boolean opt-in: the config key when set, else the
// code value. There is no flag layer.
func (s *rpcService) optIn(subkey string, code bool) bool {
	if s.root.Viper != nil {
		key := keyPrefix + subkey
		if s.root.Viper.IsSet(key) {
			return s.root.Viper.GetBool(key)
		}
	}
	return code
}

// maxBodyBytes is the per-message read bound.
func (s *rpcService) maxBodyBytes() int {
	if s.cfg.MaxBodyBytes > 0 {
		return s.cfg.MaxBodyBytes
	}
	return DefaultMaxBodyBytes
}

// validate is the configuration gate (serve-lifecycle.md §"The override rule"):
// every refusal here is a usage error at exit 2, before anything
// binds. The exposure refusals are the api service's, under this
// service's names.
func (s *rpcService) validate() error {
	addr := s.addr()
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("addr: %w", err)
	}
	if !cli.IsLoopbackAddr(addr) {
		if s.cfg.Auth == nil && !s.optIn(subkeyInsecureRemote, s.cfg.InsecureRemote) {
			return fmt.Errorf(
				"addr: %q is not a loopback address and the rpc service has no authentication; "+
					"set rpcserve.Config.Auth, listen on 127.0.0.1, or set services.rpc.insecure_remote: true "+
					"to serve unauthenticated beyond loopback",
				addr,
			)
		}
		if !cli.ServePolicyConfigured(s.root) && !s.optIn(subkeyInsecureNoPolicy, s.cfg.InsecureNoPolicy) {
			return fmt.Errorf(
				"addr: %q is not a loopback address and no delegation policy is configured; "+
					"set --policy, listen on 127.0.0.1, or set services.rpc.insecure_no_policy: true "+
					"to serve every command beyond loopback",
				addr,
			)
		}
	}
	return cli.ValidateServeBridge(s.root)
}

// rpcTransport is the [transportsvc.Transport] behind the service: its
// own listener, serving the Commands handler MountRPC builds over the
// bridge the seam reflected at Start.
type rpcTransport struct {
	svc *rpcService

	mu   sync.Mutex
	ln   net.Listener
	srv  *http.Server
	stop context.CancelFunc
}

// Bind acquires the listener and reports the base URL a Connect,
// gRPC-Web or `buf curl` client is configured with; a gRPC client
// dials its host:port.
func (t *rpcTransport) Bind(context.Context) (string, error) {
	ln, err := net.Listen("tcp", t.svc.addr())
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}
	t.mu.Lock()
	t.ln = ln
	t.mu.Unlock()
	return "http://" + ln.Addr().String(), nil
}

// Serve mounts the Commands handler and serves until ctx is canceled
// or Close runs.
func (t *rpcTransport) Serve(ctx context.Context, _ transportsvc.Invoker) error {
	t.mu.Lock()
	ln := t.ln
	t.mu.Unlock()
	if ln == nil {
		return errors.New("rpc: Serve called before Bind")
	}
	b := t.svc.Bridge()
	if b == nil {
		_ = ln.Close()
		return errors.New("rpc: no bridge; the service has not started")
	}
	withholdUnserved(b, t.svc.root, cmdsurface.SurfaceRPC)

	rs := rpc.NewServer(t.svc.serverOpts...)
	if err := cmdsurface.MountRPC(b, rs, t.mountOptions(b)...); err != nil {
		_ = ln.Close()
		return err
	}

	// Request contexts derive from base, so stopping the service ends
	// every open stream — a stream has no end of its own, and would
	// otherwise hold Shutdown until its budget ran out.
	base, cancelBase := context.WithCancel(context.WithoutCancel(ctx))
	srv := rs.HTTPServer()
	srv.Handler = t.middleware()(liftStreamWriteDeadline(rs))
	srv.ReadHeaderTimeout = 5 * time.Second
	srv.BaseContext = func(net.Listener) context.Context { return base }

	t.mu.Lock()
	t.srv = srv
	t.stop = cancelBase
	t.mu.Unlock()

	// A supervisor that cancels without calling Stop must not leave
	// the listener open.
	after := context.AfterFunc(ctx, func() { _ = t.Close(context.Background()) })
	defer after()

	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close ends every open stream, then drains the server within ctx; a
// server that cannot drain in time is closed outright.
func (t *rpcTransport) Close(ctx context.Context) error {
	t.mu.Lock()
	srv, stop, ln := t.srv, t.stop, t.ln
	t.mu.Unlock()

	if stop != nil {
		stop()
	}
	if srv == nil {
		if ln != nil {
			if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				return err
			}
		}
		return nil
	}
	if err := srv.Shutdown(ctx); err != nil {
		_ = srv.Close()
		return err
	}
	return nil
}

// mountOptions wires the service's gates into the Commands handler:
// Auth as an interceptor on every procedure, the call's provenance
// from what the server verified rather than what the body claims, the
// kit/auth-required gate answered by that verification, and the
// message size bound; then the adopter's interceptors, inside them
// all.
func (t *rpcTransport) mountOptions(b *cmdsurface.Bridge) []cmdsurface.RPCOption {
	opts := []cmdsurface.RPCOption{
		cmdsurface.WithRPCCallMeta(rpcCallMeta),
		cmdsurface.WithRPCAuthenticated(func(ctx context.Context, _ connect.AnyRequest) bool {
			return rpc.Authenticated(ctx)
		}),
		cmdsurface.WithRPCHandlerOptions(connect.WithReadMaxBytes(t.svc.maxBodyBytes())),
	}
	if t.svc.cfg.Auth != nil {
		opts = append(opts, cmdsurface.WithRPCInterceptors(
			rpc.Authenticate(t.svc.cfg.Auth, rpc.OnAuthRefused(auditRPCAuthRefusal(b))),
		))
	}
	if len(t.svc.cfg.Interceptors) > 0 {
		opts = append(opts, cmdsurface.WithRPCAdmittedInterceptors(t.svc.cfg.Interceptors...))
	}
	return opts
}

// middleware is the HTTP stack in front of the handler: request ids,
// request logging, and panic recovery, as on the api and mcp services.
func (t *rpcTransport) middleware() api.Middleware {
	logger := kitlog.New(t.svc.root.Viper)
	return api.Chain(
		api.RequestID(),
		api.Logger(logger.Info),
		api.Recovery(func(v any, r *http.Request) {
			logger.Error("panic recovered", "error", v, "path", r.URL.Path)
		}),
	)
}

// liftStreamWriteDeadline lifts the server's write deadline for
// InvokeStream calls only. The deadline is sized for request/reply; a
// stream outlives it by design, and once it expires the next message
// fails and the stream is cut. Every other call keeps it. The read
// deadline needs nothing: the request message is read before the
// stream starts.
func liftStreamWriteDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == cmdsurface.RPCInvokeStreamProcedure {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		}
		next.ServeHTTP(w, r)
	})
}

// rpcCallMeta is the provenance of one call. Identity comes from what
// Auth verified, never from the request body: without Auth, Caller
// and Tenant stay empty whatever the client claimed, and the body's
// extra map is dropped, since a client-written "scopes" entry would
// otherwise read as a credential's. The request id is the
// X-Request-ID header (issued when absent) unless only the body named
// one; the trace and idempotency key come from the headers REST reads,
// falling back to the body's.
func rpcCallMeta(ctx context.Context, req connect.AnyRequest, claimed cmdsurface.Meta) cmdsurface.Meta {
	hdr := req.Header()
	peer := req.Peer()
	hr := (&http.Request{Header: hdr, RemoteAddr: peer.Addr}).WithContext(ctx)

	meta := cmdsurface.Meta{
		RequestID:      api.RequestIDFromContext(ctx),
		TraceID:        api.TraceIDFromRequest(hr),
		IdempotencyKey: hdr.Get(api.HeaderIdempotencyKey),
	}
	if hdr.Get("X-Request-ID") == "" && claimed.RequestID != "" {
		meta.RequestID = claimed.RequestID
	}
	if meta.TraceID == "" {
		meta.TraceID = claimed.TraceID
	}
	if meta.IdempotencyKey == "" {
		meta.IdempotencyKey = claimed.IdempotencyKey
	}

	extra := map[string]string{"rpc_protocol": peer.Protocol}
	if peer.Addr != "" {
		extra["remote_addr"] = peer.Addr
	}
	if rpc.Authenticated(ctx) {
		claims := rpc.ClaimsFromContext(ctx)
		meta.Caller, meta.Tenant = api.IdentityOf(claims)
		if scopes := api.ScopesOf(claims); len(scopes) > 0 {
			extra["scopes"] = strings.Join(scopes, ",")
		}
	}
	meta.Extra = extra
	return meta
}

// auditRPCAuthRefusal reports a call Auth refused into the bridge's
// sinks, so "not authenticated" lands in the same stream as the
// bridge's own verdicts. The command is not known at this layer: a
// streaming call's message has not been read yet.
func auditRPCAuthRefusal(b *cmdsurface.Bridge) func(context.Context, rpc.RefusedCall, error) {
	return func(ctx context.Context, call rpc.RefusedCall, err error) {
		hr := (&http.Request{Header: call.Header}).WithContext(ctx)
		b.Audit(ctx, cmdsurface.Invocation{
			Meta: cmdsurface.Meta{
				Surface:     cmdsurface.SurfaceRPC,
				RequestID:   api.RequestIDFromContext(ctx),
				TraceID:     api.TraceIDFromRequest(hr),
				RequestedAt: time.Now(),
				Extra: map[string]string{
					"rpc_protocol":  call.Peer.Protocol,
					"rpc_procedure": call.Procedure,
					"remote_addr":   call.Peer.Addr,
				},
			},
		}, cmdsurface.Result{}, fmt.Errorf("%w: %v", cmdsurface.ErrAuthRefused, err))
	}
}

// withholdUnserved hides from surface every command the reflector
// judges non-invocable for a served surface — interactive,
// management-only, self-hosting — under the same reflection the REST
// projection mounts from (the Root's reserved verbs, no Allow*
// options). The bridge reflects more permissively, describing
// reserved verbs as leaves the socket reaches; a network surface
// narrows to REST's set.
func withholdUnserved(b *cmdsurface.Bridge, root *cli.Root, surface cmdsurface.Surface) {
	served := cmdreflect.Reflect(root.Cmd, cmdreflect.WithReserved(root))
	for _, leaf := range b.Leaves() {
		if !leaf.Enabled[surface] {
			continue
		}
		if d := served.Lookup(leaf.PathKey()); d == nil || !d.Invocable {
			b.Hide(leaf.PathKey(), surface)
		}
	}
}
