package bus

import (
	"fmt"
	"os"
	"strings"
)

// EnvSinkKind selects an env-configured Sink to attach to bus.New().
// Recognized values (case-insensitive):
//
//   - "jsonl" (with [EnvSinkPath] set) → [JSONLSink] writing JSONL to
//     the file at [EnvSinkPath]. Append mode; the file is created if
//     absent.
//
// Any other value (or an unset variable) disables the env-driven sink
// wiring; bus.New() returns the bare bus.
const EnvSinkKind = "KIT_BUS_SINK"

// EnvSinkPath is the destination path for env-configured file sinks
// (currently only "jsonl"). When [EnvSinkKind]=="jsonl" but
// [EnvSinkPath] is empty, the sink is skipped and a one-line warning
// is sent to [EnvSinkErrReporter] (if set) — silent misconfiguration
// would leave operators chasing missing events.
const EnvSinkPath = "KIT_BUS_SINK_PATH"

// envSinkErrFunc receives diagnostics from [envSinksFromEnv]
// (misconfiguration, open errors). Defaults to a no-op so the bus pkg
// stays stderr-quiet on import; tests and adopters override via
// [SetEnvSinkErrReporter].
var envSinkErrFunc ErrFunc = func(error) {}

// SetEnvSinkErrReporter installs fn as the receiver for env-sink
// diagnostics. Pass nil to reset to the no-op default. Safe to call
// at any time; the new function applies to subsequent bus.New() calls.
//
// Single-process-wide (no per-bus override) by design: the env sink is
// itself process-wide configuration, so the reporter follows the same
// scope.
func SetEnvSinkErrReporter(fn ErrFunc) {
	if fn == nil {
		envSinkErrFunc = func(error) {}
		return
	}
	envSinkErrFunc = fn
}

// envSinksFromEnv consults [EnvSinkKind] + [EnvSinkPath] and returns
// the sinks to attach to a freshly constructed bus. Returns a nil
// slice when no env sink is configured, so callers can skip the
// TeeBus wrap entirely in the common case (zero overhead).
//
// Misconfiguration (unknown kind, jsonl without path, file open
// failure) is reported via [envSinkErrFunc] and the sink is skipped —
// the bus must keep working even when an operator typoed an env var.
func envSinksFromEnv() []Sink {
	raw, ok := os.LookupEnv(EnvSinkKind)
	if !ok {
		return nil
	}
	kind := strings.ToLower(strings.TrimSpace(raw))
	if kind == "" {
		return nil
	}
	switch kind {
	case "jsonl":
		path := strings.TrimSpace(os.Getenv(EnvSinkPath))
		if path == "" {
			envSinkErrFunc(fmt.Errorf(
				"bus: %s=jsonl but %s is empty; skipping env sink",
				EnvSinkKind, EnvSinkPath))
			return nil
		}
		sink, err := NewJSONLSinkFile(path)
		if err != nil {
			envSinkErrFunc(fmt.Errorf(
				"bus: %s=jsonl: open %q: %w; skipping env sink",
				EnvSinkKind, path, err))
			return nil
		}
		return []Sink{sink}
	default:
		envSinkErrFunc(fmt.Errorf(
			"bus: unknown %s=%q (want \"jsonl\"); skipping env sink",
			EnvSinkKind, raw))
		return nil
	}
}
