package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

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
	// add", "widget *", "*"). Empty exposes the whole tree: a local
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
	//		AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceRPC},
	//	}
	//
	// Naming a surface here widens that surface only; every other
	// transport keeps the ceiling it had.
	Policy cmdsurface.Policy

	// Auth, when set, verifies every request before it is invoked;
	// see [socket.Authenticator]. Without one the socket file's
	// 0600 permission is the access control, and the caller and
	// tenant a request carries are recorded as provenance only —
	// nothing is granted on their basis. With one, the verified
	// identity replaces them and a refusal answers UNAUTHENTICATED.
	Auth socket.Authenticator
}

// WithSocket returns a Root option registering the built-in `socket`
// service: the tool's command tree served as newline-delimited JSON
// over a Unix domain socket.
//
// Unlike [WithAPI], the socket service is NOT enabled by default.
// WithAPI's default-on behavior is a compatibility obligation to
// adopters whose `serve` predates the hierarchy; a service arriving
// through the registry for the first time gets the contract's default
// instead, which is `enabled: false` (contract §"Configuration
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
		// load, so the error path here is unreachable in practice.
		// The runner is resolved at Start for the same reason: a root
		// factory replays the operator's parsed root flags onto every
		// tree it builds. The shared options go last so a
		// test-injected Runner still wins.
		transportsvc.WithBridgeOptionsFunc(func() []cmdsurface.Option {
			shared, err := root.serveBridgeOptions()
			if err != nil {
				return nil
			}
			return append(root.serveRunnerOptions(), shared...)
		}),
		transportsvc.WithValidate(func() error {
			if err := validateSocketPath(root, cfg); err != nil {
				return err
			}
			if _, err := root.servePermission(); err != nil {
				return err
			}
			return root.validateRootFactory()
		}),
		// Same class as the api service: it accepts requests that
		// mutate shared state, and it listens.
		transportsvc.WithClass(string(SideEffectWriteShared), "listen"),
	}
	for _, p := range cfg.Expose {
		opts = append(opts, transportsvc.Expose(p))
	}
	for _, p := range cfg.Hide {
		opts = append(opts, transportsvc.Hide(p))
	}

	svc := transportsvc.NewTransportService(
		SocketServiceName, root.Cmd, cmdsurface.SurfaceRPC, tr, opts...,
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
	l.tr = socket.New(path)
	l.tr.Auth = l.cfg.Auth
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
	inv.Meta.Surface = cmdsurface.SurfaceRPC
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
// failure a second later (contract §"The override rule").
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
