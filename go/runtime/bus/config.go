package bus

import (
	"fmt"
	"os"
	"strings"
)

// EnvEnforce is the environment variable that overrides the bus
// enforcement mode. Recognized values (case-insensitive): "off",
// "warn", "strict". Any other value is ignored and the default
// (ModeWarn) is used.
const EnvEnforce = "KIT_BUS_ENFORCE"

// ConfigKeyEnforce is the kit/core/config key the bus reads to pick
// up its enforcement mode. The bus does not import the config
// package directly; callers thread a ConfigGetter to avoid a
// dependency cycle.
const ConfigKeyEnforce = "kit.bus.enforce"

// ConfigGetter is the minimal interface the bus needs to read its
// enforcement mode from a configuration source. It is satisfied by
// kit/core/config.Config (Get(key) (string, bool)) and by ad-hoc
// map-backed shims in tests.
type ConfigGetter interface {
	Get(key string) (string, bool)
}

// ModeFromString parses s into a [Mode]. Recognized values are
// "off", "warn", "strict" (case-insensitive, surrounding whitespace
// allowed). The empty string returns the default ([ModeWarn], nil).
// Any other value returns ModeWarn and a non-nil error so callers
// can decide whether to fall back to defaults or surface the
// problem.
func ModeFromString(s string) (Mode, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ModeWarn, nil
	}
	switch strings.ToLower(trimmed) {
	case "off":
		return ModeOff, nil
	case "warn":
		return ModeWarn, nil
	case "strict":
		return ModeStrict, nil
	default:
		return ModeWarn, fmt.Errorf("bus: unknown enforce mode %q (want off|warn|strict)", s)
	}
}

// ModeFromEnv reads [EnvEnforce] and returns the matching [Mode].
// If the variable is unset, empty, or contains an unrecognized
// value, ModeFromEnv returns the default [ModeWarn].
func ModeFromEnv() Mode {
	v, ok := os.LookupEnv(EnvEnforce)
	if !ok {
		return ModeWarn
	}
	m, err := ModeFromString(v)
	if err != nil {
		return ModeWarn
	}
	return m
}

// ModeFromConfig resolves the bus enforcement mode using the
// documented precedence:
//
//  1. getter.Get([ConfigKeyEnforce]) — when present and parseable
//  2. [EnvEnforce] — when set and parseable
//  3. default [ModeWarn]
//
// A nil getter is treated as "no config available" and the function
// falls through to the env step. Unparseable values at any layer
// fall through to the next layer rather than failing loudly; the
// bus is best-effort here so a bad config row doesn't take down
// callers.
func ModeFromConfig(getter ConfigGetter) Mode {
	if getter != nil {
		if v, ok := getter.Get(ConfigKeyEnforce); ok {
			if m, err := ModeFromString(v); err == nil {
				return m
			}
		}
	}
	return ModeFromEnv()
}

// WithEnforceFromEnv is a convenience option that reads the
// enforcement mode from [EnvEnforce] (via [ModeFromEnv]) at
// construction time. It is shorthand for
// WithEnforce(ModeFromEnv()) and follows the same precedence rules
// as [WithEnforce] — a later WithEnforce overrides an earlier
// WithEnforceFromEnv.
func WithEnforceFromEnv() Option {
	return WithEnforce(ModeFromEnv())
}
