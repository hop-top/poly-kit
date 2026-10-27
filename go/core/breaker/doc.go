// Package breaker provides runtime circuit breakers for kit-based tools.
//
// kit/breaker bounds the runtime blast radius of any operation —
// file writes, exec spawns, HTTP calls, token spend — that path
// policy alone (kit/scope) cannot catch. Where kit/scope answers
// "where can I touch?", kit/breaker answers "how much, how fast,
// how often before I stop?".
//
// The circuit state machine is kit's own, reading an injectable
// Clock (WithClock) so cooldowns are testable without sleeping and
// the polyglot ports share one contract (contracts/breaker-v1). The
// rate limiter, bulkhead, timeout and fallback come from failsafe-go,
// exposed through kit-flavored types so callers don't need to import
// failsafe directly. kit/breaker adds the two policies failsafe
// doesn't ship: Volume (cumulative bytes) and Count (cumulative ops).
//
// Composition order (innermost → outermost in the executor):
//
//	Volume / Count → RateLimiter → Bulkhead → Timeout → CircuitBreaker → Fallback
//
// Cheap pre-flight checks fire first; the circuit breaker observes
// everything else's failures; the fallback catches them all.
//
// Note: failsafe.With takes policies OUTERMOST-first, so the slice
// kit/breaker hands to it is the reverse of the conceptual order
// above.
package breaker
