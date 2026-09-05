// Package telemetry is the kit runtime telemetry emitter: it captures
// anonymous CLI usage signals (command path, exit code, duration) and
// publishes them on the kit bus for adopters that have opted in.
//
// The wire contract is mirrored by polyglot SDKs and a cross-language
// contract test diffs them.
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
// hook denies; the `kit telemetry` CLI ships the user-facing gate
// that flips it. No consent => no emit, regardless of Mode.
package telemetry
