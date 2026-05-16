// Package toolspec defines a structured knowledge base for CLI tools.
//
// Types here are pure data with zero transitive dependencies. Sub-packages
// (sources/help, sources/completion) own their own deps and are imported
// separately.
//
// # Core Types
//
//   - [ToolSpec]: full spec for a CLI tool (commands, flags, errors, workflows)
//   - [Command]: command tree node with safety, contract, output schema, intent
//   - [SafetyLevel]: legacy safe/caution/dangerous classification
//   - [Permission]: typed token vocabulary for Safety.Permissions
//   - [Contract]: idempotency, side effects, pre-conditions
//
// # Safety vocabulary
//
// The walker projects three orthogonal axes from cobra annotations
// into the Safety record (see ADR-0019 for the full design):
//
//  1. kit/side-effect — six-tier ladder
//     (read | write-local | write-shared | destructive-local |
//     destructive-shared | interactive). Legacy 4-tier values
//     (write, destructive) keep working and map conservatively to
//     the shared variant.
//  2. kit/network — orthogonal axis
//     (none | egress:public | egress:private | ingress).
//  3. Capability hints — kit/exec → [PermExecSubprocess];
//     kit/bus-publish → [PermBusPublish]. Both forward-looking; the
//     harness contract track owns their default-policy decoder.
//
// Each command's Safety.Permissions slice always carries one
// kit:fs:* token and one kit:network:* token, plus optional
// capability tokens. Permissions are namespace-prefixed so consumers
// can extend without colliding (e.g. "myorg:db:write").
//
// # Registry
//
// [Registry] resolves specs from ordered [Source] implementations with
// optional caching. Sources are queried in order; results are merged via
// [Merge]:
//
//	reg := toolspec.NewRegistry(
//	    toolspec.WithSource(sources.Help{}),
//	    toolspec.WithCache(store),
//	)
//	spec, _ := reg.Resolve(ctx, "kubectl")
//
// # Capabilities
//
// [CapabilitySet] describes discoverable capabilities of a running service.
// Used by api.WithCapabilities to serve GET /capabilities:
//
//	cs := toolspec.NewCapabilitySet("myapp", "1.0.0")
//	cs.Add(toolspec.Capability{Name: "list-items", Type: "endpoint", Path: "/items"})
//	cs.Merge(otherSet)
//	data, _ := cs.JSON()
//
// Key functions: [NewCapabilitySet], [CapabilitySet.Add],
// [CapabilitySet.JSON], [CapabilitySet.Merge].
package toolspec
