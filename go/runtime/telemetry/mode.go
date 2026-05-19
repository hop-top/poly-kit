package telemetry

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// Mode controls how much detail the telemetry emitter ships. Three
// tiers, mirroring go/runtime/provenance/mode.go for muscle memory:
//
//   - ModeOff:  default; emit is a zero-cost no-op.
//   - ModeAnon: emit installation_id + command_path + exit_code +
//     duration_ms + occurred_at + kit_version + sdk_lang/version.
//   - ModeFull: ModeAnon plus args + flags, both AFTER redact.
//
// The semantics differ from provenance (which gates VALIDITY); here we
// gate WHAT-WE-EMIT. The idiom — atomic global + one-shot env read +
// per-context override — is identical so adopters learn the pattern
// once. See ADR-0035 for the canonical precedence rules.
type Mode int32

const (
	// ModeOff disables telemetry emission. Default.
	ModeOff Mode = iota
	// ModeAnon emits the anonymous payload tier.
	ModeAnon
	// ModeFull emits the full payload tier (args/flags, post-redact).
	ModeFull
)

// String returns the Mode name (matching the env var token).
func (m Mode) String() string {
	switch m {
	case ModeOff:
		return "off"
	case ModeAnon:
		return "anon"
	case ModeFull:
		return "full"
	default:
		return "unknown"
	}
}

// ParseMode maps a string token (case-insensitive: "off", "anon",
// "full") to a Mode. Empty input is treated as ModeOff + true so an
// unset env var doesn't masquerade as an error. Unknown tokens map to
// ModeOff + false so callers can distinguish "operator typoed the env
// var" from "operator opted out".
func ParseMode(s string) (Mode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "off":
		return ModeOff, true
	case "anon":
		return ModeAnon, true
	case "full":
		return ModeFull, true
	default:
		return ModeOff, false
	}
}

// globalMode is the package-global Mode. atomic.Int32 so SetMode can
// be called from init() without locking.
var globalMode atomic.Int32

// envModeApplied guards the one-shot env-var read. After SetMode or
// the first CurrentMode call, the env vars are never consulted again.
var envModeApplied atomic.Bool

// appPrefix is the registered application name used to derive the
// <APP>_TELEMETRY_MODE env var name. atomic.Value holds a string so
// SetAppPrefix can be called from init() without locking. Empty
// string means no app override is configured.
var appPrefix atomic.Value // string

// envOnce serialises the one-shot env-var read inside CurrentMode so
// concurrent first-callers establish a happens-before edge with the
// goroutine that actually consults the env and stores the resolved
// Mode. Without it, CAS-losers would race the CAS-winner's Store and
// frequently observe the stale ModeOff default.
var envOnce sync.Once

// SetMode sets the package-global mode. ModeOff is the default. Call
// from init() to lock in for the program; subsequent CurrentMode
// calls bypass env-var reading entirely. Reentrant; the last call
// wins.
func SetMode(m Mode) {
	envModeApplied.Store(true) // bypass env-var application
	globalMode.Store(int32(m))
}

// CurrentMode reports the active package-global mode. On first call
// (and only if SetMode has not run) the env vars are consulted in
// precedence order: <APP>_TELEMETRY_MODE wins over KIT_TELEMETRY_MODE.
// After SetMode or the first env-read, the global is sticky.
func CurrentMode() Mode {
	envOnce.Do(func() {
		// Preserve the SetMode bypass: if SetMode ran first,
		// envModeApplied is already true and the inner CAS no-ops,
		// leaving the globalMode SetMode wrote intact.
		if envModeApplied.CompareAndSwap(false, true) {
			if v, ok := readEnvMode(); ok && v != ModeOff {
				globalMode.Store(int32(v))
			}
		}
	})
	return Mode(globalMode.Load())
}

// readEnvMode consults <APP>_TELEMETRY_MODE first (if app prefix is
// set) then KIT_TELEMETRY_MODE. First valid, non-Off hit wins. The
// boolean signals whether any var provided a usable value; on false
// callers should leave the global untouched (default ModeOff).
func readEnvMode() (Mode, bool) {
	if prefix := CurrentAppPrefix(); prefix != "" {
		envName := strings.ToUpper(prefix) + "_TELEMETRY_MODE"
		if raw := os.Getenv(envName); raw != "" {
			if v, ok := ParseMode(raw); ok {
				return v, true
			}
		}
	}
	if raw := os.Getenv("KIT_TELEMETRY_MODE"); raw != "" {
		if v, ok := ParseMode(raw); ok {
			return v, true
		}
	}
	return ModeOff, false
}

// SetAppPrefix registers the adopter's application name used to
// compose the <APP>_TELEMETRY_MODE env var (e.g. SetAppPrefix("spaced")
// reads SPACED_TELEMETRY_MODE). Call from init() before any
// CurrentMode call. Empty prefix disables the app-specific lookup,
// leaving only KIT_TELEMETRY_MODE.
func SetAppPrefix(prefix string) {
	appPrefix.Store(strings.TrimSpace(prefix))
}

// CurrentAppPrefix returns the registered application prefix, or ""
// if none has been set.
func CurrentAppPrefix() string {
	v := appPrefix.Load()
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// modeCtxKey is the private context key for per-invocation overrides.
type modeCtxKey struct{}

// WithMode returns a copy of ctx with mode override m. Useful for
// per-invocation overrides (e.g. a single command forcing ModeFull
// while the rest of the process emits ModeAnon).
// CurrentModeFromContext returns this override when present.
func WithMode(ctx context.Context, m Mode) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, modeCtxKey{}, m)
}

// CurrentModeFromContext returns the per-context override installed
// via WithMode, falling back to the package-global CurrentMode().
func CurrentModeFromContext(ctx context.Context) Mode {
	if ctx == nil {
		return CurrentMode()
	}
	if v, ok := ctx.Value(modeCtxKey{}).(Mode); ok {
		return v
	}
	return CurrentMode()
}

// resetForTest clears all package globals so a test can exercise the
// env-precedence machinery from a clean slate. Test-scope only — never
// call from production code. Unexported so external packages can't
// invoke it; mode_test.go uses it via internal test access.
func resetForTest() {
	globalMode.Store(int32(ModeOff))
	envModeApplied.Store(false)
	appPrefix.Store("")
	envOnce = sync.Once{}
}
