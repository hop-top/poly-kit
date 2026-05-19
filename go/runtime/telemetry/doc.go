// Package telemetry is the kit runtime telemetry emitter: it captures
// anonymous CLI usage signals (command path, exit code, duration) and
// publishes them on the kit bus for adopters that have opted in.
//
// Canonical design lives in ADR-0035 (`docs/adr/0035-runtime-telemetry.md`).
// Read that before changing the wire contract — the schema is mirrored
// by polyglot SDKs and a cross-language contract test diffs them.
//
// # Three modes
//
// The emitter has three tiers, gated by Mode (see mode.go):
//
//   - ModeOff:  default; emit is a zero-cost no-op.
//   - ModeAnon: installation_id + command + exit + duration only.
//   - ModeFull: ModeAnon plus argv tail + flags, both AFTER redact.
//
// stdout and stderr are NEVER captured at any tier.
//
// # Consent gate
//
// Emission additionally requires a granted ConsentHook. The default
// hook denies; sibling track kit-consent ships the user-facing gate
// that flips it. No consent => no emit, regardless of Mode.
//
// # Sibling tracks
//
//   - kit-consent: the user-facing consent gate and rotation CLI.
//   - kit-telemetry-compliance: redact-check audit observer.
//   - cmdsurf-telemetry: first adopter (CLI command-surface analytics).
//   - sdk-telemetry: polyglot SDK mirrors of this wire contract.
package telemetry
