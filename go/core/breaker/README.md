# breaker

## What it answers

How much, how fast, how often an operation may run before the tool stops
itself: file writes, exec spawns, HTTP calls, token spend. Which paths an
operation may touch is `go/core/scope`; what text may leave the process is
`go/core/redact`.

## Use it when

- bound calls per interval or in-flight ops → `breaker.New(name, breaker.MaxPerMinute(n), breaker.MaxConcurrent(n))`
- cap cumulative bytes or ops → `breaker.MaxBytes(n)`, `breaker.MaxOps(n)`
- trip after consecutive failures, recover after a delay → `breaker.WithCircuit(breaker.CircuitOpts{...})`, `breaker.ResetAfter(d)`
- route to an alternate path on trip → `breaker.OnTrip(breaker.Degrade)` + `breaker.Fallback(fn)`
- guard a call site → `WrapCtx`, `WrapValue`, `WrapBytes`, `WrapWriter`, `WrapReader`, `WrapHTTP`
- load fuses declared in `breaker.yaml` → `breaker.FromConfig(tool)`
- inspect from the shell → `kit breaker list | show | reset`

## Quick start

```go
b := breaker.New("exec-spawns",
    breaker.MaxConcurrent(4),
    breaker.MaxPerMinute(30),
)

err := breaker.WrapCtx(b, ctx, func(ctx context.Context) error {
    return exec.CommandContext(ctx, "convert", args...).Run()
})
```

## Contract

- `Allow()` returns `ErrBrokenCircuit` when open; `Record(success, n)` feeds counters and the state machine; `Reset()` closes and zeroes counters.
- `OnTrip(Degrade)` without `Fallback` behaves like `Halt`; `OnTrip(Warn)` is log-only, for migration and known soft-failure modes.
- `MaxPerMinute` is bursty (period-windowed); `Timeout(d)` applies to `Executor().Run/Get`, not `Allow`.
- Bus topics `kit.core.breaker.{tripped,opened,closed,half_opened}` publish only when `WithPublisher` is wired; `WithTopicPrefix` / `WithTopics` rename them.
- `breaker.yaml`: `~/.config/<tool>/breaker.yaml` merged over `/etc/xdg/<tool>/breaker.yaml`; unknown keys are an error.
- State is per process and in memory; `Lookup` / `List` / `ResetAll` see this process only.
- `kit breaker` exit codes: `0` ok, `1` not-found, `2` usage error.

## Neighbours

- [`policy/`](policy/): native `MaxBytes` (Volume) and `MaxOps` (Count) policies that failsafe-go does not ship.
- `go/core/scope`: path policy; neither package imports the other, FS-touching code consults both.
- `go/core/redact`: secrets and PII leaving the process.
- `go/console/cli/breaker`: the `kit breaker` subcommand tree.

## See also

- [Breaker reference](../../../docs/adopters/reference/breaker.md): policy table, API surface, wrap helpers, examples, YAML schema, limitations
- [Go primitives index](../../../docs/adopters/reference/go-primitives.md#i-need-guardrails-on-what-my-tool-can-do)
- ADR-0006: why failsafe-go, alternatives considered
