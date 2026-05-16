# `kit` CLI conformance (Layer-A / 12fcc-static)

`hop.top/kit/go/console/cli/conformance` is the adopter-facing
surface for the **12-factor CLI conformance contract**, Wave 1
(static-track). Import it as `kitconformance` from a unit test:

```go
import kitconformance "hop.top/kit/go/console/cli/conformance"

func TestCLIConforms(t *testing.T) {
    kitconformance.AssertCLI(t, buildRoot())
}
```

`AssertCLI` forces `Config.EnforceValidate=true` for its scope and
walks the entire cobra tree under `root.Cmd`. The adopter's runtime
config is untouched — the helper restores the previous value when
the assertion returns.

This document explains **what gets enforced**, **what doesn't yet**,
the **factor → annotation** mapping, and a **migration cookbook**
for adopters flipping `EnforceValidate=true` on an existing tool.

## TL;DR — what's enforced at 0.1.0-alpha.0

`EnforceValidate=true` is the **default** at this release. Adopters
who need a temporary escape hatch (negative tests, fuzz harnesses,
embedded use-cases) set `cli.Config{DisableValidate: true}`. The
flip is final — there is no migration window per
[design.md §6](../../../../.tlc/tracks/12fcc-static/design.md).

The validator runs in two passes:

### Pass 1 — shipped checks (always active when `Validate` runs)

| # | Rule | Annotation | Failure bucket |
|---|------|-----------|----------------|
| S1 | Every runnable leaf has `kit/side-effect` | `cli.SetSideEffect` | `Missing` / `Invalid` |
| S2 | Every runnable leaf has `kit/idempotent` (after auto-apply) | `cli.SetIdempotency` | `MissingIdempotency` / `InvalidIdempotency` |

### Pass 2 — Layer-A hard tier (rides `EnforceValidate=true`)

| # | Rule | Annotation / source | Failure bucket |
|---|------|---------------------|----------------|
| H1 | Runnable leaves + group nodes carry non-empty `Short` | `cmd.Short` | `MissingShort` |
| H2 | Runnable leaves carry non-empty `Long` | `cmd.Long` | `MissingLong` |
| H3 | `kit/side-effect` valid (shipped, sub-tier of H) | `cli.SetSideEffect` | `Invalid` |
| H4 | `kit/idempotent` valid (shipped, sub-tier of H) | `cli.SetIdempotency` | `InvalidIdempotency` |
| H5 | `kit/output-schema`, when declared, parses as JSON | `cli.SetOutputSchema` | `InvalidOutputSchema` |
| H6 | Root mounts a reserved `status` subcommand | `cli.WithStatus(...)` or hand-rolled `Use: "status"` | `MissingStatusSubcommand` |
| H7 | Depth-1 runnable leaves carry `kit/top-level-verb` | `cli.SetTopLevelVerb` | `UnannotatedTopLevelLeaf` |
| H8 | Top-level verb count ≤ `Config.MaxTopLevelVerbs` (default 10) | n/a — adjust `MaxTopLevelVerbs` | `TooManyTopLevelVerbs` |
| H9 | Leaves at depth ≥ 3 have `kit/hierarchical` on every intermediate, unless the depth-1 ancestor is reserved | `cli.SetHierarchical` | `UnannotatedDepthExceedance` |
| H10 | Tree depth ≤ `Config.MaxHierarchyDepth` (default 3, capped 5) | n/a — restructure tree | `HierarchyDepthExceeded` |

### Pass 2 — Layer-A configurable tier (off by default at α)

Sub-flags on `Config` flip the configurable arms piecewise. They
stay **off** at `0.1.0-alpha.0` so adopters can flip each one when
their annotation tail is ready.

| # | Rule | Config flag | Annotation / setter | Failure bucket |
|---|------|-------------|---------------------|----------------|
| C1 | Opted-out `--dry-run` on write/destructive leaves carries a 1-200-char rationale | `EnforceDryRunRationale` | `cli.SetDryRunRationale` | `MissingDryRunRationale` |
| C2 | Destructive leaves require typed-token confirmation | `EnforceDestructiveToken` | `cli.SetDestructiveToken` | `MissingDestructiveToken` |
| C3 | Runnable leaves declare `kit/examples` | `EnforceGuidance` | `cli.SetExamples` | `MissingExamples` |
| C4 | Non-read leaves declare `kit/next-steps` | `EnforceGuidance` | `cli.SetNextSteps` | `MissingNextSteps` |

`AssertCLIWithOptions` lets a CI matrix flip each flag independently:

```go
kitconformance.AssertCLIWithOptions(t, root, kitconformance.Options{
    EnforceGuidance:        true,  // gate Examples + NextSteps
    EnforceDryRunRationale: false, // not ready yet
})
```

### Pass 2 — soft warnings (informational, no fail)

| # | Rule | When |
|---|------|------|
| W1 | `kit/passthrough` annotation surfaces | `PassthroughStrictness="warn"` (default); flip to `"reject"` to promote to fail |

## What is NOT enforced

The 12fcc-static track is **structural** — it locks the shape of
the cobra tree and the annotation surface. The dynamic / behavioral
arms ship in companion tracks:

- **Provenance check** (factors 3, 4): captured in `12fcc-prov`
  (`go/runtime/provenance/`). Not part of `kit.Validate`.
- **Test cassettes** (factor 9): xrr-based integration testing in
  `12fcc-harness` (`go/console/cli/conformance/harness/`). Not part
  of `kit.Validate`.
- **AI-judged quality** (factors 4, 9 polish tier): deferred until
  structural enforcement is adopted.
- **Structured-output detection at runtime** (Pass 2 H5 extension):
  the validator cannot tell whether `RunE` calls `output.Dispatch`;
  declare `cli.SetOutputSchema` explicitly when your leaf emits
  structured data. A future `kit doctor` track will surface leaves
  that emit structured data without a declared schema.

## Factor → annotation map

| 12-factor # | Factor | Wave-1 surface | How to satisfy |
|-------------|--------|----------------|----------------|
| 1 | Discovery | `<tool> spec --format json` | `cli.RegisterSpecCommand(root, "1.1")` (the spec subcommand self-annotates) |
| 2 | Versioning | `--api-version` filter + `kit/since`, `kit/min-api-version` | `cli.SetSinceVersion`, `cli.SetMinAPIVersion` |
| 3 | Provenance | `go/runtime/provenance/` (out of scope here) | see `12fcc-prov` |
| 4 | Schema | `kit/output-schema` + `kit/output-schema-version` | `cli.SetOutputSchema(cmd, cli.OutputSchema{Type: &T{}, Version: "1.0"})` |
| 5 | Side effect | `kit/side-effect` | `cli.SetSideEffect(cmd, cli.SideEffectRead)` |
| 6 | Idempotency | `kit/idempotent` | `cli.SetIdempotency(cmd, cli.IdempotencyYes)` |
| 7 | Retry | `kit/retryable` | `cli.SetRetryable(cmd, true)` |
| 8 | State | reserved `<tool> status` | `cli.WithStatus(cli.StatusConfig{})` |
| 9 | Testability | xrr cassettes (out of scope) | see `12fcc-harness` |
| 10 | Errors | `output.Error` envelope (already shipped via `cli.WrapRunE`) | nothing; ride the middleware |
| 11 | Observability | slog hooks + `--show-sensitive` audit | nothing; ride kit defaults |
| 12 | Evolution | `kit/deprecated-since`, `kit/removal-target`, `kit/replaced-by` | `cli.SetDeprecation` |
| — | Shape | `kit/top-level-verb`, `kit/hierarchical`, `kit/passthrough` | `cli.SetTopLevelVerb`, `cli.SetHierarchical`, `cli.SetPassthrough` |
| — | Guidance | `kit/examples`, `kit/next-steps` (configurable C3/C4) | `cli.SetExamples`, `cli.SetNextSteps` |
| — | Confirmation | `kit/destructive-token`, `kit/dry-run-rationale` (configurable C1/C2) | `cli.SetDestructiveToken`, `cli.SetDryRunRationale` |

## Migration cookbook

For adopters running on `kit` < 0.1.0-alpha.0 (no enforcement) who
need to flip `EnforceValidate=true`:

### Step 1 — run the assertion in a unit test

```go
func TestCLIConforms(t *testing.T) {
    root := buildMyRoot()           // your normal constructor
    kitconformance.AssertCLI(t, root)
}
```

`AssertCLI` will dump every failing bucket. Start with the **hard
tier** (Pass 2 H1-H10). Skip Pass 2 C1-C4 for now — they're off by
default.

### Step 2 — address Pass 1 failures first (kit/side-effect, kit/idempotent)

Every leaf that fails `S1`/`S2` needs a `cli.SetSideEffect(cmd, ...)`
call. Pick the tier honestly:

- `SideEffectRead` — no state mutation, no network writes
- `SideEffectWrite` — disk writes, local state changes
- `SideEffectWriteExternal` — network writes outside the
  tool's own state (e.g. POSTing to a peer)
- `SideEffectDestructive` — irreversible deletes (data,
  credentials, ledgers)
- `SideEffectDestructiveExternal` — irreversible deletes affecting
  systems beyond the tool's own state

`SetIdempotency` defaults are auto-applied based on the verb name
(`list/show/get/info → yes`; `delete/destroy → no`; etc.), so you
mostly leave it untouched. Pin explicitly when the default is wrong.

### Step 3 — annotate Short + Long on every leaf

`H1` and `H2` are the loudest buckets. cobra's `Short` is the
one-line description shown in `--help`; `Long` is the paragraph
shown on `<cmd> --help`. Both must be non-empty.

```go
&cobra.Command{
    Use:   "deploy <env>",
    Short: "Deploy the current commit to the named environment",
    Long: "Build, package, and deploy the current commit to env. " +
        "Idempotent against the same SHA; --force re-deploys.",
    // ...
}
```

### Step 4 — register the reserved `status` subcommand

`H6`. The simplest path is `cli.WithStatus(cli.StatusConfig{})`:

```go
root := cli.New(cli.Config{...},
    cli.WithStatus(cli.StatusConfig{
        ExtraEnvKeys:   []string{"MYTOOL_*"}, // widen env filter
        RedactPatterns: []string{"PRIVATE"},  // your secret-key marker
    }),
)
```

That mounts a `<tool> status` subcommand with six default providers
(profile / env / workspace / auth / effective-config /
kit-annotations); extend via `root.RegisterStatusProvider(name, fn)`.

Adopters who already ship a `status` command of their own keep it —
H6 is a presence check, not a behavior check.

### Step 5 — shape annotations for non-canonical layouts

`H7-H10` cover the noun-verb shape rules. The canonical layout is
depth-2 (`<tool> <noun> <verb>`); deviations need explicit opt-ins.

| Layout | Annotation | Where to put it |
|--------|-----------|-----------------|
| `<tool> <verb>` (depth-1 leaf) | `kit/top-level-verb` | The leaf |
| `<tool> <noun> <sub-noun> <verb>` (depth ≥ 3) | `kit/hierarchical` | Every intermediate, **unless** the depth-1 ancestor is a reserved subcommand (status / spec) |

```go
// Depth-1 leaf: kit init, kit serve
cli.SetTopLevelVerb(initCmd)

// Depth-3+ tree: mytool foo bar baz
fooGroup := &cobra.Command{Use: "foo", Short: "Foo management"}
cli.SetHierarchical(fooGroup)
barGroup := &cobra.Command{Use: "bar", Short: "Bar management"}
cli.SetHierarchical(barGroup)
fooGroup.AddCommand(barGroup)
barGroup.AddCommand(bazLeaf)
```

If the tree genuinely exceeds depth 5, restructure — the cap is
hard-coded and represents a modeling smell. If a leaf at depth 3+
is unavoidable and the depth-1 ancestor is reserved (e.g. you have
`<tool> toolspec policy show`), no annotation is required.

### Step 6 — opt-in to configurable tiers when ready

```go
// Once your guidance authoring is caught up
cfg.EnforceGuidance = true

// Once your destructive surfaces have typed tokens
cfg.EnforceDestructiveToken = true

// Once your write/destructive leaves all carry rationales
cfg.EnforceDryRunRationale = true
```

Flip each independently per follow-up track. There's no requirement
to flip all four at once.

### Step 7 — the escape hatch

If you genuinely cannot annotate (legacy internal tool, vendored
cobra binary, etc.), keep `cli.Config{DisableValidate: true}` for
now. The flag is supported indefinitely; it exists for adopters who
opt out of the strict surface deliberately.

For commands kit ships internally that cannot reasonably carry the
full annotation set (compat shims, debug-only stubs), use
`cli.SetExemptValidation(cmd)` to opt out at the leaf level. This
is **reserved for kit-internal use** — adopter commands should
annotate instead.

## ValidationFailureMode

When validation fails at `Execute()`, `Config.ValidationFailureMode`
picks the response:

| Value | Behavior |
|-------|----------|
| `""` (default) `ValidationFailureExit` | Write error to stderr, `os.Exit(2)` (`ExitUsage`). Preserves the pre-flip shipped UX. |
| `"error"` `ValidationFailureError` | Return `*cli.ValidationError` from `Execute`. Pair with `cli.NewE` to also catch construction-time failures. |
| `"panic"` `ValidationFailurePanic` | Panic with the error value. Useful for debugging registration-order issues — the stack trace pinpoints the offending caller. |
| `"silent"` `ValidationFailureSilent` | Log to stderr and continue. Recovery-mode escape hatch; **discouraged** outside tooling that must boot even with a misconfigured tree. |

`cli.NewE` is the constructor-time companion: it runs `Validate` at
construction time and returns `(*Root, *ValidationError)` so adopters
who embed kit inside a larger CLI (plugin host, multi-tool harness,
server pre-boot validator) can route the failure into their own
error envelope:

```go
root, ve := cli.NewE(cfg, opts...)
if ve != nil {
    return ourEnvelope.Wrap(ve)
}
return root.Execute(ctx)
```

## Error envelope

Validation failures surface through `output.Error` via
`ValidationError.AsCLIError()`:

```go
err := root.Validate()
if ve, ok := err.(*cli.ValidationError); ok {
    cliErr := ve.AsCLIError()
    // cliErr.Code     == output.CodeUsage
    // cliErr.ExitCode == 2
    // cliErr.Message  == ve.Error()
}
```

The kit `WrapRunE` middleware routes adopter `RunE` errors through
the same envelope, so a misconfigured tree under `ValidationFailureError`
mode emits the same shape as a runtime usage error.

## References

- Design doc:
  [`.tlc/tracks/12fcc-static/design.md`](../../../../.tlc/tracks/12fcc-static/design.md)
- ADR-0024 (12fcc conformance contract) — forthcoming
- Foundation commits: `e4b1a23..a559b7a` on branch `12fcc-static`
- Migration commits: `c543bf1..HEAD` on branch `12fcc-static`
