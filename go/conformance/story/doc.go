// Package story is the kit-shipped story DSL surface: a closed-key
// YAML schema, a parser that enforces the closed-key set via
// yaml.v3's KnownFields(true), a three-tier validator, and helpers
// scenario tooling can use to index + content-pin stories.
//
// The package itself exposes high-level helpers (Index,
// ContentSHA256, Discover); the sub-packages hold the typed schema
// (schema/), parser (parser/), validator (validator/), and a
// minimal toolspec projection used for tier 3 (toolspec/).
//
// The CLI wrapper for adopters is `kit conformance verify-stories`
// (see go/console/cli/conformance/verify_stories.go).
//
// Leak-rule resistance: a valid story does NOT carry scenario keys
// (scenario_id, assertions, judge, cassette_must_*). The closed-key
// shape enforced by parser is the structural guarantee; the
// validator's metadata-key denylist (sourced from the same
// contracts/scenario-rules.json verify-no-leak uses) closes the
// last escape hatch. See ADR-0026.
package story
