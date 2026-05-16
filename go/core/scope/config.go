// FromConfig loader: read declarative YAML policy from disk.

package scope

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"hop.top/kit/go/core/xdg"
)

// configFile is the on-disk YAML schema for scope.yaml.
type configFile struct {
	Mode  string   `yaml:"mode"`  // strict | warn | prompt
	Allow []string `yaml:"allow"` // patterns + macros
	Deny  []string `yaml:"deny"`  // patterns + macros
}

// FromConfig reads ~/.config/<tool>/scope.yaml (or the platform equivalent
// via xdg.RawConfigDir) and the system-wide /etc/xdg/<tool>/scope.yaml,
// returning a Policy that combines them. Per-user file wins on Mode and is
// merged after system rules so per-user rules can override system rules
// (deny-wins still applies).
//
// A missing file is not an error: callers get scope.New() (empty Strict
// policy). A parse error or unrecognized mode IS an error.
//
// Macros expand at load time:
//
//	tool:config|data|cache|state|runtime|bin
//
// resolves to the corresponding ToolConfig/Data/Cache/State/Runtime/Bin
// helper for tool. Bare paths (with or without ~) pass through unchanged.
func FromConfig(tool string) (*Policy, error) {
	p := New()

	systemPath := filepath.Join("/etc", "xdg", tool, "scope.yaml")
	if err := mergeFile(p, systemPath, tool); err != nil {
		return nil, err
	}

	userDir, err := xdg.RawConfigDir(tool)
	if err == nil {
		userPath := filepath.Join(userDir, "scope.yaml")
		if err := mergeFile(p, userPath, tool); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// MustFromConfig wraps FromConfig and panics on error. A missing file is
// fine; only parse errors panic.
func MustFromConfig(tool string) *Policy {
	p, err := FromConfig(tool)
	if err != nil {
		panic(err)
	}
	return p
}

// mergeFile parses path (if it exists) and applies its rules to p.
func mergeFile(p *Policy, path, tool string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("scope: read %q: %w", path, err)
	}
	var cfg configFile
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("scope: parse %q: %w", path, err)
	}
	if cfg.Mode != "" {
		mode, err := parseMode(cfg.Mode)
		if err != nil {
			return fmt.Errorf("scope: %s: %w", path, err)
		}
		p.SetMode(mode)
	}
	if len(cfg.Allow) > 0 {
		patterns, err := expandMacros(cfg.Allow, tool)
		if err != nil {
			return fmt.Errorf("scope: %s: %w", path, err)
		}
		p.Allow(patterns...)
	}
	if len(cfg.Deny) > 0 {
		patterns, err := expandMacros(cfg.Deny, tool)
		if err != nil {
			return fmt.Errorf("scope: %s: %w", path, err)
		}
		p.Deny(patterns...)
	}
	return nil
}

func parseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "strict":
		return Strict, nil
	case "warn":
		return Warn, nil
	case "prompt":
		return Prompt, nil
	default:
		return Strict, fmt.Errorf("unknown mode %q (want strict|warn|prompt)", s)
	}
}

// expandMacros walks the entries and resolves "tool:<base>" macros to the
// corresponding ToolDir helper. Other entries are passed through as Patterns.
func expandMacros(entries []string, tool string) ([]Pattern, error) {
	out := make([]Pattern, 0, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e, "tool:") {
			out = append(out, Pattern(e))
			continue
		}
		base := strings.TrimPrefix(e, "tool:")
		patterns, err := resolveToolMacro(base, tool)
		if err != nil {
			return nil, err
		}
		out = append(out, patterns...)
	}
	return out, nil
}

func resolveToolMacro(base, tool string) ([]Pattern, error) {
	switch base {
	case "config":
		return ToolConfig(tool), nil
	case "data":
		return ToolData(tool), nil
	case "cache":
		return ToolCache(tool), nil
	case "state":
		return ToolState(tool), nil
	case "runtime":
		return ToolRuntime(tool), nil
	case "bin":
		return ToolBin(tool), nil
	default:
		return nil, fmt.Errorf("unknown tool macro %q (want config|data|cache|state|runtime|bin)", base)
	}
}
