// Package serve holds the contract types for the kit service hierarchy:
// the [Service] a tool can run, the [Registry] services register into,
// the pure resolution of a `service start` invocation into a runnable set, and
// the mapping from a lifecycle outcome to a process exit code.
//
// The normative specification is
// docs/contracts/serve-lifecycle.md. This package is the authority
// for signatures; that document is the authority for behavior. Doc
// comments below cite the section they encode.
//
// # Scope
//
// This package declares the contract and runs it. The [Supervisor]
// starts a resolved set of services in order, waits for readiness,
// reacts to failure by policy and stops in reverse order, recording
// the run in a [RunStatus] a status surface reads ([WithRunObserver]
// hands it over as the run begins). It binds no listeners and mounts
// no cobra command (that lives in hop.top/kit/go/console/cli); the
// Supervisor installs no signal handlers itself, [SignalContext]
// turns SIGINT/SIGTERM into the cancellation and escalation it takes.
// The rest is pure: [Resolve] is a function of its inputs,
// [ExitCodeFor] is a lookup, and [Registry] is a map with a stability
// guarantee.
//
// # Hierarchy
//
//	<tool> service start            supervisor — every configured AND enabled service
//	<tool> service start <service>  selector   — exactly one, overriding enablement
//
// Explicit selection overrides aggregate enablement, provided the
// service is registered and its configuration and policy validate.
// See [Resolve] and [Outcome].
package serve
