package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
	"hop.top/kit/go/transport/transportsvc"
)

// SocketServiceName is the identifier the built-in Unix socket
// service registers under. Like [APIServiceName] it is a CLI word, a
// config key segment (services.socket.*), and a bus payload value at
// once, so it is stable across releases.
const SocketServiceName = "socket"

// socketSubkeyPath is the service-owned config key for the socket
// path: services.socket.path.
const socketSubkeyPath = ".path"

// SocketConfig configures the built-in `socket` service.
type SocketConfig struct {
	// Path is the socket path. Empty resolves to
	// <runtime dir>/<tool>/<tool>.sock — $XDG_RUNTIME_DIR when set,
	// otherwise the platform's own location for ephemeral per-user
	// files. services.socket.path overrides it, and --socket
	// overrides that.
	Path string

	// Expose lists the command patterns the socket may invoke, in
	// the pattern language of [cmdsurface.Bridge.Expose] ("widget
	// add", "widget *", "*"). A non-empty Expose narrows the socket
	// to what it names. Empty exposes the whole tree: a local
	// owner-only socket that reaches nothing is not useful, and the
	// destructive ceiling still applies on top.
	Expose []string

	// Hide carves exceptions out of Expose, applied after it.
	Hide []string

	// Policy gates which commands the socket may invoke. The zero
	// value behaves exactly like [cmdsurface.DefaultPolicy]: every
	// non-destructive command is reachable, and destructive ones are
	// refused on this surface.
	//
	// To permit destructive commands over the socket, name the
	// socket's surface:
	//
	//	Policy: cmdsurface.Policy{
	//		AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceSocket},
	//	}
	//
	// The socket invokes as [cmdsurface.SurfaceSocket], so only that
	// entry lifts its ceiling; naming SurfaceRPC here has no effect
	// on the socket. This Policy belongs to the socket's own bridge,
	// so no other transport's ceiling changes either.
	Policy cmdsurface.Policy

	// Auth, when set, verifies every request before it is invoked;
	// see [socket.Authenticator]. Without one the socket file's
	// 0600 permission is the access control, and the caller and
	// tenant a request carries are recorded as provenance only —
	// nothing is granted on their basis. With one, the verified
	// identity replaces them and a refusal answers UNAUTHENTICATED.
	//
	// services.socket.auth.mode: peer selects kit's peer-credential
	// authenticator ([socket.NewPeerAuthenticator]) instead: the mode
	// selects the verifier, and Auth is not consulted under it.
	Auth socket.Authenticator

	// PeerScopes, under services.socket.auth.mode: peer, maps an
	// admitted peer's kernel-reported credentials to the scopes it
	// holds, as a token's scopes reach the permission gate: a leaf
	// declaring kit/permissions runs for a peer holding every scope
	// it names. services.socket.auth.peer.scopes, or its services.all
	// default, replaces it when set: the operator's list is every
	// admitted peer's scopes, and PeerScopes is not consulted. With
	// neither, a peer holds no scopes and such a leaf is refused as
	// insufficient scope.
	PeerScopes func(socket.PeerCred) []string
}

// WithSocket returns a Root option registering the built-in `socket`
// service: the tool's command tree served as newline-delimited JSON
// over a Unix domain socket.
//
// Unlike [WithAPI], the socket service is NOT enabled by default.
// WithAPI's default-on behavior is a compatibility obligation to
// adopters whose `serve` predates the hierarchy; a service arriving
// through the registry for the first time gets the contract's default
// instead, which is `enabled: false` (serve-lifecycle.md §"Configuration
// surface"). Start it with `<tool> serve socket`, which overrides
// enablement, or set services.socket.enabled.
func WithSocket(cfg SocketConfig) func(*Root) {
	return func(r *Root) {
		r.ensureServeRegistry()
		r.socketCfg = &cfg
		r.serveReg.Register(newSocketService(r, &cfg))
		r.mountSocketServeFlags()
	}
}

// mountSocketServeFlags puts --socket on the serve parent, mirroring
// how --addr reaches the api service.
//
// The contract makes per-service flags valid under the selector form,
// which is the form this service is normally started with. It is
// registered on the parent because that is where cobra resolves flags
// for `serve socket`, and it is inert when the socket service is not
// the one selected.
func (r *Root) mountSocketServeFlags() {
	for _, c := range r.Cmd.Commands() {
		if c.Name() != "serve" {
			continue
		}
		if c.Flags().Lookup("socket") == nil {
			c.Flags().String("socket", "",
				"Socket path for the socket service")
		}
		return
	}
}

// newSocketService builds the socket service on the transport seam.
// Everything except the socket path and its validation is the seam's:
// reflection, the policy path, readiness, and stop.
func newSocketService(root *Root, cfg *SocketConfig) *transportsvc.TransportService {
	tr := &lazySocket{root: root, cfg: cfg}

	opts := []transportsvc.TransportOption{
		// A local owner-only channel that reaches nothing is not
		// useful; the destructive ceiling still gates what it can do.
		transportsvc.Expose("*"),
		// The zero Policy resolves identically to DefaultPolicy, so
		// an adopter that sets nothing gets the conservative gate.
		transportsvc.WithBridgeOptions(cmdsurface.WithPolicy(cfg.Policy)),
		// The permission gate and audit sinks are resolved at Start:
		// --policy is parsed and every adopter option has run only
		// then. Validate has already refused a --policy that cannot
		// load and opened the audit chains, so the error path here is
		// unreachable in practice; were it reached, the bridge refuses
		// every call rather than run one unaudited or ungated.
		// The runner is resolved at Start for the same reason: a root
		// factory replays the operator's parsed root flags onto every
		// tree it builds. The shared options go last so a
		// test-injected Runner still wins.
		transportsvc.WithBridgeOptionsFunc(func() []cmdsurface.Option {
			shared, err := root.serveBridgeOptions(SocketServiceName, ServeExposure{Loopback: true})
			if err != nil {
				return []cmdsurface.Option{cmdsurface.WithPermission(refuseAll(err))}
			}
			// A Unix socket is reachable only from this machine: the
			// rate limit's loopback default applies.
			limit, err := root.serveRateLimitOptions(SocketServiceName, true)
			if err != nil {
				return []cmdsurface.Option{cmdsurface.WithPermission(refuseAll(err))}
			}
			opts := append(root.serveRunnerOptions(), root.serveObservabilityOptions(SocketServiceName)...)
			opts = append(opts, limit...)
			return append(opts, shared...)
		}),
		transportsvc.WithValidate(func() error {
			if err := validateSocketPath(root, cfg); err != nil {
				return err
			}
			if _, err := resolveSocketAuth(root, cfg); err != nil {
				return err
			}
			if _, err := root.servePermission(ServeExposure{Loopback: true}); err != nil {
				return err
			}
			if err := validateServeAudit(root, SocketServiceName); err != nil {
				return err
			}
			if _, _, err := serveRateLimit(root.Viper, SocketServiceName, true); err != nil {
				return err
			}
			if _, _, err := serveConcurrency(root.Viper, SocketServiceName); err != nil {
				return err
			}
			if _, _, err := serveQuota(root.Viper, SocketServiceName); err != nil {
				return err
			}
			// timeouts.command reaches the socket; the server keys
			// have no listener to bound here.
			if err := validateServeTimeouts(root, SocketServiceName); err != nil {
				return err
			}
			if _, err := serveIdempotency(root.Viper, SocketServiceName); err != nil {
				return err
			}
			// The bridge options func above cannot report an error, so
			// the audit chains open here, where one can: a chain
			// another process holds fails the service before it
			// starts. Start reuses what this opened.
			if _, err := root.serveConfiguredAuditSinks(SocketServiceName); err != nil {
				return err
			}
			return root.validateRootFactory()
		}),
		// Same class as the api service: it accepts requests that
		// mutate shared state, and it listens.
		transportsvc.WithClass(string(SideEffectWriteShared), "listen"),
	}
	if len(cfg.Expose) > 0 {
		// A non-empty Expose narrows the whole-tree default: hide
		// everything, then expose what was named, then Hide's
		// exceptions, in that order.
		opts = append(opts, transportsvc.Hide("*"))
	}
	for _, p := range cfg.Expose {
		opts = append(opts, transportsvc.Expose(p))
	}
	for _, p := range cfg.Hide {
		opts = append(opts, transportsvc.Hide(p))
	}

	svc := transportsvc.NewTransportService(
		SocketServiceName, root.Cmd, cmdsurface.SurfaceSocket, tr, opts...,
	)
	// The transport reports its own refusals into the service's
	// bridge, which exists only once the service has started.
	tr.svc = svc
	return svc
}

// lazySocket defers resolving the socket path until Bind, so a
// --socket flag parsed after construction still wins. It is otherwise
// exactly [socket.Transport].
type lazySocket struct {
	root *Root
	cfg  *SocketConfig
	svc  *transportsvc.TransportService
	tr   *socket.Transport
}

func (l *lazySocket) Bind(ctx context.Context) (string, error) {
	path, err := resolveSocketPath(l.root, l.cfg)
	if err != nil {
		return "", err
	}
	auth, err := resolveSocketAuth(l.root, l.cfg)
	if err != nil {
		return "", err
	}
	l.tr = socket.New(path)
	l.tr.Auth = auth
	l.tr.OnRefused = l.refused
	return l.tr.Bind(ctx)
}

// refused routes a transport-level refusal (a failed authentication)
// into the bridge's audit sinks, so the socket's "not authenticated"
// lands in the same stream as the bridge's "not permitted" and "ran".
func (l *lazySocket) refused(ctx context.Context, inv cmdsurface.Invocation, err error) {
	if l.svc == nil {
		return
	}
	b := l.svc.Bridge()
	if b == nil {
		return
	}
	inv.Meta.Surface = cmdsurface.SurfaceSocket
	b.Audit(ctx, inv, cmdsurface.Result{}, err)
}

func (l *lazySocket) Serve(ctx context.Context, inv transportsvc.Invoker) error {
	if l.tr == nil {
		return errors.New("socket: Serve called before Bind")
	}
	return l.tr.Serve(ctx, inv)
}

func (l *lazySocket) Close(ctx context.Context) error {
	if l.tr == nil {
		return nil
	}
	return l.tr.Close(ctx)
}

// resolveSocketAuth resolves the socket's authenticator from
// services.socket.auth and its services.all default: the peer
// authenticator under auth.mode: peer, configured by auth.peer, else
// SocketConfig.Auth. Every refusal names the key at fault.
//
// auth.mode: mtls set under services.socket is refused, since the
// socket has no TLS listener to ask for a certificate; under
// services.all it is the HTTP listeners' default, and the socket does
// not read it. peer on a platform without peer credentials is refused
// here, so the service fails validation rather than refusing every
// caller.
func resolveSocketAuth(root *Root, cfg *SocketConfig) (socket.Authenticator, error) {
	var code socket.Authenticator
	if cfg != nil {
		code = cfg.Auth
	}
	if root == nil || root.Viper == nil {
		return code, nil
	}
	c := svcconfig.New(root.Viper)
	for _, b := range []string{authBlock, authPeerBlock} {
		if err := c.ValidateBlock(b, SocketServiceName, svcconfig.Shared); err != nil {
			return nil, err
		}
	}
	res := tlsResolver{cfg: c, svc: SocketServiceName}

	mode, modeKey := res.str(authBlock, "mode")
	mode = strings.ToLower(mode)
	switch {
	case mode == "" || mode == AuthModePeer:
	case isAuthMode(mode):
		// An HTTP listener's credential mode (mtls, jwt, jwks, oidc,
		// apikey). Set for the socket itself, validation refuses it
		// first (svcconfig HTTPValues); services.all's is the HTTP
		// listeners' default, which the socket does not read.
		if modeKey == svcconfig.Key(SocketServiceName, authBlock, "mode") {
			return nil, fmt.Errorf("%s: %q needs an HTTP listener, and the %s service has none; it supports %q",
				modeKey, mode, SocketServiceName, AuthModePeer)
		}
		mode = ""
	default:
		return nil, fmt.Errorf("%s: unknown mode %q; the %s service supports %q",
			modeKey, mode, SocketServiceName, AuthModePeer)
	}
	if mode != AuthModePeer {
		if k := res.anySet(authPeerBlock, "require_same_uid", "resolve_names", "scopes"); k != "" {
			return nil, fmt.Errorf("%s: set, but %s is not %q", k,
				svcconfig.Key(SocketServiceName, authBlock, "mode"), AuthModePeer)
		}
		return code, nil
	}

	var peer socket.PeerAuthConfig
	var err error
	if peer.RequireSameUID, _, err = res.boolean(authPeerBlock, "require_same_uid"); err != nil {
		return nil, err
	}
	if peer.ResolveNames, _, err = res.boolean(authPeerBlock, "resolve_names"); err != nil {
		return nil, err
	}
	if peer.Scopes, err = resolvePeerScopes(res, cfg); err != nil {
		return nil, err
	}
	auth, err := socket.NewPeerAuthenticator(peer)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", modeKey, err)
	}
	return auth, nil
}

// resolvePeerScopes is the peer authenticator's scope source:
// auth.peer.scopes from services.socket, then services.all, granted to
// every admitted peer; else SocketConfig.PeerScopes; else none. A set
// list replaces the code function rather than joining it, as a
// configured list replaces a code value everywhere in services.*: a
// merged grant is one nobody wrote, and the operator keeps the last
// word on what a peer may do.
func resolvePeerScopes(res tlsResolver, cfg *SocketConfig) (func(socket.PeerCred) []string, error) {
	raw, k, ok := res.cfg.Lookup(res.svc, authPeerBlock, "scopes")
	if !ok {
		if cfg == nil {
			return nil, nil
		}
		return cfg.PeerScopes, nil
	}
	switch v := raw.(type) {
	case string, []string:
	case []any:
		for _, e := range v {
			if _, ok := e.(string); !ok {
				return nil, fmt.Errorf("%s: must be a list of scope names", k)
			}
		}
	default:
		return nil, fmt.Errorf("%s: must be a list of scope names", k)
	}
	scopes := res.list(authPeerBlock, "scopes")
	return func(socket.PeerCred) []string { return slices.Clone(scopes) }, nil
}

// resolveSocketPath applies the configuration precedence: the --socket
// flag, then services.socket.path, then SocketConfig.Path, then the
// XDG runtime default.
func resolveSocketPath(root *Root, cfg *SocketConfig) (string, error) {
	if root != nil && root.socketFlag != "" {
		return expandSocketPath(root.socketFlag)
	}
	if root != nil && root.Viper != nil {
		if v := root.Viper.GetString(serveKeyPrefix + SocketServiceName + socketSubkeyPath); v != "" {
			return expandSocketPath(v)
		}
	}
	if cfg != nil && cfg.Path != "" {
		return expandSocketPath(cfg.Path)
	}
	return defaultSocketPath(root)
}

// defaultSocketPath is <runtime dir>/<tool>/<tool>.sock. The runtime
// base is $XDG_RUNTIME_DIR when set and the platform's own ephemeral
// per-user location otherwise, falling back to the OS temp directory
// — the same resolution every other kit runtime artifact takes.
func defaultSocketPath(root *Root) (string, error) {
	tool := "kit"
	if root != nil && root.Config.Name != "" {
		tool = root.Config.Name
	}
	return xdg.RuntimeFile(tool, tool+".sock")
}

// expandSocketPath makes a path absolute so the resolved value is
// unambiguous in the readiness event, whatever the process working
// directory is.
func expandSocketPath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path: empty socket path")
	}
	return filepath.Abs(p)
}

// validateSocketPath is the service's configuration gate: a path that
// cannot resolve, or whose parent is a file rather than a directory,
// is a usage error caught before anything binds rather than a start
// failure a second later (serve-lifecycle.md §"The override rule").
func validateSocketPath(root *Root, cfg *SocketConfig) error {
	path, err := resolveSocketPath(root, cfg)
	if err != nil {
		return fmt.Errorf("path: %w", err)
	}
	if path == "" {
		return errors.New("path: empty socket path")
	}
	// A unix socket path is bounded by the platform's sockaddr_un
	// size. Refusing here names the real problem; the kernel's own
	// error for an over-long path is "invalid argument".
	if len(path) > maxSocketPathLen {
		return fmt.Errorf(
			"path: %q is %d bytes, over the %d-byte limit for a unix socket path",
			path, len(path), maxSocketPathLen,
		)
	}
	if parent := filepath.Dir(path); parent != "" {
		if info, statErr := os.Stat(parent); statErr == nil && !info.IsDir() {
			return fmt.Errorf("path: %s is not a directory", parent)
		}
	}
	return nil
}

// maxSocketPathLen is the conservative portable bound on a unix
// socket path: sockaddr_un.sun_path is 104 bytes on darwin and the
// BSDs, 108 on Linux.
const maxSocketPathLen = 103

// validateSocketBlocks refuses an HTTP-plane middleware block set for
// the socket service: it has no HTTP listener, and gets what it needs
// from that plane from its own transport (the line bound, the
// owner-only file). The invocation-plane blocks — tracing, metrics,
// audit — apply to it as to every transport service.
func validateSocketBlocks(root *Root) error {
	if root.socketCfg == nil || root.serveReg == nil {
		return nil
	}
	if _, ok := root.serveReg.Lookup(SocketServiceName); !ok {
		return nil
	}
	return svcconfig.New(root.Viper).ValidateNoHTTP(SocketServiceName)
}

// applySocketFlags records the --socket flag so the service resolves
// it at bind time. It mirrors applyAPICompat's role for --addr.
func applySocketFlags(cmd *cobra.Command, root *Root) {
	if root.socketCfg == nil || root.serveReg == nil {
		return
	}
	if f := cmd.Flags().Lookup("socket"); f != nil && f.Changed {
		path, _ := cmd.Flags().GetString("socket")
		root.socketFlag = path
	}
}
