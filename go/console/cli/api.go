package cli

import "hop.top/kit/go/transport/api"

// APIConfig configures the built-in api service and token commands
// added by WithAPI.
type APIConfig struct {
	// Addr is the default listen address (default ":8080").
	Addr string
	// OpenAPI configures OpenAPI spec generation (nil = disabled).
	OpenAPI *api.OpenAPIConfig
	// Auth validates requests (nil = no auth).
	Auth api.AuthFunc
	// Handlers registers custom routes on the router.
	Handlers func(r *api.Router)
	// Resources registers ResourceRouters (called after router setup).
	Resources func(r *api.Router, humaAPI interface{})
	// OnHub provides the WebSocket hub to the consumer (nil = no WS).
	OnHub func(hub *api.Hub)
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
			cfg.Addr = ":8080"
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
// two are the documented exception: they predate the hierarchy, and
// refusing them under the supervisor form would break every adopter
// that has one HTTP surface and a shell script that starts it.
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
		return
	}
}
