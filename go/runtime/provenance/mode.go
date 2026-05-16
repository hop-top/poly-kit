package provenance

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
)

// Mode controls the package's runtime strictness. Three tiers:
//
//   - ModeOff: no checks; wrappers behave as plain pass-throughs;
//     Render emits plain JSON (no envelope). Default.
//   - ModeWarn: track Provenance; log warnings to stderr on missing
//     entries; emit anyway.
//   - ModeStrict: track Provenance; return *output.Error{Code:
//     "PROVENANCE_MISSING", ExitCode: 6} from Render on missing
//     entries; nothing hits stdout.
//
// Adopters call SetMode(ModeWarn) in main.init() to dogfood; flip to
// ModeStrict once the warnings are clean. WithMode overrides
// per-context for a specific invocation.
type Mode int32

const (
	// ModeOff disables provenance checking. Default.
	ModeOff Mode = iota
	// ModeWarn records and warns on stderr.
	ModeWarn
	// ModeStrict refuses to emit on missing provenance.
	ModeStrict
)

// String returns the Mode name (matching the env var token).
func (m Mode) String() string {
	switch m {
	case ModeOff:
		return "off"
	case ModeWarn:
		return "warn"
	case ModeStrict:
		return "strict"
	default:
		return "unknown"
	}
}

// ParseMode maps a string token (case-insensitive: "off", "warn",
// "strict") to a Mode. Unknown tokens map to ModeOff + false.
func ParseMode(s string) (Mode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "off":
		return ModeOff, true
	case "warn":
		return ModeWarn, true
	case "strict":
		return ModeStrict, true
	default:
		return ModeOff, false
	}
}

// globalMode is the package-global Mode. atomic.Int32 so SetMode can
// be called from init() without locking.
var globalMode atomic.Int32

// envModeOnce guards the one-shot env-var read. KIT_PROVENANCE_MODE
// is consulted on first CurrentMode call when SetMode has not run.
var envModeApplied atomic.Bool

// SetMode sets the package-global mode. ModeOff is the default. Call
// from init() to lock in for the program. Reentrant; the last call
// wins.
func SetMode(m Mode) {
	envModeApplied.Store(true) // bypass env-var application
	globalMode.Store(int32(m))
}

// CurrentMode reports the active package-global mode. On first call
// (or before SetMode has run) the env var KIT_PROVENANCE_MODE is
// consulted; "off"/"warn"/"strict" tokens are honored.
func CurrentMode() Mode {
	if envModeApplied.CompareAndSwap(false, true) {
		if v, ok := ParseMode(os.Getenv("KIT_PROVENANCE_MODE")); ok && v != ModeOff {
			globalMode.Store(int32(v))
		}
	}
	return Mode(globalMode.Load())
}

// modeCtxKey is the private context key for per-invocation overrides.
type modeCtxKey struct{}

// WithMode returns a copy of ctx with mode override m. Useful for
// per-request mode changes (e.g., strict in --strict invocations only).
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
