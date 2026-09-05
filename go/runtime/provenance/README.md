# provenance

## What it answers

Where each field of a structured output came from, so a reader can tell a
freshly fetched value from a cached or derived one. Recording that a side
effect happened at all is `go/runtime/sideeffect`; emitting the bytes is
`go/console/output`.

## Use it when

- a field is served from a cache → `provenance.NewCached(v, p)` or `Tracker.Cache`
- a field is derived by the tool (LLM, heuristic, join) → `provenance.NewSynthesized(v, p)` with `Source: SourceInferred`
- a field is defaulted from config with no upstream truth → `Synthesized[T]` with `Source: SourceDefaulted`
- stamp provenance automatically → `provenance/wrap/httpwrap`, `sqlwrap`, `execwrap`
- emit with the provenance envelope → `provenance.Render(ctx, w, "json", out)`
- choose enforcement → `SetMode`, `WithMode(ctx, ModeStrict)`, `KIT_PROVENANCE_MODE`
- assert in integration tests → `AssertProvenanceComplete`, `AssertProvenanceMatchesCassette`

## Quick start

```go
type UserOut struct {
    Email  string                          `json:"email"`
    Cohort provenance.Cached[string]       `json:"cohort"`
    Score  provenance.Synthesized[float64] `json:"score"`
}

return provenance.Render(ctx, cmd.OutOrStdout(), "json", out)
```

## Contract

- Plain `T` with no wrapper is the authoritative case. Reach for a wrapper whenever a reader needs to know more than "this tool fetched it fresh".
- Three modes: `ModeOff` (default) leaves `Render` as plain `json.Marshal`; `ModeWarn` records, emits and warns to stderr on missing entries; `ModeStrict` refuses to emit and returns `*output.Error{Code: "PROVENANCE_MISSING", ExitCode: 65}`.
- `--strict`, where adopters wire it, applies `WithMode(ctx, ModeStrict)` for that run only and leaves the package global alone.
- Provenance rides in a sibling `provenance` envelope key, never inside `data`, so pre-provenance consumers still parse `data.*`. Keys are RFC 6901 JSON pointers, stable across schema versions. `SetEnvelopeKey("result")` from `main.init()` renames the data key.
- The wrappers are zero-cost in `ModeOff`, and `Provenance` can always be constructed by hand.
- `provenance.Normalize` is the contract surface shared with the xrr cassette recorder: both sides normalise identically so cassette cross-check is a string compare.
- The lint is structural and the mode is the runtime backstop; neither alone catches a wrapper that is declared correctly but left unpopulated on some branch.

## Neighbours

- [`wrap/`](wrap/): the `httpwrap`, `sqlwrap` and `execwrap` auto-stamping clients.
- `go/tools/provenancelint`: the `go vet`-style analyzer (`provcheck`).
- `go/runtime/sideeffect`: recording that an effect happened, not where a value came from.
- `go/console/output`: the emitter and its `*output.Error` exit-code mapping.

## See also

- [Provenance reference](../../../docs/adopters/reference/provenance.md): wrapper choice, happy path, mode table, envelope shape, lint findings, source wrappers, harness primitives, cassette normalisation, v1 limitations
- [Go primitives index](../../../docs/adopters/reference/go-primitives.md)
- ADR-0024: provenance lint and guardrail combination
- ADR-0019: the `runtime/sideeffect` mirror precedent
