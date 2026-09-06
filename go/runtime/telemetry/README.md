# telemetry

## What it answers

How a kit-based CLI learns how its command surface is used without
exfiltrating user data: a `Mode` gate, an anonymous rotatable
`installation_id`, a `ConsentHook` seam, and a batched HTTPS sink with
on-disk spool fallback. The prompt UX and persisted decision are
`go/core/consent`; the redact rules are `go/core/redact`.

## Use it when

- keep telemetry off → do nothing; `ModeOff` is the default
- emit anonymous usage → `SetMode(ModeAnon)` plus a wired `ConsentHook`
- emit args and flags post-redact → `SetMode(ModeFull)` plus `WithRedactor(...)`
- brand the env var → `SetAppPrefix("spaced")`, giving `SPACED_TELEMETRY_MODE`
- prefix topics → `WithTopicPrefix("<app>.telemetry.event")`
- override for one command → `WithMode(ctx, ModeFull)`
- ship over HTTPS → `NewHTTPSSink(url, WithTelemetryAuthEnv(...))`
- read, rotate or locate the identity → `InstallationID()`, `Rotate()`, `InstallIDPath()`, `ResetForTest()`
- drain and inspect the spool → `(*HTTPSSink).ReplaySpool(ctx)`, `.Stats()`
- verify redact fired → `SetRedactObserver(...)`

## Quick start

```go
telemetry.SetAppPrefix("spaced")
telemetry.SetConsentHook(consent.NewHook(store))

emitter, err := telemetry.New(
    telemetry.WithBus(b),
    telemetry.WithRedactor(telemetry.MustLoadRedactor()),
    telemetry.WithTopicPrefix("spaced.telemetry.event"),
    telemetry.WithKitVersion(buildVersion),
)
```

## Contract

- Three tiers: `ModeOff` (default, a zero-cost no-op reading one `atomic.Int32`), `ModeAnon` (`installation_id`, `command_path`, `exit_code`, `duration_ms`, `occurred_at`, `kit_version`, SDK lang and version), `ModeFull` (adds `args` and `flags` after redact, flag keys verbatim). `stdout` and `stderr` are NEVER captured at any tier.
- Emission also requires a granted `ConsentHook`. A nil hook resolves to default-deny, so upgrading kit never starts a stream by surprise.
- The emitter strips `Args` and `Flags` defensively whenever mode is not Full, even if the caller populated them.
- Mode precedence at `Record` time: `WithMode(ctx, m)`, then `SetMode(m)`, then `<APP>_TELEMETRY_MODE`, then `KIT_TELEMETRY_MODE`, then `ModeOff`. Env is read once per process and never again after `SetMode` or the first `CurrentMode`.
- `Record` is a soft-refusal API: off, denied or a failed install_id lookup all return nil without publishing, and only a bus-publish failure returns non-nil. Callers cannot detect the mode from the return value, by design.
- Identity is 32 raw `crypto/rand` bytes on disk, surfaced as their lowercase-hex SHA-256. The path `<XDG_STATE_HOME>/kit/telemetry/installation_id` is fixed, not per-tool, and shared with the polyglot SDKs; file `0600`, parent `0700`; first-call races settle via `O_EXCL`; `Rotate()` renames atomically over the live file.
- `HTTPSSink` sheds newest-wins from a ring (default cap 1024), batches at `WithBatchSize` (default 100) or `WithFlushInterval` (default 30s), retries with full-jitter exponential backoff (`WithMaxRetries` default 5, base 1s, capped 60s), bails immediately on `breaker.ErrBrokenCircuit`, and spools terminal failures to `<XDG_STATE_HOME>/kit/telemetry/spool/YYYY-MM-DD.jsonl` under `WithMaxSpoolBytes` (default 16 MiB, oldest-mtime evicted). `Close`/`CloseCtx` are idempotent and spool what they cannot ship.
- The Bearer token is re-read from `WithTelemetryAuthEnv` (default `KIT_TELEMETRY_AUTH_TOKEN`) on every POST, so operators rotate without restarting.
- Build-time endpoint: bake `DefaultEndpoint` via ldflags; `ResolveEndpoint(env, wire)` is a pure helper with precedence env, wire, `DefaultEndpoint`, empty. Empty means no HTTPS sink.
- Wire: topic `<prefix>.recorded` (default `kit.telemetry.event.recorded`), bus `Source` `kit.runtime.telemetry`, `schema_version` a string. `flushed`, `dropped` and `spool_overflowed` are reserved in the same Object space.

## Neighbours

- [`go/core/consent/`](../../core/consent/): prompt UX, persistence, decision-source resolution, and `kit consent reset`.
- [`go/core/redact/`](../../core/redact/): rule loading behind `MustLoadRedactor()`.
- [`go/runtime/bus/`](../bus/): topic grammar validator, `bus.NewEvent`, `bus.Sink`.
- [`go/console/cli/telemetry.go`](../../console/cli/telemetry.go): `cli.TelemetryConfig`, the typed bundle for `cli.WithTelemetry`.

## See also

- [Telemetry reference](../../../docs/adopters/reference/telemetry.md): intent table, happy path, mode and field-presence tables, identity, sink pipeline, build-time options, consent hook, wire format, concurrency notes
- [Telemetry compliance](../../../docs/adopters/reference/telemetry-compliance.md): the F13 `ConsentingTelemetry` checklist
