// Package llm provides configuration resolution for LLM providers.
//
// It supports a three-layer merge strategy: config file < URI < env vars.
// Provider URIs follow the format scheme://model[?param=val&param2=val2].
package llm

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/core/xdg"
)

// URI represents a parsed provider URI (scheme://model?params).
type URI struct {
	Scheme string
	Model  string
	Host   string            // optional, for local providers (host:port)
	Params map[string]string // query params
}

// ProviderConfig holds resolved provider settings after merge.
// Scheme and Host are populated by the registry when resolving a URI.
type ProviderConfig struct {
	Scheme  string
	Host    string
	APIKey  string
	BaseURL string
	Model   string
	Params  map[string]string
	Extras  map[string]any
}

// ResolvedConfig is the final merged configuration.
type ResolvedConfig struct {
	URI       URI
	Provider  ProviderConfig
	Fallbacks []string
}

// configFile mirrors the on-disk YAML structure.
type configFile struct {
	Default   string                        `yaml:"default"`
	Providers map[string]configFileProvider `yaml:"providers"`
	Fallback  []string                      `yaml:"fallback"`
}

// configFileProvider mirrors a single provider block in the YAML.
type configFileProvider struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
	// Any unknown keys land here.
	Extra map[string]any `yaml:",inline"`
}

// ParseURI parses a provider URI string into its components.
//
// Format: scheme://[host:port/]model[?param=val&param2=val2]
//
// The scheme is required. The model may be empty. Host:port is detected
// when the authority portion contains a colon followed by digits.
func ParseURI(raw string) (URI, error) {
	if raw == "" {
		return URI{}, fmt.Errorf("empty URI")
	}

	idx := strings.Index(raw, "://")
	if idx < 0 {
		return URI{}, fmt.Errorf("missing scheme in URI %q", raw)
	}

	scheme := raw[:idx]
	if scheme == "" {
		return URI{}, fmt.Errorf("empty scheme in URI %q", raw)
	}

	rest := raw[idx+3:] // everything after "://"

	// Split off query params.
	var params map[string]string
	if qIdx := strings.Index(rest, "?"); qIdx >= 0 {
		params = parseQuery(rest[qIdx+1:])
		rest = rest[:qIdx]
	}

	var host, model string

	// Detect host:port — the first path segment contains a colon
	// followed by digits (e.g. localhost:1234).
	if slash := strings.Index(rest, "/"); slash > 0 {
		candidate := rest[:slash]
		if h, p, err := net.SplitHostPort(candidate); err == nil && p != "" {
			host = net.JoinHostPort(h, p)
			model = rest[slash+1:]
		} else {
			model = rest
		}
	} else {
		model = rest
	}

	return URI{
		Scheme: scheme,
		Model:  model,
		Host:   host,
		Params: params,
	}, nil
}

// parseQuery splits key=val&key2=val2 into a map.
func parseQuery(q string) map[string]string {
	if q == "" {
		return nil
	}
	m := make(map[string]string)
	for _, pair := range strings.Split(q, "&") {
		k, v, _ := strings.Cut(pair, "=")
		if k != "" {
			m[k] = v
		}
	}
	return m
}

// LoadConfig resolves the full provider configuration by merging:
//
//  1. Config file values for the URI's scheme
//  2. URI values (model, params)
//  3. Environment variable overrides
//
// When uri is empty, LLM_PROVIDER env var or the config file's default
// field is used. Returns an error if no URI can be determined.
func LoadConfig(uri string) (ResolvedConfig, error) {
	// Load config file (best-effort).
	cf := loadConfigFile()

	// Resolve effective URI.
	effectiveURI := uri
	if effectiveURI == "" {
		if envProvider := os.Getenv("LLM_PROVIDER"); envProvider != "" {
			effectiveURI = envProvider
		} else if cf.Default != "" {
			effectiveURI = cf.Default
		} else {
			return ResolvedConfig{}, fmt.Errorf(
				"no URI provided and no default configured",
			)
		}
	}

	parsed, err := ParseURI(effectiveURI)
	if err != nil {
		return ResolvedConfig{}, err
	}

	// Layer 1: config file provider block.
	var pc ProviderConfig
	if fp, ok := cf.Providers[parsed.Scheme]; ok {
		pc.APIKey = fp.APIKey
		pc.BaseURL = fp.BaseURL
		pc.Model = fp.Model
		if len(fp.Extra) > 0 {
			pc.Extras = fp.Extra
		}
	}

	// Layer 2: URI overrides.
	if parsed.Model != "" {
		pc.Model = parsed.Model
	}
	if parsed.Host != "" && pc.BaseURL == "" {
		pc.BaseURL = "http://" + parsed.Host
	}
	// URI params override specific fields.
	if parsed.Params != nil {
		if v, ok := parsed.Params["base_url"]; ok {
			pc.BaseURL = v
		}
		if v, ok := parsed.Params["api_key"]; ok {
			pc.APIKey = v
		}
		// Store remaining params.
		if pc.Params == nil {
			pc.Params = make(map[string]string)
		}
		for k, v := range parsed.Params {
			pc.Params[k] = v
		}
	}

	// Layer 3: env var overrides.
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		pc.APIKey = v
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		pc.BaseURL = v
	}

	// Fallbacks: env > config file.
	var fallbacks []string
	if envFB := os.Getenv("LLM_FALLBACK"); envFB != "" {
		for _, f := range strings.Split(envFB, ",") {
			if s := strings.TrimSpace(f); s != "" {
				fallbacks = append(fallbacks, s)
			}
		}
	} else {
		fallbacks = cf.Fallback
	}

	return ResolvedConfig{
		URI:       parsed,
		Provider:  pc,
		Fallbacks: fallbacks,
	}, nil
}

// loadConfigFile reads {xdg.ConfigDir("hop")}/llm.yaml.
// Returns an empty configFile if the file does not exist or cannot be read.
func loadConfigFile() configFile {
	dir, err := xdg.ConfigDir("hop")
	if err != nil {
		return configFile{}
	}

	data, err := os.ReadFile(filepath.Join(dir, "llm.yaml"))
	if err != nil {
		return configFile{}
	}

	var cf configFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return configFile{}
	}
	return cf
}
