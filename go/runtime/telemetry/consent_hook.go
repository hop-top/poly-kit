package telemetry

import (
	"context"
	"sync/atomic"
)

// ConsentHook is the seam between kit-telemetry and kit-consent. The
// emitter consults Granted before publishing any event; false denies
// emit regardless of Mode. The boolean is deliberately narrower than
// a State/decision_source enum because the cross-package contract
// only needs the emit-gate decision; richer state is internal to
// kit-consent.
type ConsentHook interface {
	Granted(ctx context.Context) bool
}

// denyHook is the default hook installed when no real ConsentHook has
// been wired. Default-deny so an upgrade never starts a telemetry
// stream by surprise.
type denyHook struct{}

// Granted always returns false. A nil-deref-safe permanent deny.
func (denyHook) Granted(context.Context) bool { return false }

// hookHolder wraps the active ConsentHook so atomic.Value always sees
// the same concrete type regardless of which implementation kit-consent
// (or a test) installs. atomic.Value panics on inconsistent stored
// types; wrapping in a fixed struct sidesteps that without falling back
// to a Mutex.
type hookHolder struct{ h ConsentHook }

// globalHook holds the active package-global ConsentHook. atomic.Value
// follows the same idiom as appPrefix in mode.go so SetConsentHook can
// be called from init() / cobra.OnInitialize without locking. The
// stored type is always hookHolder (type-stable per atomic.Value
// requirements) and the wrapped ConsentHook is never nil —
// SetConsentHook(nil) resets to denyHook{}, never stores nil.
var globalHook atomic.Value // hookHolder

func init() {
	globalHook.Store(hookHolder{h: denyHook{}})
}

// SetConsentHook installs the global consent hook. kit-consent calls
// this from cobra.OnInitialize during application startup. Passing
// nil resets to the default-deny hook so the emitter can always
// dereference the result without a nil check.
func SetConsentHook(h ConsentHook) {
	if h == nil {
		globalHook.Store(hookHolder{h: denyHook{}})
		return
	}
	globalHook.Store(hookHolder{h: h})
}

// CurrentConsentHook returns the active package-global hook. Always
// non-nil — the package init seeds globalHook with denyHook{} and
// SetConsentHook(nil) resets to the same default rather than storing
// nil.
func CurrentConsentHook() ConsentHook {
	v := globalHook.Load()
	if v == nil {
		// Defensive: init() seeds the slot, but if someone reordered
		// inits we still want a safe default rather than a nil-deref.
		return denyHook{}
	}
	holder, ok := v.(hookHolder)
	if !ok || holder.h == nil {
		return denyHook{}
	}
	return holder.h
}

// consentHookCtxKey is the private context key for per-invocation
// overrides. Unexported so external packages can't collide.
type consentHookCtxKey struct{}

// WithConsentHook returns a copy of ctx with the supplied hook
// installed as a per-context override. Tests use this to install a
// permissive hook without touching global state; production code
// generally should not. Mirrors WithMode's nil-ctx tolerance: a nil
// ctx is treated as context.Background().
func WithConsentHook(ctx context.Context, h ConsentHook) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, consentHookCtxKey{}, h)
}

// CurrentConsentHookFromContext returns the ctx-scoped hook installed
// via WithConsentHook, falling back to the package-global hook when
// none is set. Always returns non-nil. A nil ctx falls back to the
// global, matching CurrentModeFromContext.
func CurrentConsentHookFromContext(ctx context.Context) ConsentHook {
	if ctx == nil {
		return CurrentConsentHook()
	}
	if v, ok := ctx.Value(consentHookCtxKey{}).(ConsentHook); ok && v != nil {
		return v
	}
	return CurrentConsentHook()
}
