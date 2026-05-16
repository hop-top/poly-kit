package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseOption customizes [ParseConfigArgs]. Add new options as
// [WithProjectMarker]-style helpers; the variadic shape mirrors
// [ReloadOption] and lets callers pass zero options for the
// historical default behavior.
type ParseOption func(*parseConfig)

type parseConfig struct {
	// projectMarker, when non-empty, is the relative path inside a
	// project root that holds the project's config file. A bare-
	// directory -c token resolves to <dir>/<projectMarker> instead of
	// being rejected. Example: ".rlz/config.yaml".
	projectMarker string
}

// WithProjectMarker tells [ParseConfigArgs] how to resolve a bare-
// directory -c token. When set, "-c /path/to/proj" expands to
// "-c /path/to/proj/<marker>" — the file MUST exist under the
// directory; a missing marker file is a clear error, not a silent
// drop. Empty marker is a no-op (preserves the legacy "directory ->
// error" behavior).
//
// Marker is a relative path; absolute paths are rejected as a
// programmer error so adopters don't accidentally configure a
// global file as the per-directory marker.
func WithProjectMarker(marker string) ParseOption {
	return func(c *parseConfig) { c.projectMarker = marker }
}

// ParseConfigArgs splits raw -c/--config tokens into two ordered groups:
// extra config file paths and value-override pairs.
//
// A token with an "=" sign is treated as an override pair; a token without is
// treated as a path to an additional config file. Paths are validated lightly:
// they must point at an existing readable file, otherwise the token is
// rejected as malformed. This avoids silently treating a typo'd "key" with no
// "=" as a phantom file path.
//
// Bare-directory tokens are rejected by default. When [WithProjectMarker] is
// passed, a bare-directory token resolves to <dir>/<marker> — convenient for
// "-c <project root>" UX where the per-project config file lives at a known
// relative path inside the project.
//
// Examples:
//
//	-c model=o3                     -> override {model: "o3"}
//	-c shell.policy.inherit=all     -> nested override
//	-c 'tags=["a","b"]'             -> structured value
//	-c /etc/tool/extra.yaml         -> extra file path (must exist)
//	-c flaky=*not-yaml              -> literal fallback ("*not-yaml")
//	-c /path/to/proj                -> error by default;
//	                                   resolves to /path/to/proj/<marker>
//	                                   when WithProjectMarker is set
//
// Both groups preserve the original argument order. Pairs are merged into a
// single nested map (later wins on conflict); paths are returned as-is for
// the loader to layer in order.
func ParseConfigArgs(args []string, opts ...ParseOption) (paths []string, overrides map[string]any, err error) {
	if len(args) == 0 {
		return nil, nil, nil
	}
	cfg := parseConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.projectMarker != "" && filepath.IsAbs(cfg.projectMarker) {
		return nil, nil, fmt.Errorf(
			"config: ParseConfigArgs: project marker %q must be a relative path",
			cfg.projectMarker)
	}
	overrides = map[string]any{}
	for _, a := range args {
		eq := strings.IndexByte(a, '=')
		if eq < 0 {
			// No '=' — treat as a path. Reject up front if it doesn't
			// resolve so users get an immediate error rather than a
			// silent skip later.
			info, statErr := os.Stat(a)
			if statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					return nil, nil, fmt.Errorf(
						"-c %q: not a key=value pair and no such file", a)
				}
				return nil, nil, fmt.Errorf("-c %q: %w", a, statErr)
			}
			if info.IsDir() {
				if cfg.projectMarker == "" {
					return nil, nil, fmt.Errorf(
						"-c %q: path is a directory, expected a file", a)
				}
				resolved := filepath.Join(a, cfg.projectMarker)
				markerInfo, mErr := os.Stat(resolved)
				if mErr != nil {
					if errors.Is(mErr, os.ErrNotExist) {
						return nil, nil, fmt.Errorf(
							"-c %q: directory has no project marker %q (resolved to %q)",
							a, cfg.projectMarker, resolved)
					}
					return nil, nil, fmt.Errorf(
						"-c %q: stat project marker %q: %w", a, resolved, mErr)
				}
				if markerInfo.IsDir() {
					return nil, nil, fmt.Errorf(
						"-c %q: project marker %q resolved to a directory, expected a file (%q)",
						a, cfg.projectMarker, resolved)
				}
				paths = append(paths, resolved)
				continue
			}
			paths = append(paths, a)
			continue
		}
		if eq == 0 {
			return nil, nil, fmt.Errorf("-c %q: empty key", a)
		}
		key := strings.TrimSpace(a[:eq])
		raw := a[eq+1:]
		if err := setDottedKey(overrides, key, parseValue(raw)); err != nil {
			return nil, nil, fmt.Errorf("-c %q: %w", a, err)
		}
	}
	if len(overrides) == 0 {
		overrides = nil
	}
	return paths, overrides, nil
}

// ParseOverrides is a thin wrapper around ParseConfigArgs that rejects path
// tokens. Use it when the caller explicitly only wants the override map and
// treats bare tokens as a parse error.
//
// Deprecated: prefer ParseConfigArgs which supports both tokens. Retained
// because internal callers still take the override-only path.
func ParseOverrides(pairs []string) (map[string]any, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := map[string]any{}
	for _, p := range pairs {
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("override %q: expected key=value", p)
		}
		key := strings.TrimSpace(p[:eq])
		raw := p[eq+1:]
		if err := setDottedKey(out, key, parseValue(raw)); err != nil {
			return nil, fmt.Errorf("override %q: %w", p, err)
		}
	}
	return out, nil
}

// parseValue attempts to parse raw as YAML. On any failure it returns the raw
// string verbatim — never an error. This makes the override syntax forgiving
// for unquoted values that happen to contain YAML-significant characters.
func parseValue(raw string) any {
	var v any
	if err := yaml.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	return v
}

// setDottedKey walks dst by the dotted segments of key, creating intermediate
// maps as needed, and writes val at the leaf. Conflicts (a non-map value where
// a map is required) return an error rather than silently clobbering.
func setDottedKey(dst map[string]any, key string, val any) error {
	if key == "" {
		return fmt.Errorf("empty key")
	}
	segs := strings.Split(key, ".")
	cur := dst
	for i, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg]
		if !ok {
			m := map[string]any{}
			cur[seg] = m
			cur = m
			continue
		}
		m, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("conflict at %q: expected map, got %T",
				strings.Join(segs[:i+1], "."), next)
		}
		cur = m
	}
	cur[segs[len(segs)-1]] = val
	return nil
}

// ApplyOverrides merges overrides onto dst. dst must be a non-nil pointer to a
// struct (the same shape passed to Load). The merge round-trips through YAML,
// reusing the existing yaml.v3 unmarshal rules so override semantics match
// file-loading semantics exactly: maps merge recursively, scalars and slices
// replace.
//
// A nil or empty overrides map is a no-op.
func ApplyOverrides(dst any, overrides map[string]any) error {
	if len(overrides) == 0 {
		return nil
	}
	data, err := yaml.Marshal(overrides)
	if err != nil {
		return fmt.Errorf("marshal overrides: %w", err)
	}
	if err := yaml.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("apply overrides: %w", err)
	}
	return nil
}
