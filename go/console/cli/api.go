package cli

import (
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// DefaultAPIAddr is the api service's listen address when neither
// APIConfig.Addr, services.api.addr, nor --addr sets one. It is a
// loopback address: a tool that serves its command tree over HTTP
// serves it to the machine it runs on until the adopter says
// otherwise, and saying otherwise means either configuring Auth or
// opting into unauthenticated exposure by name (see
// APIConfig.InsecureRemote), and bounding what those callers may run
// with a delegation policy (see APIConfig.InsecureNoPolicy).
const DefaultAPIAddr = "127.0.0.1:8080"

// APIConfig configures the built-in api service and token commands
// added by WithAPI.
type APIConfig struct {
	// Addr is the default listen address (default [DefaultAPIAddr],
	// a loopback address). A non-loopback address — ":8080",
	// "0.0.0.0:8080", a LAN IP — is refused at validation unless
	// Auth is set or InsecureRemote opts in, and refused again
	// unless a --policy is in force or InsecureNoPolicy opts in.
	Addr string
	// OpenAPI configures OpenAPI spec generation (nil = disabled).
	OpenAPI *api.OpenAPIConfig
	// Auth validates requests (nil = no auth). It gates every
	// route, projected and adopter-owned, and is what permits a
	// non-loopback Addr. The claims it returns attribute each call:
	// see [api.IdentityOf] for the shapes that carry a principal and
	// tenant into the audit trail and the permission gate.
	Auth api.AuthFunc
	// InsecureRemote permits serving WITHOUT authentication on a
	// non-loopback address. It is an explicit acceptance that every
	// host able to reach Addr may run every command the policy
	// permits, as whoever it claims to be. services.api.insecure_remote
	// and --insecure-remote set the same thing. It changes nothing
	// when Auth is set and honored.
	//
	// It waives authentication only. Whether a caller admitted this
	// way is bounded in what it may run is InsecureNoPolicy's
	// question, and a non-loopback address needs an answer to both.
	InsecureRemote bool
	// InsecureNoPolicy permits serving on a non-loopback address with
	// NO delegation policy in force. Without a policy the permission
	// gate permits every command for every caller, so this is an
	// explicit acceptance that any caller the surface admits may run
	// the whole command tree, destructive commands included.
	// services.api.insecure_no_policy and --insecure-no-policy set the
	// same thing. It changes nothing when --policy names a policy.
	//
	// It is separate from InsecureRemote because authentication and
	// authorization are separate: a tool with Auth configured has
	// said who may call, not what they may run.
	InsecureNoPolicy bool
	// Handlers registers custom routes on the router.
	Handlers func(r *api.Router)
	// Resources registers ResourceRouters (called after router setup).
	Resources func(r *api.Router, humaAPI interface{})
	// OnHub provides the WebSocket hub to the consumer (nil = no WS).
	OnHub func(hub *api.Hub)

	// Policy gates which projected commands may run over REST. The
	// zero value withholds every destructive command, which is the
	// safe default.
	//
	// To permit destructive commands over REST:
	//
	//	Policy: cmdsurface.Policy{
	//	    AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST},
	//	}
	//
	// Permitting them does not skip confirmation: a command that
	// declares kit/requires-confirmation still needs the
	// X-Confirm-Token header on every call.
	Policy cmdsurface.Policy

	// Expose lists the command patterns the projection may mount, in
	// the pattern language of [cmdsurface.Bridge.Expose] ("widget
	// add", "widget *", "*"). Empty exposes the whole tree, which is
	// what makes projection automatic; the destructive ceiling in
	// Policy still applies on top.
	Expose []string

	// Hide carves exceptions out of Expose, applied after it.
	// Hidden commands stay visible in the discovery listing with
	// invocable=false and the reason "withheld-by-config", and this
	// affects REST only — the CLI and every other surface are
	// untouched.
	Hide []string
}

// WithAPI returns a Root option that stores the API config, registers
// the HTTP API as the "api" service, and adds the "token" command when
// Auth is set.
//
// The API now reaches the command surface as a service under the
// kit-owned `serve` parent rather than as a leaf `serve` command
// (contract §"Compatibility"). For a tool whose only service is the
// API this is not observable: `<tool> serve` still starts the HTTP
// server with the same `--addr` and `--no-auth` flags and the same
// behavior. A tool that registers services of its own gains them as
// siblings under the same lifecycle.
//
// An adopter replacing the built-in API with its own implementation
// registers it through [WithServiceOverride] under the same name.
func WithAPI(cfg APIConfig) func(*Root) {
	return func(r *Root) {
		if cfg.Addr == "" {
			cfg.Addr = DefaultAPIAddr
		}
		r.apiCfg = &cfg

		r.ensureServeRegistry()
		r.serveReg.Register(newAPIService(r, &cfg))
		r.mountAPIServeFlags()

		if cfg.Auth != nil {
			r.Cmd.AddCommand(tokenCmd(r))
		}
	}
}

// mountAPIServeFlags puts the leaf `serve` command's own flags onto
// the serve parent, so an adopter's existing `<tool> serve --addr :9000`
// keeps working verbatim.
//
// They are per-service flags for the api service, and the contract
// makes per-service flags valid only under the selector form. These
// are the documented exception: --addr and --no-auth predate the
// hierarchy, and refusing them under the supervisor form would break
// every adopter that has one HTTP surface and a shell script that
// starts it. --insecure-remote joins them because it qualifies the
// other two — it is the flag that makes a non-loopback --addr
// without auth acceptable — and a qualifier that is valid in fewer
// places than the flags it qualifies would be a trap.
func (r *Root) mountAPIServeFlags() {
	cfg := r.apiCfg
	if cfg == nil {
		return
	}
	for _, c := range r.Cmd.Commands() {
		if c.Name() != "serve" {
			continue
		}
		if c.Flags().Lookup("addr") == nil {
			c.Flags().String("addr", cfg.Addr, "Listen address for the api service")
		}
		if cfg.Auth != nil && c.Flags().Lookup("no-auth") == nil {
			c.Flags().Bool("no-auth", false, "Disable authentication on the api service")
		}
		if c.Flags().Lookup(insecureRemoteFlag) == nil {
			c.Flags().Bool(insecureRemoteFlag, false,
				"Serve the api service without authentication on a non-loopback address")
		}
		if c.Flags().Lookup(insecureNoPolicyFlag) == nil {
			c.Flags().Bool(insecureNoPolicyFlag, false,
				"Serve the api service on a non-loopback address with no delegation policy")
		}
		return
	}
}
