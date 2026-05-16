package router

import (
	"fmt"
	"net/http"
)

// Handler returns an http.Handler using [NewServer] for routing.
// Convenience wrapper for callers that want the http.Handler interface.
func Handler(ctrl *Controller) http.Handler {
	return NewServer(ctrl)
}

// ServerConfig holds configuration for the routellm HTTP server.
type ServerConfig struct {
	Addr string `yaml:"addr"`
}

// DefaultServerConfig returns sensible defaults.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{Addr: ":6060"}
}

// NewHTTPServer creates an *http.Server with the given config and handler.
func NewHTTPServer(cfg ServerConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
	}
}

// RouterServerConfig holds the configuration for setting up the router
// controller.
type RouterServerConfig struct {
	Routers     []string `yaml:"routers"`
	StrongModel string   `yaml:"strong_model"`
	WeakModel   string   `yaml:"weak_model"`
	Threshold   float64  `yaml:"threshold"`
}

// FullConfig is the top-level configuration for the routellm server.
type FullConfig struct {
	Router RouterServerConfig `yaml:"router"`
	Server ServerConfig       `yaml:"server"`
}

// DefaultFullConfig returns defaults for the full server config.
func DefaultFullConfig() FullConfig {
	return FullConfig{
		Router: RouterServerConfig{
			Routers:   []string{"random"},
			Threshold: 0.5,
		},
		Server: DefaultServerConfig(),
	}
}

// Validate checks the config for obvious errors.
func (c *FullConfig) Validate() error {
	if c.Router.StrongModel == "" {
		return fmt.Errorf("router.strong_model is required")
	}
	if c.Router.WeakModel == "" {
		return fmt.Errorf("router.weak_model is required")
	}
	if c.Router.Threshold < 0 || c.Router.Threshold > 1 {
		return fmt.Errorf(
			"router.threshold %.4f out of [0,1] range",
			c.Router.Threshold,
		)
	}
	if c.Server.Addr == "" {
		c.Server.Addr = ":6060"
	}
	return nil
}
