// Package buswf generates the `.github/workflows/kit-bus-*.yml` files
// that emit PR-lifecycle events onto the kit bus.
//
// Implements the consumer-side scaffolding contract pinned in
// docs/contracts/kit-init-pr-wiring.md (T-0776). The four topic names
// are:
//
//   - github.pr.run.completed
//   - github.pr.comment.created
//   - github.pr.pull.merged
//   - github.pr.pull.closed
//
// All workflows are gated at job level on
// `vars.KIT_BUS_ENABLED == 'true' && vars.KIT_BUS_INGRESS_URL != ""`
// so they are runtime-disabled by default even when present.
//
// The actual POST to the bus ingress, signature/bearer auth, payload
// size truncation, and fail-open/closed behavior live in the helper
// binary `cmd/kit-bus-emit`. Workflows shell out to it through `run:`.
// Keeping payload logic in Go (rather than inlined bash) makes it
// unit-testable; see cmd/kit-bus-emit for those tests.
package buswf
