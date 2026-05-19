package telemetry

import (
	"encoding/hex"
	"errors"
	"time"
)

// SchemaVersion is the canonical wire schema version for telemetry
// events. It is a string per ADR-0035 (decision #7): weakly-typed SDKs
// round-trip int vs float vs string inconsistently, so the wire type is
// fixed at string. Future major bumps go "2", "3", ...
const SchemaVersion = "1"

// SDKLang identifies the emitter SDK in the event envelope. Go events
// stamp "go"; the polyglot SDKs ("py", "ts", "rs", "php") fill in their
// own values when they emit. The cross-language contract test (track
// sdk-telemetry, T-0709) diffs the wire shape across emitter languages.
const SDKLang = "go"

// installIDHexLen is the expected length of a rendered installation_id.
// 32 raw bytes -> SHA-256 -> 64 lowercase hex chars (ADR-0035 #4).
const installIDHexLen = 64

// Event is the on-wire telemetry payload published on
// `kit.telemetry.event.recorded` (or `<app>.telemetry.event.recorded`
// for adopters that set WithTopicPrefix). Field order, JSON tags, and
// `omitempty` placement are part of the contract — they are diffed by
// the cross-language contract test (sdk-telemetry T-0709).
//
// Anon tier (Mode == "anon"): Args and Flags MUST be empty. The emitter
// defensively strips them before publish even if a caller populated
// them (ADR-0035 #6). Validate enforces the schema, not the tier rule.
//
// Full tier (Mode == "full"): Args is the post-redact argv tail and
// Flags maps flag-name to its post-redact value. Flag KEYS are
// preserved verbatim; only VALUES go through redact.
//
// stdout / stderr are NEVER captured at any tier — that is an
// observability-platform job, not a telemetry job.
type Event struct {
	SchemaVersion  string            `json:"schema_version"`
	SDKLang        string            `json:"sdk_lang"`
	SDKVersion     string            `json:"sdk_version,omitempty"`
	InstallationID string            `json:"installation_id"`
	Mode           string            `json:"mode"` // "anon" | "full" — never "off"
	CommandPath    []string          `json:"command_path"`
	ExitCode       int               `json:"exit_code"`
	DurationMS     int64             `json:"duration_ms"`
	OccurredAt     time.Time         `json:"occurred_at"`
	KitVersion     string            `json:"kit_version,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Flags          map[string]string `json:"flags,omitempty"`
	TraceID        string            `json:"trace_id,omitempty"`
}

// Sentinel errors returned by Validate. Callers MUST use errors.Is for
// matching; do not string-match the message.
var (
	// ErrSchemaVersion means the event's SchemaVersion does not match
	// the wire constant. Either the producer is from a future kit or
	// the field was zero-valued.
	ErrSchemaVersion = errors.New("event: bad schema_version")
	// ErrInstallID means the installation_id is empty or not the
	// expected 64-char lowercase hex SHA-256 digest.
	ErrInstallID = errors.New("event: bad installation_id")
	// ErrMode means the mode field is not "anon" or "full". "off" is
	// invalid here: the emitter short-circuits before Validate runs,
	// so an "off"-tagged event is a producer bug.
	ErrMode = errors.New("event: bad mode")
	// ErrCommandPath means CommandPath is empty. Every event names a
	// command (argv0 at minimum).
	ErrCommandPath = errors.New("event: command_path required")
	// ErrOccurredAt means OccurredAt is the zero time. The emitter
	// stamps OccurredAt before calling Validate; a zero value here
	// signals an unstamped event.
	ErrOccurredAt = errors.New("event: occurred_at required")
	// ErrSDKLang means sdk_lang was not set. Producers MUST populate
	// it (Go canonical events use SDKLang = "go").
	ErrSDKLang = errors.New("event: sdk_lang required")
)

// Validate returns nil iff e is well-formed for emission on the bus.
// The emitter calls Validate immediately before publish; failures
// surface as a returned error from Emitter.Record (no panic, no drop
// without notice).
//
// Validate enforces the SCHEMA contract. Tier-specific rules (Anon
// MUST NOT include Args/Flags) are NOT enforced here — the emitter
// handles tier semantics earlier in the pipeline.
func (e Event) Validate() error {
	if e.SchemaVersion != SchemaVersion {
		return ErrSchemaVersion
	}
	if e.SDKLang == "" {
		return ErrSDKLang
	}
	if !validInstallID(e.InstallationID) {
		return ErrInstallID
	}
	if e.Mode != "anon" && e.Mode != "full" {
		return ErrMode
	}
	if len(e.CommandPath) == 0 {
		return ErrCommandPath
	}
	if e.OccurredAt.IsZero() {
		return ErrOccurredAt
	}
	return nil
}

// validInstallID reports whether s is the expected 64-char lowercase
// hex SHA-256 digest. We accept exactly the format Hash() produces in
// installid.go; uppercase hex or a 32-byte raw string is rejected.
func validInstallID(s string) bool {
	if len(s) != installIDHexLen {
		return false
	}
	// hex.DecodeString accepts upper or lower case; enforce lowercase
	// explicitly to match the on-read derivation contract.
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
