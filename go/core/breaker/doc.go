// Package breaker provides runtime circuit breakers for kit-based tools.
//
// kit/breaker bounds the runtime blast radius of any operation —
// file writes, exec spawns, HTTP calls, token spend — that path
// policy alone (kit/scope) cannot catch. Where kit/scope answers
// "where can I touch?", kit/breaker answers "how much, how fast,
// how often before I stop?".
//
// The package is a thin wrapper over failsafe-go, exposing
// kit-flavored types so callers don't need to import failsafe
// directly and so we own the contract for the polyglot parity
// ports (TS + Python).
//
// failsafe-go ships CircuitBreaker, RateLimiter, Bulkhead, Timeout,
// Retry, Fallback, Hedge, AdaptiveLimiter, AdaptiveThrottler.
// kit/breaker adds the two policies failsafe doesn't ship: Volume
// (cumulative bytes) and Count (cumulative ops).
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
