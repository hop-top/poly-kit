package consent

import "context"

// Store is the persistence interface backing the on-disk consent file.
// Implementations read and write the telemetry.consent partition of a
// kit AppConfig YAML; the canonical FileStore lives at
// <XDG_CONFIG_HOME>/kit/telemetry.yaml.
//
// All methods take a context to leave room for future I/O cancellation
// (e.g. reads over slow network-mounted homes); the current FileStore
// performs synchronous local disk I/O and does not honor ctx
// cancellation mid-syscall — it only checks ctx.Err on entry.
type Store interface {
	// Get returns the current decision. Missing file or absent
	// telemetry.consent block yields Decision{State: StateUnknown}
	// with a nil error so callers can branch on Unknown without
	// pre-checking fs.ErrNotExist. Malformed YAML on disk returns an
	// error — silently treating corruption as "no decision" would
	// mask real bugs in the persistence layer.
	Get(ctx context.Context) (Decision, error)

	// Set persists the decision atomically. Other top-level keys in
	// the file (this YAML is the kit AppConfig, not consent-only)
	// are preserved verbatim. Implementations MUST write to a *.tmp
	// sibling and rename, with file perms 0600.
	Set(ctx context.Context, d Decision) error

	// Clear resets the persisted decision to StateUnknown by removing
	// the telemetry.consent block from the file. Used by `kit
	// telemetry reset`. Other top-level keys are preserved. If no
	// file exists, Clear is a no-op (success).
	Clear(ctx context.Context) error
}
