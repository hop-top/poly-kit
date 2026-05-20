package consent

import (
	"context"

	"hop.top/kit/go/runtime/telemetry"
)

// storeHook adapts a Store to telemetry.ConsentHook. Default-deny on
// any read failure: a corrupt or unreadable consent file MUST NOT cause
// the emitter to default-allow. The cross-package contract is a plain
// bool and the surface is narrower than the internal State enum on
// purpose.
//
// Per-batch semantic: kit-telemetry's emitter currently consults the
// hook per-event, but sinks that batch events MUST re-check Granted on
// each batch flush — a decision flipped to denied between Record and
// Send is the kind of edge case the boolean is meant to handle. That
// re-check lives in the sink (kit-telemetry), not here; this adapter
// is stateless and cheap to call.
type storeHook struct {
	s Store
}

// Granted reads the current decision and returns d.Granted(). Read
// errors are swallowed and reported as false — the audit trail for the
// failure lives one layer up (the CLI / status command surfaces "why").
// We deliberately do not log here: this is a hot-path call from the
// emitter, and the right place to expose persistence failures is the
// reload / status surface, not every emit attempt.
func (h storeHook) Granted(ctx context.Context) bool {
	d, err := h.s.Get(ctx)
	if err != nil {
		return false
	}
	return d.Granted()
}

// NewHook returns a telemetry.ConsentHook backed by the given Store.
// Install it into kit-telemetry via telemetry.SetConsentHook during
// application bootstrap (typically cobra.OnInitialize), or call
// Install() to do both in one step.
//
// Passing a nil Store panics. We fail fast on the programming error
// rather than silently default-deny: a nil Store means a wiring bug,
// and surfacing it at construction time is cheaper than discovering
// every emit silently dropped at runtime. The package-level default-
// deny hook already covers the "no hook installed" case.
func NewHook(s Store) telemetry.ConsentHook {
	if s == nil {
		panic("consent: NewHook called with nil Store")
	}
	return storeHook{s: s}
}

// Install constructs the default FileStore and installs a ConsentHook
// backed by it into the kit-telemetry emitter via
// telemetry.SetConsentHook. Returns the Store so callers (e.g. `kit
// telemetry status`) can read/write the persisted decision without
// re-resolving the path themselves.
//
// Call from main.init() or cobra.OnInitialize before any telemetry
// emission can happen. Idempotent — re-installing simply swaps the
// active hook via atomic.Value, no leak.
//
// If FileStore construction fails (e.g. XDG path resolution error),
// Install returns the error and leaves the package-level default-deny
// hook in place. Telemetry stays inert on failure, which is the safe
// default: a partial install must never accidentally enable emission.
func Install() (Store, error) {
	s, err := NewFileStore()
	if err != nil {
		return nil, err
	}
	telemetry.SetConsentHook(NewHook(s))
	return s, nil
}
