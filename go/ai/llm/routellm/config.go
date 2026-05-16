// Package routellm provides configuration types for the RouteLLM router.
//
// RouteLLM configuration lives inside a provider's "extras" map in the
// main llm.yaml config file. Use [ParseRouterConfig] to extract and
// validate it, or [DefaultRouterConfig] for sensible defaults.
//
// Environment variable overrides (highest precedence):
//
//   - ROUTELLM_BASE_URL      — router HTTP base URL
//   - ROUTELLM_STRONG_MODEL  — strong model identifier
//   - ROUTELLM_WEAK_MODEL    — weak model identifier
//   - ROUTELLM_ROUTERS       — comma-separated router names
package routellm

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// RouterConfig holds the full RouteLLM router configuration.
type RouterConfig struct {
	BaseURL      string         `yaml:"base_url"`
	GRPCPort     int            `yaml:"grpc_port"`
	StrongModel  string         `yaml:"strong_model"`
	WeakModel    string         `yaml:"weak_model"`
	Routers      []string       `yaml:"routers"`
	RouterConfig map[string]any `yaml:"router_config"`
	Eva          EvaConfig      `yaml:"eva"`
	Autostart    bool           `yaml:"autostart"`
	PIDFile      string         `yaml:"pid_file"`
}

// EvaConfig holds evaluation/contract enforcement settings.
type EvaConfig struct {
	Contracts []string `yaml:"contracts"`
	Enforce   bool     `yaml:"enforce"`
}

// DefaultRouterConfig returns a RouterConfig with sensible defaults.
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		BaseURL:  "http://localhost:6060",
		GRPCPort: 6061,
	}
}

// ParseRouterConfig extracts and validates RouteLLM configuration from
// the provider extras map. Missing keys fall back to defaults. Environment
// variables override all other sources.
func ParseRouterConfig(extras map[string]any) (RouterConfig, error) {
	cfg := DefaultRouterConfig()

	// Extract the "routellm" sub-map if present.
	raw, ok := extras["routellm"]
	if ok {
		sub, isMap := raw.(map[string]any)
		if !isMap {
			return cfg, fmt.Errorf(
				"routellm: expected map, got %T", raw,
			)
		}
		if err := remarshal(sub, &cfg); err != nil {
			return cfg, fmt.Errorf("routellm: %w", err)
		}
	}

	// Environment variable overrides.
	if v := os.Getenv("ROUTELLM_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("ROUTELLM_STRONG_MODEL"); v != "" {
		cfg.StrongModel = v
	}
	if v := os.Getenv("ROUTELLM_WEAK_MODEL"); v != "" {
		cfg.WeakModel = v
	}
	if v := os.Getenv("ROUTELLM_ROUTERS"); v != "" {
		routers := make([]string, 0)
		for _, r := range strings.Split(v, ",") {
			if s := strings.TrimSpace(r); s != "" {
				routers = append(routers, s)
			}
		}
		cfg.Routers = routers
	}

	return cfg, nil
}

// remarshal round-trips a map[string]any through YAML into a typed struct.
func remarshal(src map[string]any, dst any) error {
	data, err := yaml.Marshal(src)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, dst)
}
