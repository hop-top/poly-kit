package provenance

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Tracker is the per-context provenance recorder. It accumulates
// Provenance entries keyed by JSON-pointer path (RFC 6901) for later
// validation at emit time.
//
// Construct via NewTracker or rely on the no-op fallback returned by
// Track(ctx) when no Tracker is installed. The kit cli middleware
// installs a Tracker on the root context for every command invocation
// once the auto-install follow-up lands; library code retrieves it via
// Track(ctx). A nil context or a context with no installed Tracker
// yields a no-op Tracker — recording is best-effort, never panicking.
type Tracker struct {
	mu      sync.RWMutex
	entries map[string]Provenance
	// invalid records validation errors per path (lazily reported by
	// Verify; recording the same path twice with different errors is
	// last-write-wins).
	invalid map[string]error
}

// NewTracker returns a fresh, empty Tracker.
func NewTracker() *Tracker {
	return &Tracker{
		entries: make(map[string]Provenance),
		invalid: make(map[string]error),
	}
}

// trackerCtxKey is a private context key type. Unexported so no other
// package can shadow or collide.
type trackerCtxKey struct{}

// WithTracker installs t on ctx. Typically called by the kit cli
// middleware once per invocation; library code rarely needs it.
//
// A nil t clears any inherited Tracker on the returned context (rare;
// useful when re-entering untracked code from inside a tracked scope).
func WithTracker(ctx context.Context, t *Tracker) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, trackerCtxKey{}, t)
}

// Track returns the Tracker installed on ctx, or a no-op Tracker if
// none. Never returns nil; safe to call without checking ctx.
//
// The "no-op" Tracker is a fresh tracker that discards entries (or
// keeps them locally, doesn't matter — the package's Render path only
// consults the Tracker installed via WithTracker on the same ctx). The
// helper just guarantees library code never panics on a nil Tracker.
func Track(ctx context.Context) *Tracker {
	if ctx == nil {
		return noopTracker()
	}
	v, ok := ctx.Value(trackerCtxKey{}).(*Tracker)
	if !ok || v == nil {
		return noopTracker()
	}
	return v
}

// noopTracker returns a per-call fresh Tracker. Callers writing to it
// will not affect any other code path. Allocated lazily.
//
// Note: we intentionally do not return a shared package-global no-op
// Tracker because that would let concurrent writes race on its map.
// One fresh tracker per Track() call when none is installed.
func noopTracker() *Tracker { return NewTracker() }

// Synthesize records that the value at JSON-pointer path was inferred,
// derived, or defaulted. SchemaVersion is filled in when empty;
// Source defaults to SourceInferred. The Provenance is validated;
// invalid records are remembered and surfaced at Verify time.
//
// Synthesize returns the validation error eagerly for the caller's
// benefit, but the error is also stored against path so Verify can
// report it later (in case the caller swallows the immediate error).
func (t *Tracker) Synthesize(path string, prov Provenance) error {
	prov = prov.fillDefaults()
	if prov.Source == "" {
		prov.Source = SourceInferred
	}
	return t.record(path, prov)
}

// Cache records that the value at JSON-pointer path was served from
// cache. Same semantics as Synthesize; defaults Source to SourceCached.
func (t *Tracker) Cache(path string, prov Provenance) error {
	prov = prov.fillDefaults()
	if prov.Source == "" {
		prov.Source = SourceCached
	}
	if prov.Source == SourceDefaulted {
		err := fmt.Errorf("Tracker.Cache: SourceDefaulted rejected; use Synthesize")
		t.mu.Lock()
		t.invalid[path] = err
		t.mu.Unlock()
		return err
	}
	return t.record(path, prov)
}

// Authoritative is the rare explicit-record case. Adopters who want to
// emit a provenance entry for a plain-T (no wrapper) field can call
// this; it stamps Source=SourceAuthoritative.
func (t *Tracker) Authoritative(path string, prov Provenance) error {
	prov = prov.fillDefaults()
	if prov.Source == "" {
		prov.Source = SourceAuthoritative
	}
	return t.record(path, prov)
}

// Record is the dispatch entry point used by source wrappers. It
// routes to Synthesize / Cache / Authoritative based on tier; an
// invalid tier falls back to Authoritative.
func (t *Tracker) Record(tier SourceTier, path string, prov Provenance) error {
	switch tier {
	case SourceCached:
		return t.Cache(path, prov)
	case SourceInferred, SourceDefaulted:
		return t.Synthesize(path, prov)
	default:
		return t.Authoritative(path, prov)
	}
}

func (t *Tracker) record(path string, prov Provenance) error {
	if path == "" {
		err := fmt.Errorf("provenance: empty path")
		t.mu.Lock()
		t.invalid[""] = err
		t.mu.Unlock()
		return err
	}
	if err := prov.Validate(); err != nil {
		t.mu.Lock()
		t.invalid[path] = err
		t.mu.Unlock()
		return err
	}
	t.mu.Lock()
	t.entries[path] = prov
	delete(t.invalid, path)
	t.mu.Unlock()
	return nil
}

// Lookup returns the Provenance recorded for path and whether one
// exists. Used by Verify / Render.
func (t *Tracker) Lookup(path string) (Provenance, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.entries[path]
	return p, ok
}

// Snapshot returns a defensive copy of the recorded provenance map.
// Used by Render at emit time and by tests.
func (t *Tracker) Snapshot() map[string]Provenance {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]Provenance, len(t.entries))
	for k, v := range t.entries {
		out[k] = v
	}
	return out
}

// Paths returns the sorted list of recorded JSON-pointer paths. Useful
// for tests + diagnostics.
func (t *Tracker) Paths() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.entries))
	for k := range t.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// InvalidPaths returns the sorted list of paths whose recorded
// Provenance failed Validate(). Surfaced at Verify time.
func (t *Tracker) InvalidPaths() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.invalid))
	for k := range t.invalid {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// invalidError returns the validation error recorded for path, if any.
func (t *Tracker) invalidError(path string) (error, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.invalid[path]
	return e, ok
}
