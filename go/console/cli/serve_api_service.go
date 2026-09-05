package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// APIServiceName is the identifier the built-in HTTP API registers
// under. It is a stable service identifier: a CLI word, a config key
// segment (services.api.*), and a bus payload value at once, so
// renaming it would be a breaking change to the command surface, the
// config file, and any subscriber filtering on it.
const APIServiceName = "api"

// Service-owned config keys and flags for the api service.
const (
	// apiSubkeyAddr is services.api.addr.
	apiSubkeyAddr = ".addr"
	// apiSubkeyInsecureRemote is services.api.insecure_remote, the
	// configuration form of --insecure-remote.
	apiSubkeyInsecureRemote = ".insecure_remote"
	// insecureRemoteFlag is the flag that opts into serving without
	// authentication beyond loopback.
	insecureRemoteFlag = "insecure-remote"
)

// apiService adapts the HTTP API to the serve.Service lifecycle.
//
// It wraps exactly the server construction the leaf `serve` command
// performed — the same middleware stack, the same router options, the
// same WebSocket hub — so a tool whose only service is the API behaves
// as it did before (contract §"Compatibility").
//
// The one behavioral difference is readiness. The leaf command bound
// its listener inside http.Server.ListenAndServe, which reports
// nothing and swallows the resolved port for an ":0" address. A
// service must report ready only once every acquisition that can fail
// deterministically has succeeded, so this binds the listener itself
// and reports ready after the bind — which also makes the resolved
// address observable to a test.
type apiService struct {
	cfg    *APIConfig
	root   *Root
	noAuth bool
	// insecureFlag records --insecure-remote; it is kept apart from
	// cfg.InsecureRemote so the flag wins over the config key and the
	// config key wins over the code default.
	insecureFlag bool

	mu   sync.Mutex
	srv  *http.Server
	addr string
	up   bool
}

// newAPIService returns the api service over cfg. addr overrides
// cfg.Addr when non-empty, which is how --addr keeps working.
func newAPIService(root *Root, cfg *APIConfig) *apiService {
	return &apiService{cfg: cfg, root: root}
}

func (a *apiService) Name() string { return APIServiceName }

// Validate is the configuration gate: an address that does not parse
// is a usage error caught before anything binds, rather than a start
// failure discovered a second later.
//
// It is also the exposure gate. A listen address that is not loopback
// is refused when nothing authenticates the callers it admits — no
// Auth configured, or Auth disabled with --no-auth — unless the
// adopter accepted that by name through services.api.insecure_remote
// or --insecure-remote. Refusing at validation, at exit 2, is what
// keeps "I forgot Auth" from becoming "every host on the network can
// run my commands": the message names the three ways to proceed.
func (a *apiService) Validate() error {
	addr := a.listenAddr()
	if addr == "" {
		return errors.New("addr: empty listen address")
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("addr: %w", err)
	}
	if err := a.validateExposure(addr); err != nil {
		return err
	}
	// The permission gate is built from --policy at start; a --policy
	// that cannot be loaded is a configuration error, and belongs
	// here rather than a second later as a start failure. A root
	// factory that cannot build a usable tree is the same class.
	if _, err := a.root.servePermission(); err != nil {
		return err
	}
	return a.root.validateRootFactory()
}

// validateExposure refuses a non-loopback address the service would
// serve unauthenticated, unless the opt-in is set.
func (a *apiService) validateExposure(addr string) error {
	if isLoopbackAddr(addr) || a.authenticates() || a.insecureRemote() {
		return nil
	}
	const fix = "listen on 127.0.0.1, or set services.api.insecure_remote: true (or --insecure-remote) to serve unauthenticated beyond loopback"
	if a.cfg.Auth != nil && a.noAuth {
		return fmt.Errorf(
			"addr: %q is not a loopback address and --no-auth disables authentication; drop --no-auth, %s",
			addr, fix,
		)
	}
	return fmt.Errorf(
		"addr: %q is not a loopback address and the api service has no authentication; set APIConfig.Auth, %s",
		addr, fix,
	)
}

// authenticates reports whether requests will pass through Auth: an
// AuthFunc is configured and --no-auth did not disable it.
func (a *apiService) authenticates() bool {
	return a.cfg.Auth != nil && !a.noAuth
}

// insecureRemote resolves the opt-in with the usual precedence: the
// flag, then services.api.insecure_remote, then APIConfig.
func (a *apiService) insecureRemote() bool {
	if a.insecureFlag {
		return true
	}
	if a.root != nil && a.root.Viper != nil {
		key := serveKeyPrefix + APIServiceName + apiSubkeyInsecureRemote
		if a.root.Viper.IsSet(key) {
			return a.root.Viper.GetBool(key)
		}
	}
	return a.cfg.InsecureRemote
}

// Class declares the side-effect and network class the policy gate
// resolves. An HTTP server that accepts requests from the network is
// the clearest case of both.
func (a *apiService) Class() (sideEffect, network string) {
	return string(SideEffectWriteShared), "listen"
}

// listenAddr resolves the listen address: services.api.addr when set,
// otherwise the APIConfig default that --addr also writes to.
func (a *apiService) listenAddr() string {
	if a.root != nil && a.root.Viper != nil {
		if v := a.root.Viper.GetString(serveKeyPrefix + APIServiceName + apiSubkeyAddr); v != "" {
			return v
		}
	}
	return a.cfg.Addr
}

// Start binds the listener, reports ready, and serves until ctx is
// canceled.
func (a *apiService) Start(ctx context.Context, ready func()) error {
	handler, err := a.buildHandler(ctx)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", a.listenAddr())
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	srv := &http.Server{
		Handler:           handler,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	a.mu.Lock()
	a.srv = srv
	a.addr = ln.Addr().String()
	a.up = true
	a.mu.Unlock()

	// The listener is bound: every acquisition that can fail
	// deterministically has succeeded, so the service is ready.
	ready()

	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Ready reports whether the listener is bound and serving.
func (a *apiService) Ready() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.up
}

// Addr is the resolved listen address once bound, which is how a
// caller learns the port chosen for an ":0" address.
func (a *apiService) Addr() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.addr
}

// Stop drains in-flight requests within the caller's budget.
func (a *apiService) Stop(ctx context.Context) error {
	a.mu.Lock()
	srv := a.srv
	a.up = false
	a.mu.Unlock()

	if srv == nil {
		return nil
	}
	if err := srv.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}

// buildHandler assembles the router exactly as the leaf `serve`
// command did.
//
// The projection's bridge is built first, before the middleware,
// because the auth middleware reports its refusals into the bridge's
// audit sinks: an unauthenticated call and a permitted one must land
// in the same stream, and only the bridge knows where that is.
func (a *apiService) buildHandler(ctx context.Context) (http.Handler, error) {
	logger := kitlog.New(a.root.Viper)

	pcfg, bridge, err := a.projection()
	if err != nil {
		return nil, err
	}

	mws := []api.Middleware{
		api.RequestID(),
		api.Logger(logger.Info),
		api.Recovery(func(v any, r *http.Request) {
			logger.Error("panic recovered", "error", v, "path", r.URL.Path)
		}),
		api.ContentType("application/json"),
	}
	if a.authenticates() {
		mws = append(mws, api.Auth(a.cfg.Auth, api.OnAuthRefused(auditAuthRefusal(bridge))))
	}

	opts := []api.RouterOption{api.WithMiddleware(mws...)}
	if a.cfg.OpenAPI != nil {
		opts = append(opts, api.WithOpenAPI(*a.cfg.OpenAPI))
	}
	router := api.NewRouter(opts...)

	if a.cfg.Handlers != nil {
		a.cfg.Handlers(router)
	}
	if a.cfg.Resources != nil {
		a.cfg.Resources(router, api.HumaAPI(router))
	}
	if a.cfg.OnHub != nil {
		hub := api.NewHub()
		go hub.Run(ctx)
		router.Handle("GET", "/ws", api.WSHandler(hub))
		a.cfg.OnHub(hub)
	}

	a.mountProjection(router, pcfg)

	return router, nil
}

// projection reflects the completed command tree into the projection
// config and the bridge that executes it. A tool with no command
// root projects nothing and audits nothing.
func (a *apiService) projection() (api.ProjectionConfig, *cmdsurface.Bridge, error) {
	if a.root == nil || a.root.Cmd == nil {
		return api.ProjectionConfig{}, nil, nil
	}
	// Config is the authority for name and version: Cmd.Version
	// carries the rendered --version template, not the bare value.
	return buildProjection(
		a.root.Cmd, a.root.Config.Name, a.root.Config.Version, a.root, a.cfg,
	)
}

// mountProjection mounts the versioned REST projection plus its
// OpenAPI description.
//
// It runs LAST, after the adopter's Handlers and Resources, so an
// adopter route always wins a pattern collision: the projection is
// additive, and a tool that already serves something at a path it
// happens to want keeps serving it.
//
// The tree is reflected at start, because that is the first moment
// cobra has all of it — WithAPI runs while the tree is still being
// built.
func (a *apiService) mountProjection(router *api.Router, cfg api.ProjectionConfig) {
	if a.root == nil || a.root.Cmd == nil {
		return
	}
	api.MountCommandProjection(router, cfg)
	api.DescribeCommandProjection(router, cfg)
	// Serves a floor spec only when the adopter never configured
	// WithOpenAPI; a no-op otherwise.
	api.MountMinimalProjectionSpec(router, cfg)
}

// auditAuthRefusal returns the hook the auth middleware calls for a
// request it refuses. The refusal reaches the bridge's sinks as an
// invocation that never ran, carrying what the transport knows at
// that point: the request id, the trace id, the peer, and the
// command the URL addressed when it is a projected route.
func auditAuthRefusal(bridge *cmdsurface.Bridge) func(r *http.Request, err error) {
	return func(r *http.Request, err error) {
		if bridge == nil {
			return
		}
		meta := api.RequestMetaFrom(r)
		inv := cmdsurface.Invocation{
			Path: projectedPathOf(r.URL.Path),
			Meta: cmdsurface.Meta{
				Surface:     cmdsurface.SurfaceREST,
				RequestID:   meta.RequestID,
				TraceID:     meta.TraceID,
				RequestedAt: meta.ReceivedAt,
				Extra: map[string]string{
					"http_method": r.Method,
					"http_path":   r.URL.Path,
					"remote_addr": meta.RemoteAddr,
				},
			},
		}
		bridge.Audit(r.Context(), inv, cmdsurface.Result{},
			fmt.Errorf("%w: %v", cmdsurface.ErrAuthRefused, err))
	}
}

// projectedPathOf returns the command path a projected route
// addresses, or nil for any other URL.
func projectedPathOf(urlPath string) []string {
	prefix := api.CommandProjectionPrefix + "/"
	if !strings.HasPrefix(urlPath, prefix) {
		return nil
	}
	rest := strings.Trim(strings.TrimPrefix(urlPath, prefix), "/")
	if rest == "" {
		return nil
	}
	return strings.Split(rest, "/")
}

// applyAPICompat maps the leaf `serve` command's own flags onto the
// api service and preserves its default-on behavior.
//
// Two things keep an existing adopter working (contract
// §"Compatibility"):
//
//   - --addr and --no-auth reach the api service, because they were
//     the leaf command's flags and adopters' scripts still pass them.
//   - The api service is enabled by default, because calling WithAPI
//     IS the request to serve it. Enablement defaults to false for a
//     service that arrived through the registry — where an unrequested
//     open port is the risk the default guards against — but WithAPI
//     is not that case: the adopter asked for exactly this surface.
//
// A services.api.enabled key in config still wins, so an adopter that
// has migrated can turn it off without removing the option.
func applyAPICompat(cmd *cobra.Command, root *Root, configs map[string]serve.Config) {
	if root.apiCfg == nil || root.serveReg == nil {
		return
	}
	svc, ok := root.serveReg.Lookup(APIServiceName)
	if !ok {
		return
	}

	if a, isAPI := svc.(*apiService); isAPI {
		if f := cmd.Flags().Lookup("addr"); f != nil && f.Changed {
			addr, _ := cmd.Flags().GetString("addr")
			a.cfg.Addr = addr
		}
		if noAuth, err := cmd.Flags().GetBool("no-auth"); err == nil {
			a.noAuth = noAuth
		}
		if f := cmd.Flags().Lookup(insecureRemoteFlag); f != nil && f.Changed {
			a.insecureFlag, _ = cmd.Flags().GetBool(insecureRemoteFlag)
		}
	}

	explicitlyConfigured := root.Viper != nil &&
		root.Viper.IsSet(serveKeyPrefix+APIServiceName+serveSubkeyEnabled)
	if explicitlyConfigured {
		return
	}
	cfg := configs[APIServiceName]
	cfg.Enabled = true
	configs[APIServiceName] = cfg
}
