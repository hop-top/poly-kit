# `hop.top/kit/go/runtime/provenance`

Wire-level provenance for Factor-12 (Hops First-CLI Conformance
Conventions): every Synthesized or Cached value that flows into
structured output carries metadata describing where it came from.

> Two parametrised wrappers — `Cached[T]` and `Synthesized[T]` —
> mark the non-authoritative provenance of a single field. A
> per-context `Tracker` records the metadata keyed by RFC 6901
> JSON pointer. The `Render` boundary fires a strict-mode refusal
> when an emitted wrapper has no recorded `Provenance` entry.

ADR-0024 records the design rationale and the lint-plus-guardrail
combination.

## Why this exists

Agents and adopters reading kit-produced JSON should be able to
answer "where did this number come from?" without grepping source
or replaying cassettes. The two failure modes Factor 12 guards
against:

- Honest mistake: an adopter forgets to populate a field, defaults
  it to zero, and the consumer reads "0" as authoritative.
- Capability drift: an adopter swaps a live API call for a cached
  copy "while we're rate-limited", and downstream tooling never
  notices the freshness regression.

The package makes both errors loud at the `Render` boundary
rather than silent on the wire.

## When to declare what

Use the wrappers (or `Tracker.Synthesize` / `Tracker.Cache`) on
**output fields** that are:

- Served from a cache layer rather than freshly fetched → `Cached[T]`.
- Derived by the tool from other inputs (LLM, heuristic, join) →
  `Synthesized[T]` with `Source: SourceInferred`.
- Defaulted from configuration with no upstream source of truth →
  `Synthesized[T]` with `Source: SourceDefaulted`.

Plain `T` (no wrapper) is the **authoritative** case. The
convention says: if a reader needs to know where the value came
from beyond "this tool fetched it fresh", reach for a wrapper.

## Adopter happy path

```go
import (
    "context"
    "time"

    "hop.top/kit/go/runtime/provenance"
)

type UserOut struct {
    Email  string                          `json:"email"`
    Cohort provenance.Cached[string]       `json:"cohort"`
    Score  provenance.Synthesized[float64] `json:"score"`
}

func runE(cmd *cobra.Command, args []string) error {
    ctx := cmd.Context()

    // 1. Fetch + stamp. Two paths:
    //
    //   (a) Construct the wrapper directly:
    cohort, fetchedAt := readCacheLayer()
    out := UserOut{
        Email: fetchEmail(ctx),
        Cohort: provenance.NewCached(cohort, provenance.Provenance{
            Source:    provenance.SourceCached,
            URL:       "https://cache/cohort/u1",
            FetchedAt: fetchedAt,
        }),
        Score: provenance.NewSynthesized(scoreOf(ctx),
            provenance.Provenance{
                Source: provenance.SourceInferred,
                Version: "scorer-v3",
            }),
    }

    //   (b) Or use the source wrappers, which auto-stamp:
    // resp, prov, _ := httpwrap.New(http.DefaultClient).
    //     Get(ctx, "/cohort", "https://cache/cohort/u1")
    // out.Cohort = provenance.NewCached(string(body), prov)

    // 2. Render. In Strict mode, Render refuses to emit if any
    //    wrapper has no Provenance recorded; the *output.Error
    //    travels back through the middleware as exit 6.
    return provenance.Render(ctx, cmd.OutOrStdout(), "json", out)
}
```

## Strict mode

Three tiers, set by `SetMode` (package-global) or `WithMode`
(per-context override) or the `KIT_PROVENANCE_MODE` env var:

| Mode | What it does | When to use |
|------|--------------|-------------|
| `ModeOff` | No checks; `Render` is plain `json.Marshal`. | Default; backward-compatible with pre-provenance adopters. |
| `ModeWarn` | Records and emits, but warns to stderr on missing entries. | Dogfooding stage — switch on once your tests pass, fix warnings as they pop up. |
| `ModeStrict` | Refuses to emit; returns `*output.Error{Code: "PROVENANCE_MISSING", ExitCode: 6}`. | Production — silent provenance regressions become loud. |

The `--strict` invocation flag (when adopters wire it) calls
`WithMode(ctx, ModeStrict)` for that single run, leaving the
package-global default alone.

## Why strict mode matters

The wrappers + lint alone won't catch all violations. The lint is
structural — it flags missing JSON tags and zero-value literals at
compile time. Strict mode is the *runtime* belt-and-suspenders: it
catches the runtime drift case where an adopter ships a correctly
declared wrapper, then forgets to populate it on some code path
(e.g., the cache-miss branch, a refactor that re-routes a function
without re-routing its Tracker call).

The default is `ModeOff` so unaware adopters are not broken on
upgrade; turn on `ModeWarn` in your `main()` once you've audited
your output structs.

## Envelope shape (Warn / Strict modes)

```json
{
  "data": {
    "email": "user@example.com",
    "cohort": "beta",
    "score": 0.42
  },
  "provenance": {
    "/cohort": {
      "schema_version": "1",
      "source": "cached",
      "url": "https://cache/cohort/u1",
      "fetched_at": "2026-05-11T12:00:00Z"
    },
    "/score": {
      "schema_version": "1",
      "source": "inferred",
      "version": "scorer-v3"
    }
  }
}
```

Why a separate envelope key?

- Schema of `data` is unchanged from a no-provenance world; agents
  written before provenance landed still parse `data.*` correctly.
- Provenance is queryable as a single block — easy to ignore or
  process in bulk.
- JSON-pointer paths are stable across schema versions.

Adopters who already have a top-level `data:` envelope key can
call `SetEnvelopeKey("result")` (or any unique string) from
`main.init()` to override the default.

## Lint

`go/tools/provenancelint` ships a `go vet`-style analyzer.
Install + run:

```sh
go install hop.top/kit/go/tools/provenancelint/cmd/provcheck
go vet -vettool=$(go env GOPATH)/bin/provcheck ./...
```

Findings:

- wrapper field missing `json:` tag
- zero-value wrapper literal in non-test code
- bare `Provenance` sibling field alongside a wrapper field
  (anti-pattern; the wrapper carries its own metadata via the
  envelope, no sibling needed)

## Source wrappers

`provenance/wrap/{httpwrap,sqlwrap,execwrap}` are kit-blessed
sub-packages that auto-stamp Provenance on each call:

- `httpwrap.Client` wraps `*http.Client`. `New` tags responses as
  authoritative; `NewCacheClient` tags them cached. URLs go through
  `Normalize` before recording (cassette-cross-check friendly).
- `sqlwrap.DB` wraps `*sql.DB`. The DSN is sanitised (password
  stripped) at `Wrap` time so it can be surfaced as URL safely.
- `execwrap.Exec` stamps URL as `exec://<argv0>` with a 12-char
  argv hash as Version.

All three are zero-cost in `ModeOff`. Adopters who don't use the
wrappers can still construct `Provenance` manually and call
`Tracker.Synthesize` / `Tracker.Cache` directly.

## Harness primitives

For integration tests (Layer B of the kit conformance harness):

```go
provenance.AssertProvenanceComplete(t, ctx, out)
provenance.AssertProvenanceMatchesCassette(t, ctx, out, entries)
```

The harness package (under construction in 12fcc-harness) re-exports
these under its own name; the implementation lives here because the
kit/provenance package owns the wire contract.

## Cassette cross-check

`provenance.Normalize` is the contract surface between this package
and the xrr cassette recorder. Both apply the same normalisation so
`AssertProvenanceMatchesCassette` becomes a string-compare problem:

- Lowercase scheme + host.
- Strip default ports (`:80` for http, `:443` for https).
- Sort query parameters by key then value.
- Strip URL fragment.
- Non-HTTP schemes (`doc://`, `exec://`, `sql://`) pass through
  unchanged (only the scheme is lowercased).

## v1 limitations

- **No cross-runtime sister packages.** TS / Py / Rs / PHP versions
  are out of scope for v1. The wire format is stable; sister
  implementations land in a follow-up if the demand materialises.
- **No attestation / signing.** This package guards against honest
  mistakes and capability drift, not adversarial authors. See
  survey §1 and §8 R6.
- **`DerivedFrom` is optional.** v1 producers MAY leave it nil even
  for inferred-from-cached values. v2 will tighten.
- **Auto-install of Tracker in kit cli middleware is deferred.** v1
  is opt-in: adopters call `provenance.WithTracker` themselves; a
  follow-up coordinated with 12fcc-static wires the install
  automatically.

## See also

- ADR-0024 — provenance lint + guardrail combination.
- Track design doc: `.tlc/tracks/12fcc-prov/design.md`.
- ADR-0019 — `runtime/sideeffect` mirror precedent for the
  context-bound seam idiom.
