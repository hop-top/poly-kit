// Package provenance implements the kit's Factor-12 (Hops First-CLI
// Conformance Conventions) provenance contract: every Synthesized or
// Cached value that flows into structured output carries metadata
// describing where the value came from.
//
// Two parametrised wrappers — Cached[T] and Synthesized[T] — flag the
// non-authoritative provenance of a single field. A per-context
// Tracker records the Provenance metadata keyed by RFC 6901 JSON
// pointer; the Render boundary fires a strict-mode refusal when an
// emitted wrapper has no recorded Provenance entry.
//
// The package is mode-gated: ModeOff (default) is zero-cost; ModeWarn
// records and warns on stderr; ModeStrict turns missing entries into
// *output.Error{Code: "PROVENANCE_MISSING", ExitCode: 6} returned from
// Render before bytes hit stdout. SetMode flips the package global;
// WithMode overrides per context (e.g., a --strict CLI invocation).
//
// Adopters typically alias the package as `prov` in their import block.
//
// Public surface (v1):
//
//	hop.top/kit/go/runtime/provenance              -- types + Tracker + Render + Verify
//	hop.top/kit/go/runtime/provenance/wrap/httpwrap -- HTTP source wrapper
//	hop.top/kit/go/runtime/provenance/wrap/sqlwrap  -- SQL source wrapper
//	hop.top/kit/go/runtime/provenance/wrap/execwrap -- os/exec source wrapper
//
// See the package README for the adopter happy-path. The design doc
// at
package provenance
