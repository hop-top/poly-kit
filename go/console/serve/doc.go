// Package serve holds the contract types for the kit serve hierarchy:
// the [Service] a tool can run, the [Registry] services register into,
// the pure resolution of a `serve` invocation into a runnable set, and
// the mapping from a lifecycle outcome to a process exit code.
//
// The normative specification is
// docs/contracts/serve-lifecycle.md. This package is the authority
// for signatures; that document is the authority for behavior. Doc
// comments below cite the section they encode.
//
// # Scope
//
// This package declares the contract only. It does not supervise
// goroutines, bind listeners, install signal handlers, or mount a
// cobra command. Everything here is pure: [Resolve] is a function of
// its inputs, [ExitCodeFor] is a lookup, and [Registry] is a map with
// a stability guarantee. The supervisor that consumes these types
// lands separately.
//
// # Hierarchy
//
//	<tool> serve            supervisor — every configured AND enabled service
//	<tool> serve <service>  selector   — exactly one, overriding enablement
//
// Explicit selection overrides aggregate enablement, provided the
// service is registered and its configuration and policy validate.
// See [Resolve] and [Outcome].
package serve
