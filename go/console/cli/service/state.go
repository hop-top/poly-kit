package service

import (
	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/serve"
)

// state is the serve plane's per-Root state, attached through the
// extension slot: the plane's options write it, and its readers find
// it with stateOf or lookup.
type state struct {
	// apiCfg is the built-in api service's configuration, nil until
	// WithAPI runs.
	apiCfg *APIConfig
	// serveReg is the registry `service start` children register
	// into, nil until the first WithService/WithServices/WithAPI
	// option runs. Its presence is what mounts the kit-owned serve
	// parent.
	serveReg *serve.Registry
	// serviceCmd is the kit-owned `service` group, serveCmd its
	// `start` leaf, and serveAlias the hidden deprecated root `serve`
	// that runs the same thing; all nil until the registry exists.
	// Every behavior keyed to kit's serve command (the per-service
	// flags, the policy exemption, the help addendum) finds it
	// through these pointers, never by name.
	serviceCmd *cobra.Command
	serveCmd   *cobra.Command
	serveAlias *cobra.Command
	// servePolicy is the third validation gate for service selection;
	// nil passes every service.
	servePolicy serve.PolicyGate
	// serveBus is the bus the supervisor publishes lifecycle events
	// to; nil leaves only the log counterpart.
	serveBus serve.Publisher
	// serveOwnConfig records WithOwnServiceConfig: the tool layers
	// services.* into Viper itself and serve reads no source of its own.
	serveOwnConfig bool
	// controlScope resolves the scope of the instance a `service`
	// command addresses; nil is the one default instance. See
	// WithServiceControlScope.
	controlScope ServiceControlScope
	// socketCfg is the built-in socket service's configuration, nil
	// until WithSocket runs.
	socketCfg *SocketConfig
	// socketFlag is the --socket value when the operator passed one;
	// it wins over config at bind time.
	socketFlag string
	// serveRateLimitCode is the rate_limit code default; see
	// WithServeRateLimit.
	serveRateLimitCode rateLimitCode
	// serveAuth is the permission gate and audit sinks every
	// kit-shipped transport service shares; see serve_auth.go.
	serveAuth serveAuthState
	// serveObs is tracing and metrics; see serve_observability.go.
	serveObs ServeObservability
	// apiKeysCfg is the API-key store's configuration, nil until
	// WithAPIKeys runs.
	apiKeysCfg *APIKeysConfig
	// rootFactory rebuilds the tool's root for one served invocation;
	// nil serves every invocation on this Root's tree. See
	// root_factory.go.
	rootFactory func() *cli.Root
}

var stateKey = cli.NewExtensionKey[*state]("service")

// stateOf returns r's serve-plane state, attaching an empty one on
// first use.
func stateOf(r *cli.Root) *state {
	return cli.Extend(r, stateKey, func(*cli.Root) *state { return &state{} })
}

// lookup returns r's serve-plane state without attaching one; ok is
// false for a Root no serve option touched.
func lookup(r *cli.Root) (*state, bool) { return cli.Extension(r, stateKey) }

var (
	_ cli.OptionsHook     = (*state)(nil)
	_ cli.PrepareHook     = (*state)(nil)
	_ cli.PolicyExemption = (*state)(nil)
)

// OptionsApplied runs once every option of New has run: the
// deprecated `serve` takes the flags the options put on `service
// start`, and the token command is mounted now that WithAPI,
// WithIdentity and WithAPIKeys may have come in any order.
func (s *state) OptionsApplied(r *cli.Root) {
	syncServeAlias(r)
	if s.apiCfg != nil && (s.apiCfg.Auth != nil || cli.IdentityConfigured(r) || s.apiKeysCfg != nil) {
		mountTokenCmd(r)
	}
}

// PrepareTree lists the registered services on `service start --help`,
// so the question "which services can I name?" is answered without a
// second invocation, and gives the deprecated `serve` every flag the
// services put on `service start`, whichever option registered them.
// Both steps are idempotent.
func (s *state) PrepareTree(r *cli.Root) {
	serveHelpAddendum(r)
	syncServeAlias(r)
}

// ExemptFromPolicy exempts kit's `service start` and the deprecated
// root `serve`, by identity, once the plane owns the services: their
// --policy names the policy the served surfaces enforce on the calls
// they carry, not a gate on the operator starting them.
func (s *state) ExemptFromPolicy(_ *cli.Root, cmd *cobra.Command) (exempt, owns bool) {
	if s.serveCmd == nil {
		return false, false
	}
	return cmd == s.serveCmd || cmd == s.serveAlias, true
}
