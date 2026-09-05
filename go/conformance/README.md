# conformance

## What it answers

Whether a cobra tree satisfies the 12-factor CLI conformance contract,
asserted from a unit test. `hop.top/kit/go/conformance` is the
adopter-facing library; not to be confused with
`hop.top/kit/go/console/cli/conformance`, which is the cobra tree
behind the `kit conformance` subcommand and exports no test helpers.
Behavioral assertions over a running CLI are the sibling `harness`;
static shape is here.

## Use it when

- assert the whole tree conforms → `kitconformance.AssertCLI(t, root)`
- flip configurable tiers piecewise in a CI matrix → `kitconformance.AssertCLIWithOptions(t, root, Options{EnforceGuidance: true})`
- opt out deliberately → `cli.Config{DisableValidate: true}`
- pick the response when validation fails at `Execute()` → `Config.ValidationFailureMode`
- validate at construction time and route the failure yourself → `cli.NewE(cfg, opts...)`
- route a failure into kit's error envelope → `ValidationError.AsCLIError()`

## Quick start

```go
import kitconformance "hop.top/kit/go/conformance"

func TestCLIConforms(t *testing.T) {
    kitconformance.AssertCLI(t, buildRoot())
}
```

`AssertCLI` forces `Config.EnforceValidate=true` for its scope and
walks the entire cobra tree under `root.Cmd`. The adopter's runtime
config is untouched: the helper restores the previous value when the
assertion returns.

## Contract

- `EnforceValidate=true` is the default at this release. The flip is final, there is no migration window.
- Pass 1, always active when `Validate` runs: every runnable leaf carries `kit/side-effect` (S1) and `kit/idempotent` after auto-apply (S2).
- Pass 2 hard tier, riding `EnforceValidate=true`: non-empty `Short` on leaves and groups (H1) and `Long` on leaves (H2), valid `kit/side-effect` (H3) and `kit/idempotent` (H4), parseable `kit/output-schema` (H5), a reserved `status` subcommand on the root (H6), `kit/top-level-verb` on depth-1 leaves (H7), top-level verb count at or under `MaxTopLevelVerbs` (default 10, H8), `kit/hierarchical` on every intermediate at depth 3 or more unless the depth-1 ancestor is reserved (H9), and tree depth at or under `MaxHierarchyDepth` (default 3, capped 5, H10).
- Pass 2 configurable tier, off by default at alpha: `EnforceDryRunRationale` (C1), `EnforceDestructiveToken` (C2), `EnforceGuidance` (C3 examples, C4 next-steps).
- Soft warning W1 surfaces `kit/passthrough` when `PassthroughStrictness="warn"` (default); `"reject"` promotes it to a failure.
- The track is structural. Provenance (`go/runtime/provenance`), cassettes (`harness/`), AI-judged quality and runtime structured-output detection are not part of `kit.Validate`.
- `cli.SetExemptValidation(cmd)` opts a leaf out and is reserved for kit-internal use; adopter commands annotate instead.

## Neighbours

- [`harness/`](harness/README.md): xrr-backed behavioral assertions over a running CLI.
- [`scenario/`](scenario/README.md), [`story/`](story/README.md): the grading DSLs.
- [`client/`](client/README.md): the Go client for the grading service.
- `go/console/cli`: every `Set*` annotation setter the checks read.
- `go/runtime/provenance`: the provenance arm, out of scope here.

## See also

- [Conformance reference](../../docs/adopters/reference/conformance.md): every rule and failure bucket, the factor-to-annotation map, `ValidationFailureMode`, the error envelope, the grading client, the harness, the scenario and story DSLs
- [enforce-cli-conformance.md](../../docs/adopters/guides/enforce-cli-conformance.md): the seven-step migration for an existing tool
- ADR-0024: the 12fcc conformance contract
