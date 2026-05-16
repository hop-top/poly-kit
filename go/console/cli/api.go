package cli

import "hop.top/kit/go/transport/api"

// APIConfig configures the built-in serve and token commands added by
// WithAPI.
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

// WithAPI returns a Root option that stores the API config and registers
// the "serve" command (and "token" when Auth is set).
func WithAPI(cfg APIConfig) func(*Root) {
	return func(r *Root) {
		if cfg.Addr == "" {
			cfg.Addr = ":8080"
		}
		r.apiCfg = &cfg
		r.Cmd.AddCommand(serveCmd(r))
		if cfg.Auth != nil {
			r.Cmd.AddCommand(tokenCmd(r))
		}
	}
}
