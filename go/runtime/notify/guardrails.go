// Package notify — guardrail integration convention (outbound sinks).
//
// guardrails.go is a load-bearing godoc anchor that locks down the
// integration convention every outbound reference sink in
// go/runtime/notify/sinks/ adopts. It declares no types, no
// constants, and no functions — sink subpackages declare their own
// package-local Option type and their own WithRedactor /
// WithBreaker constructors per the signatures documented below.
//
// # Guardrail integration convention (outbound sinks)
//
// Outbound sinks cross trust boundaries (network, OS shell-out) and
// must integrate with the kit guardrail layers per
// AGENTS.md §Guardrails. Every reference outbound sink (webhook,
// email, osnotify) MUST expose two options with the following
// shapes; sinks that do not cross a trust boundary (JSONLSink,
// StdoutSink — both in go/runtime/bus) are tracked separately by the
// redact-egress audit and are out of scope here.
//
//	// WithRedactor wraps the rendered payload through r before egress.
//	// Default nil = no-op; ops opt in via construction.
//	func WithRedactor(r *redact.Redactor) Option
//
//	// WithBreaker gates egress through b. Default nil = no-op; ops
//	// opt in via construction.
//	func WithBreaker(b breaker.Breaker) Option
//
// Each sink declares Option as its package-local type
// (`type Option func(*opts)`); these constructors live with the
// sink that owns them. There is no shared Option type exported from
// notify itself — that would force every sink into a single
// `*opts` shape and prevent per-sink configuration knobs (e.g.
// webhook headers, email recipients, osnotify icon).
//
// # Pipeline order inside Drain
//
// Every outbound sink composes the four stages in this order:
//
//	template render → redactor.Apply (or ApplyBytes) → breaker wrap → egress
//
// Concretely:
//
//   - webhook (sinks/webhook): Template.Render(event) → body bytes;
//     redactor.ApplyBytes(body) when set; HTTP request goes through
//     breaker.WrapHTTP(b, transport).RoundTrip; non-2xx returns an
//     error so RetrySink can act on it.
//   - email   (sinks/email):   subject + body templates rendered to
//     strings; redactor.Apply on each rendered string when set;
//     Mailer.Send invoked via breaker.WrapCtx(b, ctx, fn).
//   - osnotify (sinks/osnotify): title + text templates rendered to
//     strings; redactor.Apply on each rendered string when set;
//     runner.Run invoked via breaker.WrapCtx(b, ctx, fn).
//
// Redaction runs on rendered bytes/strings, never on bus.Event
// itself; transformations such as Slack template / email subject
// template happen first so the redactor sees exactly the wire
// payload that would otherwise leave the process.
//
// # Open-circuit is terminal for RetrySink
//
// When the breaker is open, the sink's wrap returns
// breaker.ErrBrokenCircuit. The surrounding RetrySink (P2,
// see retry.go) MUST NOT retry an open-circuit error: open-circuit
// is the breaker's signal that the egress is currently degraded;
// retrying would defeat the breaker. RetrySink treats
// ErrBrokenCircuit as terminal and routes straight to the
// dead-letter sink (or returns the error unwrapped when no
// dead-letter is configured).
//
// Recommended check pattern, used inside RetrySink and inside any
// caller that distinguishes open-circuit from retryable failure:
//
//	if errors.Is(err, breaker.ErrBrokenCircuit) {
//	    // terminal: do not retry; route to dead-letter or surface.
//	}
//
// breaker.WrapHTTP, breaker.WrapCtx, and the bare breaker.Allow path
// all surface ErrBrokenCircuit unwrapped, so errors.Is suffices —
// no provider-specific error class needed.
//
// # Defaults
//
// Both WithRedactor and WithBreaker default to nil at construction.
// Sinks treat nil as "no-op": no redaction transformation, no
// breaker gating. This keeps the zero-config sink trivially usable
// in dev / testing while letting ops opt in to guardrails per
// channel via construction-time wiring.
//
// # Cross-references
//
//   - docs/specs/notifications.md §3 decisions #10, #11
//   - docs/specs/notifications.md §7.5 Guardrails (redaction,
//     breakers, boundedness)
//   - docs/audits/redact-egress-audit.md (entries #14, #15 cover
//     JSONLSink + StdoutSink; webhook / email / osnotify entries are
//     added by the kit-notify track)
//   - docs/audits/breaker-primitives-audit.md (network + exec
//     egress requirements)
//   - go/core/redact/README.md (Redactor surface; Apply / ApplyBytes
//     semantics; rule loading)
//   - go/core/breaker/README.md (Breaker surface; WrapHTTP /
//     WrapCtx; ErrBrokenCircuit sentinel; policy tuning)
//
// # Why this file exists
//
// This file holds the godoc only. Each P3 sink task description
// references it as the canonical convention spec; reviewers
// checking a sink PR can read this file once and verify the sink
// matches without re-deriving the convention from the spec each
// time. Keeping it in package notify (rather than in each sink
// subpackage) makes the convention visible to anyone browsing the
// notify package and lets `go doc hop.top/kit/go/runtime/notify`
// surface it.
package notify
