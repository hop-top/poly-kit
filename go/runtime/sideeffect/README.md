# sideeffect

## What it answers

How one seam serves both `--dry-run` preview and test mocking for the four
categories that cover essentially all kit CLI mutations: `FS`, `HTTP`,
`Bus`, `Exec`. Where an output value came from is
`go/runtime/provenance`; whether a path may be touched at all is
`go/core/scope`.

## Use it when

- inject the seam → `sideeffect.FS`, `HTTP`, `Bus`, `Exec` interfaces
- run for real → `real.FS{}`, `real.NewHTTP(c)`, `real.NewBus(pub)`, `real.Exec{}`
- preview instead of acting → `dryrun.NewFS(dryrun.WithWriter(w))`, `dryrun.NewHTTP`, `dryrun.NewBus`, `dryrun.NewExec`
- assert calls in tests → `testfake.NewFS(t)`, `.Allow(pred)`, `Calls()`, `AssertCalled`, `AssertNotCalled`
- branch on the flag from library code → `sideeffect.IsDryRun(ctx)`, `sideeffect.WithDryRun(ctx, true)`
- opt a leaf in or out → `cli.SetSideEffect(cmd, cli.SideEffectWrite)`, `cli.OptOutDryRun(cmd)`
- tag published events → `sideeffect.NewDryRunPublisher(pub)`

## Quick start

```go
func pickFS(cmd *cobra.Command) sideeffect.FS {
    if sideeffect.IsDryRun(cmd.Context()) {
        return dryrun.NewFS(dryrun.WithWriter(cmd.ErrOrStderr()))
    }
    return real.FS{}
}
```

## Contract

- Reads (`os.ReadFile`, `os.Stat`, `http.Get`, `http.Head`) pass through to stdlib unchanged; dry-run does not pretend reads are unsafe.
- `--dry-run` is a kit-managed persistent flag bound to viper key `kit.dry_run`. A `PersistentPreRunE` hook resolves the policy per dispatched leaf and tags the context only when the policy allows.
- Policy by `kit/side-effect` tier: `read` is a silent no-op, `write` and `destructive` are supported by default, `interactive` is rejected with a diagnostic. `kit/dry-run: opted-out` (via `cli.OptOutDryRun`) rejects; `kit/dry-run: supported` (via the legacy `cli.SupportsDryRun`) allows with a one-time warning.
- `IsDryRun` lives here, not in `cli`, so library code can branch without taking a cli dependency.
- `testfake` `Allow` predicates aggregate: a call is rejected when at least one predicate is registered and none match. With no `Allow`, every call is accepted.
- The dry-run `Bus` sets `Mechanism: "dry_run"` only on payloads embedding `bus.Qualifiers`; other payloads are described unaugmented and the gap is logged once per Bus. `NewDryRunPublisher` augments pointer payloads in place and passes value payloads through unchanged, so pass `*T` to guarantee the tag.
- Dry-run guarantees no real writes, mutating HTTP calls or subprocesses **routed through these interfaces**. It does not chase spawned children, does not make compound state correct behind synthetic responses, and cannot see a call site that bypasses the seam.

## Neighbours

- [`real/`](real/), [`dryrun/`](dryrun/), [`testfake/`](testfake/): the three implementations.
- `go/console/cli`: registers `--dry-run`, resolves the policy, and owns `SetSideEffect` / `OptOutDryRun` / `SupportsDryRun`.
- `go/runtime/bus`: the `Qualifiers` payload slot the dry-run tag occupies.

## See also

- [Side-effect reference](../../../docs/adopters/reference/sideeffect.md): interfaces, implementation table, flag mechanics, policy and annotation tables, adoption, opt-out, migration, bus auto-tagging, guarantees and non-guarantees
- [CLI API reference](../../../docs/adopters/reference/cli-api-reference.md)
- ADR-0020: `--dry-run` unified with `kit/side-effect` (current policy)
- ADR-0019: this package and the global `--dry-run` (parent, partially superseded)
- ADR-0017: bus topic grammar and the `Qualifiers` convention
