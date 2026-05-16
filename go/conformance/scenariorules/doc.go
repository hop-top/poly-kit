// Package scenariorules is the kit-wide shared loader for
// contracts/scenario-rules.json. It is the single source of truth
// for the scenario verb set, top-level scenario keys, and the
// compound-rule definitions consumed by both:
//
//   - the leak detector (`kit conformance verify-no-leak`), which
//     uses the rules to scan files / fenced markdown / commit
//     messages for scenario-shaped YAML; and
//   - the story validator (`kit conformance verify-stories`), which
//     uses the verb set + top-level keys as a metadata-key denylist
//     (every key the leak rules treat as scenario-shaped is rejected
//     when it appears inside a story's `metadata:` map).
//
// Keeping the loader shared guarantees the two leaves stay in
// lockstep: any update to the canonical contracts/scenario-rules.json
// (which the scen package owns) propagates to both verifiers without code
// changes in either.
//
// The package is intentionally consumer-agnostic. It does not embed
// any scanning logic, YAML node walking, or formatter — those live
// in the leak detector. It only exposes:
//
//   - LoadDefault() / LoadFromPath() / LoadBytes() — the three entry
//     points that produce a *Document.
//   - The wire-format types (Document, CompoundRule) and constants
//     (the recognized Kind* values).
//
// To refresh the embedded copy after editing the canonical file:
//
//	cp contracts/scenario-rules.json \
//	  go/conformance/scenariorules/scenario_rules_embedded.json
//
// A drift test in this package compares the two files at test time
// so out-of-sync state is caught before merge.
package scenariorules
