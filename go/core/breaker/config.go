// Declarative breaker.yaml loader. Tools and ops declare breakers in
// YAML rather than constructing them by hand; FromConfig reads
// ~/.config/<tool>/breaker.yaml (and the system-wide equivalent) and
// returns a name → Breaker map ready to wire into the tool.
//
// Schema:
//
//	breakers:
//	  file-writes:
//	    on_trip: halt           # halt | degrade | warn
//	    max_per_minute: 100
//	    max_bytes: 1073741824
//	    max_ops: 10000
//	    reset_after: 5m
//	  llm-calls:
//	    on_trip: degrade
//	    max_concurrent: 4
//	    timeout: 30s
//	    circuit:
//	      failure_threshold: 5
//	      success_threshold: 2
//	      delay: 30s
//
// Unknown keys are an error (typo guard).

package breaker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/core/xdg"
)

// configFile is the on-disk YAML schema.
type configFile struct {
	Breakers map[string]map[string]any `yaml:"breakers"`
}

// FromConfig reads breaker.yaml under the system + per-user XDG
// config dirs for tool, parses it, and constructs each declared
// breaker. Returns a map keyed by breaker name. A missing file is
// not an error: callers get an empty map.
func FromConfig(tool string) (map[string]Breaker, error) {
	out := map[string]Breaker{}

	systemPath := filepath.Join("/etc", "xdg", tool, "breaker.yaml")
	if err := mergeBreakerFile(systemPath, out); err != nil {
		return nil, err
	}

	userDir, err := xdg.RawConfigDir(tool)
	if err == nil {
		userPath := filepath.Join(userDir, "breaker.yaml")
		if err := mergeBreakerFile(userPath, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// MustFromConfig is the panic-on-error variant. A missing file is
// fine; only parse / construction errors panic.
func MustFromConfig(tool string) map[string]Breaker {
	m, err := FromConfig(tool)
	if err != nil {
		panic(err)
	}
	return m
}

// Apply constructs a single Breaker from a parsed map. Useful for
// tests and library callers that want to hand-build a config without
// going through disk.
func Apply(name string, cfg map[string]any) (Breaker, error) {
	opts, err := optionsFromMap(cfg)
	if err != nil {
		return nil, fmt.Errorf("breaker: apply %q: %w", name, err)
	}
	return New(name, opts...), nil
}

// mergeBreakerFile reads + parses path (no-op if missing) and adds
// each breaker into out. Later files (per-user) override earlier
// (system) entries by name.
func mergeBreakerFile(path string, out map[string]Breaker) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("breaker: read %q: %w", path, err)
	}
	var cfg configFile
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("breaker: parse %q: %w", path, err)
	}
	for name, m := range cfg.Breakers {
		// Per-user override semantics: drop any earlier registration
		// before re-applying so registerOrPanic doesn't fire.
		if _, ok := out[name]; ok {
			Unregister(name)
		}
		b, err := Apply(name, m)
		if err != nil {
			return fmt.Errorf("breaker: %s: %w", path, err)
		}
		out[name] = b
	}
	return nil
}

// optionsFromMap maps the parsed YAML keys to Option callbacks.
// Unknown keys produce an error so users catch typos at load time.
func optionsFromMap(m map[string]any) ([]Option, error) {
	var opts []Option

	for k, v := range m {
		switch k {
		case "on_trip":
			a, err := parseAction(v)
			if err != nil {
				return nil, err
			}
			opts = append(opts, OnTrip(a))

		case "max_per_minute":
			n, err := parseInt(v)
			if err != nil {
				return nil, fmt.Errorf("max_per_minute: %w", err)
			}
			opts = append(opts, MaxPerMinute(n))

		case "max_bytes":
			n, err := parseInt64(v)
			if err != nil {
				return nil, fmt.Errorf("max_bytes: %w", err)
			}
			opts = append(opts, MaxBytes(n))

		case "max_ops":
			n, err := parseInt64(v)
			if err != nil {
				return nil, fmt.Errorf("max_ops: %w", err)
			}
			opts = append(opts, MaxOps(n))

		case "max_concurrent":
			n, err := parseInt(v)
			if err != nil {
				return nil, fmt.Errorf("max_concurrent: %w", err)
			}
			opts = append(opts, MaxConcurrent(n))

		case "timeout":
			d, err := parseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("timeout: %w", err)
			}
			opts = append(opts, Timeout(d))

		case "reset_after":
			d, err := parseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("reset_after: %w", err)
			}
			opts = append(opts, ResetAfter(d))

		case "circuit":
			cm, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("circuit: expected map, got %T", v)
			}
			co, err := parseCircuit(cm)
			if err != nil {
				return nil, err
			}
			opts = append(opts, WithCircuit(co))

		default:
			return nil, fmt.Errorf("unknown key %q", k)
		}
	}
	return opts, nil
}

func parseAction(v any) (Action, error) {
	s, ok := v.(string)
	if !ok {
		return Halt, fmt.Errorf("on_trip: expected string, got %T", v)
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "halt":
		return Halt, nil
	case "degrade":
		return Degrade, nil
	case "warn":
		return Warn, nil
	default:
		return Halt, fmt.Errorf("on_trip: unknown action %q (want halt|degrade|warn)", s)
	}
}

func parseInt(v any) (int, error) {
	switch x := v.(type) {
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case float64:
		return int(x), nil
	default:
		return 0, fmt.Errorf("expected int, got %T", v)
	}
}

func parseInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	case float64:
		return int64(x), nil
	default:
		return 0, fmt.Errorf("expected int, got %T", v)
	}
}

func parseDuration(v any) (time.Duration, error) {
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("expected duration string, got %T", v)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", s, err)
	}
	return d, nil
}

func parseCircuit(m map[string]any) (CircuitOpts, error) {
	var co CircuitOpts
	for k, v := range m {
		switch k {
		case "failure_threshold":
			n, err := parseInt(v)
			if err != nil {
				return co, fmt.Errorf("circuit.failure_threshold: %w", err)
			}
			co.FailureThreshold = uint(n)
		case "success_threshold":
			n, err := parseInt(v)
			if err != nil {
				return co, fmt.Errorf("circuit.success_threshold: %w", err)
			}
			co.SuccessThreshold = uint(n)
		case "delay":
			d, err := parseDuration(v)
			if err != nil {
				return co, fmt.Errorf("circuit.delay: %w", err)
			}
			co.Delay = d
		default:
			return co, fmt.Errorf("circuit: unknown key %q", k)
		}
	}
	return co, nil
}
