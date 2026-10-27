// Package consent implements per-capability consent grants and the
// persisted telemetry consent state machine.
//
// Grants (see Grants, Grant, GrantStore) answer "may this subject use
// this capability now": a capability id such as io.screen.capture, a
// subject (empty for any), allow or deny, and a duration of once,
// session, until or always. Request asks a caller-supplied Prompter
// when there is no standing decision; Use gates every tap and audits
// it; every path fails closed. Refusals render on the kit exit
// taxonomy through RefusedError.AsCLIError. The cross-language
// contract is contracts/consent-v1.
//
// Telemetry consent is the CapTelemetry capability. Its record keeps
// the original on-disk schema (state, decided_at, prompt_version,
// decision_source) under <XDG_CONFIG_HOME>/kit/config.yaml at the
// kit.telemetry.consent partition; a legacy
// <XDG_CONFIG_HOME>/kit/telemetry.yaml (bare telemetry.consent) is
// read as a fallback, and Clear removes the record from both files.
//
// The wire-level contract with kit-telemetry is the ConsentHook
// interface — kit-telemetry owns the interface as Granted(ctx) bool.
// NewHook builds an adapter from any Store. The surrounding policy
// (default-deny, DO_NOT_TRACK precedence, prompt_version semantics,
// decision_source taxonomy) is implemented here as the persistence +
// adapter slice; the precedence chain itself is resolved by callers
// further up the stack.
package consent

import "time"

// State is the persisted consent decision state. The wire vocabulary
// (granted, denied) matches both the YAML on-disk value and the
// KIT_TELEMETRY_CONSENT env var vocabulary — one set of strings
// everywhere.
type State string

const (
	// StateUnknown is never written to disk. It is the in-memory
	// sentinel returned for missing or absent persisted decisions so
	// callers can distinguish "no record yet" from "actively denied".
	StateUnknown State = "unknown"

	// StateGranted means the user has affirmatively consented to
	// telemetry collection. ConsentHook.Granted returns true only when
	// the persisted state is StateGranted.
	StateGranted State = "granted"

	// StateDenied means the user has affirmatively refused telemetry,
	// or the default-deny branch applied (non-TTY cold start). Same
	// on-disk shape either way; decision_source disambiguates the
	// reason.
	StateDenied State = "denied"
)

// DecisionSource records how the consent decision was reached. The
// field is mandatory on every persisted decision so `kit telemetry
// status` can answer "why am I in this state" in a single read.
type DecisionSource string

const (
	// SourcePrompt — user answered the interactive TTY prompt.
	SourcePrompt DecisionSource = "prompt"

	// SourceFlag — user passed --telemetry=on|off (or enable/disable
	// subcommand).
	SourceFlag DecisionSource = "flag"

	// SourceEnv — KIT_TELEMETRY_CONSENT was set in the environment at
	// decision time.
	SourceEnv DecisionSource = "env"

	// SourceConfig — default applied (non-TTY auto-deny, or seeded
	// configuration).
	SourceConfig DecisionSource = "config"
)

// Decision is the value object describing a single persisted consent
// state. Every field is recorded on disk so the audit trail is
// complete; the cross-package ConsentHook returns a plain bool
// (Granted) derived from State.
type Decision struct {
	State          State          `yaml:"state"           json:"state"`
	DecidedAt      time.Time      `yaml:"decided_at"      json:"decided_at"`
	PromptVersion  int            `yaml:"prompt_version"  json:"prompt_version"`
	DecisionSource DecisionSource `yaml:"decision_source" json:"decision_source"`
}

// Granted is shorthand for d.State == StateGranted. ConsentHook
// adapters call this directly; production code generally goes through
// the hook so the consent path is always the same code shape.
func (d Decision) Granted() bool {
	return d.State == StateGranted
}
