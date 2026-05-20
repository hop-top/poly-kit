package cli

import (
	runtimetelemetry "hop.top/kit/go/runtime/telemetry"
)

// TelemetryConfig is the adopter-controlled, build-time configuration
// surface for kit telemetry. Distinct from the user-mutable consent
// state in <XDG_CONFIG_HOME>/kit/config.yaml: the user owns "should
// my events ship?"; the adopter owns "where do they ship to?", "do
// we ask?", and "what tier by default?".
//
// Two tiers, named for clarity:
//
//   - User config — runtime, mutable. Lives in config.yaml under
//     kit.telemetry.consent. Owned by the human running the binary.
//     Changed via `kit telemetry enable|disable|reset` or env overrides.
//
//   - Kit options (this struct) — compile-time, immutable. Baked into
//     the adopter's binary via cli.WithTelemetry(...). Changing them
//     requires a rebuild (or, for the endpoint URL, a fresh -ldflags
//     injection). The adopter owns these values.
//
// Adopters typically leave Endpoint empty and rely on the ldflag-
// injected runtimetelemetry.DefaultEndpoint. The release pipeline
// passes -ldflags="-X 'hop.top/kit/go/runtime/telemetry.DefaultEndpoint=$URL'"
// with $URL coming from a CI secret, so the production endpoint
// never lives in git.
type TelemetryConfig struct {
	// Endpoint is the HTTPS collector URL for shipped events. Optional:
	// if empty, callers should fall back to runtimetelemetry.ResolveEndpoint
	// to honor the env override and the ldflag-injected default. Set
	// this only when an adopter has a wire-time reason to override
	// both (e.g. a monorepo with sibling binaries that ship to
	// different collectors). The common case is "leave it empty,
	// rely on the ldflag default".
	Endpoint string

	// PromptOnFirstRun controls whether the kit-telemetry first-run
	// consent prompt is allowed to fire for this binary. Default
	// (false) means kit NEVER asks: the binary behaves as default-
	// deny silently and the user must explicitly opt in via
	// `kit telemetry enable` (if the adopter exposes the kit
	// telemetry subtree) or via env (`KIT_TELEMETRY_CONSENT=granted`).
	//
	// Set to true ONLY when the adopter intends for kit to prompt
	// from a known interactive surface — typically the binary's
	// own first-run wizard. The prompt itself still respects all
	// TTY / DO_NOT_TRACK / env-precedence rules; this knob only
	// authorizes the prompt to fire at all.
	PromptOnFirstRun bool

	// DefaultModeOnGrant is the emission tier kit assumes when
	// consent is granted (via prompt, flag, or env) but no
	// explicit mode is set. Zero value (ModeOff) keeps the binary
	// silent even after a grant — the operator must additionally
	// set KIT_TELEMETRY_MODE=anon (or =full) to start emission.
	//
	// Most adopters want ModeAnon here so a "yes, enable telemetry"
	// answer to the prompt actually produces events. Set ModeFull
	// only when the binary's redact config can demonstrably handle
	// argv/flag values without leaking secrets.
	DefaultModeOnGrant runtimetelemetry.Mode
}

// WithTelemetry stashes the adopter's telemetry kit-options on the
// CLI root. Mirrors the WithIdentity / WithPeers / WithStatus pattern:
// the config struct holds the policy; consumers read it from the
// Root (via Telemetry()) at the call sites that need it.
//
// kit itself does NOT auto-fire the consent prompt on every command;
// that decision belongs to the adopter's bootstrap (e.g. a first-
// run wizard, or the kit telemetry subtree's own prompt subcommand).
// TelemetryConfig is a typed declaration of intent, not a runtime
// trigger.
func WithTelemetry(cfg TelemetryConfig) func(*Root) {
	return func(r *Root) {
		r.telemetryCfg = &cfg
	}
}

// Telemetry returns the adopter's TelemetryConfig as configured via
// WithTelemetry, or the zero value when WithTelemetry was not used.
// Callers that want to distinguish "explicitly configured zero" from
// "never configured" can use HasTelemetry.
func (r *Root) Telemetry() TelemetryConfig {
	if r.telemetryCfg == nil {
		return TelemetryConfig{}
	}
	return *r.telemetryCfg
}

// HasTelemetry reports whether the adopter wired WithTelemetry on
// this Root. Useful for adopter bootstrap code that wants to skip
// kit-telemetry wiring entirely when no config was supplied.
func (r *Root) HasTelemetry() bool {
	return r.telemetryCfg != nil
}
