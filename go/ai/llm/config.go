// Package llm provides configuration resolution for LLM providers.
//
// [LoadConfig] merges llm.yaml, the environment and the URI, the URI
// highest. Provider URIs follow the format
// scheme://model[?param=val&param2=val2].
package llm

import (
	"context"
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
	Pool      []configFilePoolEntry         `yaml:"pool,omitempty"`
}

// configFileProvider mirrors a single provider block in the YAML.
type configFileProvider struct {
	APIKey string `yaml:"api_key"`
	// APIKeyEnv names the variable holding the key, for a key kept
	// under a name other than the provider's own.
	APIKeyEnv string `yaml:"api_key_env"`
	BaseURL   string `yaml:"base_url"`
	Model     string `yaml:"model"`
	// Any unknown keys land here.
	Extra map[string]any `yaml:",inline"`
}

// configFilePoolEntry mirrors a single pool entry in llm.yaml.
type configFilePoolEntry struct {
	Alias   string  `yaml:"alias,omitempty"`
	Scheme  string  `yaml:"scheme"`
	Model   string  `yaml:"model"`
	Enabled *bool   `yaml:"enabled,omitempty"` // tristate; missing = true
	Weight  float64 `yaml:"weight,omitempty"`  // default 1.0 when zero
}

// PoolEntry is a resolved pool member after defaults and disable overrides
// have been applied. Scheme+Model identify the model in aim's registry.
type PoolEntry struct {
	Alias   string
	Scheme  string
	Model   string
	Enabled bool
	Weight  float64
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

// LoadConfig resolves the full provider configuration for uri. Each
// field, highest precedence first:
//
//   - APIKey: as [ResolveAPIKey] resolves it with no secret store — the
//     URI's api_key param; llm.yaml api_key, then the variable
//     api_key_env names; the provider's key variables; LLM_API_KEY for
//     a required key only. A key found nowhere leaves APIKey empty:
//     reporting it is the caller's ([ApplyAPIKey] returns the error).
//   - BaseURL: the URI's base_url param; the URI's host
//     ("http://host:port"); LLM_BASE_URL; llm.yaml base_url. The
//     adapter's own default is left to [Resolve] and the adapter.
//   - Model: the URI's model; llm.yaml model.
//   - Params: the URI's query params. Extras: the llm.yaml block's
//     other keys.
//
// The llm.yaml block is the one [ProviderSettingsFor] finds, so an
// alias ("fireworks-ai") reads its provider's block.
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

	ctx := context.Background()

	// The config file block configuring the scheme, an alias's
	// included (see ProviderSettingsFor).
	var pc ProviderConfig
	if fp, _, ok := findBlock(cf, DefaultRegistry.blockNamesFor(ctx, parsed.Scheme)); ok {
		pc.BaseURL = fp.BaseURL
		pc.Model = fp.Model
		if len(fp.Extra) > 0 {
			pc.Extras = fp.Extra
		}
	}

	// The key resolves as ApplyAPIKey resolves it. With no store the
	// only error is a missing key, which is the caller's to report.
	key, _ := DefaultRegistry.resolveKey(ctx, nil, parsed)
	pc.APIKey = key.Value

	if parsed.Model != "" {
		pc.Model = parsed.Model
	}

	// Base URL: the URI's own choice outranks the environment, which
	// outranks the file, as in Resolve.
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		pc.BaseURL = v
	}
	if parsed.Host != "" {
		pc.BaseURL = "http://" + parsed.Host
	}
	if v, ok := parsed.Params["base_url"]; ok {
		pc.BaseURL = v
	}
	if parsed.Params != nil {
		pc.Params = make(map[string]string, len(parsed.Params))
		for k, v := range parsed.Params {
			pc.Params[k] = v
		}
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

// LoadPool reads llm.yaml's pool block, applies defaults, then layers the
// LLM_POOL_DISABLE env override on top. Returns nil, nil when the file has
// no pool block — callers treat empty pool as "accept everything from the
// registry" rather than "deny everything".
//
// LLM_POOL_DISABLE is a comma-separated list of identifiers. An entry is
// disabled when its Alias OR its "<Scheme>:<Model>" form appears in the
// list (both matches are case-sensitive exact).
func LoadPool() ([]PoolEntry, error) {
	cf := loadConfigFile()
	if len(cf.Pool) == 0 {
		return nil, nil
	}

	entries := make([]PoolEntry, 0, len(cf.Pool))
	for _, raw := range cf.Pool {
		entries = append(entries, resolvePoolEntry(raw))
	}

	if env := os.Getenv("LLM_POOL_DISABLE"); env != "" {
		entries = ResolvePool(entries, splitCSV(env))
	}

	return entries, nil
}

// ResolvePool returns a copy of entries with disables from cliDisable applied.
// Match rule mirrors LLM_POOL_DISABLE: Alias OR "<Scheme>:<Model>", case-
// sensitive exact. Useful for downstream CLIs that already parsed flags.
func ResolvePool(entries []PoolEntry, cliDisable []string) []PoolEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]PoolEntry, len(entries))
	copy(out, entries)
	if len(cliDisable) == 0 {
		return out
	}
	disable := make(map[string]struct{}, len(cliDisable))
	for _, d := range cliDisable {
		d = strings.TrimSpace(d)
		if d != "" {
			disable[d] = struct{}{}
		}
	}
	for i := range out {
		key := out[i].Scheme + ":" + out[i].Model
		if _, hit := disable[out[i].Alias]; hit && out[i].Alias != "" {
			out[i].Enabled = false
			continue
		}
		if _, hit := disable[key]; hit {
			out[i].Enabled = false
		}
	}
	return out
}

// resolvePoolEntry applies tristate Enabled and zero-weight defaults to a
// raw yaml entry.
func resolvePoolEntry(raw configFilePoolEntry) PoolEntry {
	enabled := true
	if raw.Enabled != nil {
		enabled = *raw.Enabled
	}
	weight := raw.Weight
	if weight == 0 {
		weight = 1.0
	}
	return PoolEntry{
		Alias:   raw.Alias,
		Scheme:  raw.Scheme,
		Model:   raw.Model,
		Enabled: enabled,
		Weight:  weight,
	}
}

// splitCSV parses a comma-separated list, trimming whitespace and dropping
// empty fields.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
