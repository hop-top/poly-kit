# `hop.top/kit/go/runtime/telemetry`

Opt-in, redact-before-egress CLI telemetry: a `Mode` gate, an anonymous
rotatable `installation_id`, a `ConsentHook` seam, and a batched HTTPS
sink with on-disk spool fallback. Adopters get one config knob (`Mode`)
that maps to a three-tier privacy contract.

> Three tiers — `ModeOff` (default, zero-cost no-op), `ModeAnon`
> (install_id + command + exit + duration), `ModeFull` (adds args/flags
> AFTER redact). Emission additionally requires a granted
> `ConsentHook`; the default hook denies. stdout / stderr are NEVER
> captured at any tier.

The schema is mirrored by polyglot SDKs and a cross-language contract
test diffs the wire shape.

## Why this exists

Kit-based CLIs want to know how their command surface is actually used
without exfiltrating user data. The two failure modes telemetry guards
against:

- **Surprise emission**: an existing adopter upgrades kit, suddenly
  starts sending events. `ModeOff` is the default and existing adopters
  get silent no-ops on upgrade.
- **Leak via the happy path**: a well-meaning adopter populates `Args`
  in `ModeAnon`, expecting the emitter to "do the right thing". It
  does — the emitter defensively strips `Args`/`Flags` before publish
  whenever `mode != "full"`, and runs the configured redactor over
  what survives.

The package is polyglot-aware: the on-disk `installation_id` lives at
a fixed path so SDK siblings (Python, TS, Rust, PHP — track
sdk-telemetry / sdk-telemetry-php) read the same identifier without
re-hashing. The wire `Event` shape is also part of the contract — JSON
tags, field order, and `omitempty` placement are diffed by the
cross-language contract test (track sdk-telemetry, T-0709).

## When to use what — intent table

| Adopter intent                                              | API                                                          |
|-------------------------------------------------------------|--------------------------------------------------------------|
| I want telemetry off — what's the default?                  | `ModeOff` (no-op; nothing to do)                             |
| I want to emit anonymous usage                              | `SetMode(ModeAnon)` + wire a `ConsentHook`                   |
| I want to emit full args/flags (post-redact)                | `SetMode(ModeFull)` + `WithRedactor(...)` (required)         |
| I want adopter-specific topic prefix                        | `WithTopicPrefix("<app>.telemetry.event")`                   |
| I want the env var to carry MY brand, not `KIT_*`           | `SetAppPrefix("spaced")` → reads `SPACED_TELEMETRY_MODE`     |
| I want a one-shot override for a single command             | `WithMode(ctx, ModeFull)` on that command's context          |
| I want HTTPS upload with batching + auth                    | `NewHTTPSSink(url, WithTelemetryAuthEnv(...))`               |
| I want to override which env var holds the bearer token     | `WithTelemetryAuthEnv("MY_TELEMETRY_TOKEN")`                 |
| I want to know my install_id                                | `InstallationID()`                                           |
| I want to rotate install_id                                 | `Rotate()` (called from `kit consent reset` CLI)             |
| I want to verify redact actually fired before egress        | `SetRedactObserver(...)` (track kit-telemetry-compliance)    |
| I want to drain spooled events after a network outage       | `(*HTTPSSink).ReplaySpool(ctx)`                              |
| I want diagnostics for `kit telemetry inspect`              | `(*HTTPSSink).Stats()` → `SpoolStats`                        |

## Adopter happy path

```go
import (
    "context"
    "time"

    "github.com/spf13/cobra"

    "hop.top/kit/go/core/consent"
    "hop.top/kit/go/runtime/bus"
    "hop.top/kit/go/runtime/telemetry"
)

// In your binary's main() / cobra setup:
func init() {
    telemetry.SetAppPrefix("spaced")                  // SPACED_TELEMETRY_MODE
    telemetry.SetConsentHook(consent.NewHook(store))  // from core/consent

    b, _ := bus.New( /* ... */ )
    redactor := telemetry.MustLoadRedactor()          // panics if 0 rules
    emitter, err := telemetry.New(
        telemetry.WithBus(b),
        telemetry.WithRedactor(redactor),
        telemetry.WithTopicPrefix("spaced.telemetry.event"),
        telemetry.WithKitVersion(buildVersion),
    )
    // handle err ...
}

// In a command's RunE:
func runE(cmd *cobra.Command, args []string) error {
    start := time.Now()
    // ... do the work ...
    exitCode := 0

    _ = emitter.Record(cmd.Context(), telemetry.Event{
        CommandPath: []string{cmd.CommandPath()},
        ExitCode:    exitCode,
        DurationMS:  time.Since(start).Milliseconds(),
        Args:        args, // stripped in Anon, redacted in Full
    })
    return nil
}
```

`Record` is a soft-refusal API: when mode is `Off`, consent is denied,
or the install_id lookup fails, it returns `nil` without publishing.
The only non-nil return is a bus-publish failure. Callers cannot
distinguish "telemetry off" from "telemetry succeeded" by the return
value — that ambiguity is intentional (a privacy-respecting emitter
exposes no channel by which a consumer can detect mode).

## Modes

| Mode       | What it carries                                                                                            | When to use                                                  | Default |
|------------|------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------|---------|
| `ModeOff`  | Nothing. `Record` is a zero-cost no-op (reads one `atomic.Int32`).                                         | Production default. Backward-compatible with existing adopters. | Yes     |
| `ModeAnon` | `installation_id`, `command_path`, `exit_code`, `duration_ms`, `occurred_at`, `kit_version`, SDK lang/ver. | You want operational signals without any user input.         | No      |
| `ModeFull` | `ModeAnon` plus `args` (post-redact) and `flags` (values post-redact, keys verbatim).                      | You want full command-surface analytics and trust your redact rules. | No      |

Mode precedence (resolved at `Record` time, top wins):

1. `WithMode(ctx, m)` per-invocation override.
2. `SetMode(m)` package-global (typical: called from `init()`).
3. `<APP>_TELEMETRY_MODE` env (requires `SetAppPrefix(...)`).
4. `KIT_TELEMETRY_MODE` env (universal fallback).
5. `ModeOff`.

`<APP>_TELEMETRY_MODE` wins over `KIT_TELEMETRY_MODE` because the
adopter's brand is the user's mental model. Both names are read once
per process; after `SetMode` or the first `CurrentMode` call, env
vars are never consulted again.

## Anonymous vs Full — field presence

| Field                                       | Anon  | Full         |
|---------------------------------------------|-------|--------------|
| `installation_id` (64-char lowercase hex)   | yes   | yes          |
| `command_path` ([]string)                   | yes   | yes          |
| `exit_code`                                 | yes   | yes          |
| `duration_ms`                               | yes   | yes          |
| `occurred_at` (RFC 3339 UTC, nanos)         | yes   | yes          |
| `kit_version`, `sdk_lang`, `sdk_version`    | yes   | yes          |
| `args` ([]string)                           | NEVER | post-redact  |
| `flags` (map[string]string, values redacted) | NEVER | post-redact  |
| `stdout` / `stderr`                         | NEVER | NEVER        |

The emitter strips `Args`/`Flags` defensively when `mode == ModeAnon`
even if the caller populated them (see `emitter.go` step 4).

## Identity — install_id

- **On-disk format**: 32 raw bytes from `crypto/rand`. NOT hex.
- **On-read derivation**: SHA-256 of those 32 bytes, rendered as
  lowercase hex (64 chars). The hash is the `installation_id` that
  flows through events.
- **Path**: `<XDG_STATE_HOME>/kit/telemetry/installation_id` — FIXED,
  not per-tool. Polyglot SDKs share the same file.
- **Permissions**: file `0600`, parent directory `0700`.
- **First-call race-safety**: concurrent first calls from multiple
  processes use `O_EXCL`; the first writer wins, losers re-read.
- **Rotation**: `Rotate()` writes to `installation_id.new`, then
  `os.Rename` atomically over the live file. An interrupted rotation
  can never leave a half-written file. Rotation is exposed only as
  the function; the rotation CLI (`kit consent reset`) lives in
  `go/core/consent/`.
- **`InstallIDPath()`** is exported so SDKs and compliance checks can
  verify the storage location without invoking the read path.
- **`ResetForTest()`** removes the file (and stale `.new`) so adopters
  can drive first-run code paths in their own tests.

## Sink — HTTPS batching + spool + retry

`HTTPSSink` implements `bus.Sink`. Pipeline:

1. `Drain(ctx, ev)` enqueues into an in-memory ring (default cap
   1024). On full ring, the OLDEST event is evicted and
   `SpoolStats.DroppedOverflow` is incremented; the new event is
   admitted (newer-event-wins shedding).
2. When the ring crosses `WithBatchSize` (default 100) or the
   `WithFlushInterval` timer fires (default 30s), a background worker
   drains the ring into a single NDJSON batch.
3. The batch is POSTed with exponential-backoff retry
   (`WithMaxRetries`, default 5; base 1s × 2^attempt, full jitter,
   capped at 60s). 2xx clears; `breaker.ErrBrokenCircuit` bails
   immediately (retry would defeat the breaker).
4. On terminal failure the batch is appended to today's spool file
   `<XDG_STATE_HOME>/kit/telemetry/spool/YYYY-MM-DD.jsonl`. Total
   spool size is capped by `WithMaxSpoolBytes` (default 16 MiB);
   oldest-mtime files are evicted when the cap is exceeded.
5. `ReplaySpool(ctx)` walks the spool dir oldest-first, POSTs each
   file, and removes on 2xx. Failures leave files in place for the
   next attempt.

Auth: the Bearer token is read on every POST from the env var named by
`WithTelemetryAuthEnv` (default `KIT_TELEMETRY_AUTH_TOKEN`). Reading at
post time (not boot time) matches `bus.AuthFromEnv`'s dynamic-config
posture — operators can rotate tokens without restarting.

Offline behaviour: when the upstream is unreachable, batches accumulate
in the spool with bounded growth. Operators enforce retention with
`find -mtime +N`. `Stats()` returns the snapshot consumed by
`kit telemetry inspect`.

`Close()` / `CloseCtx(ctx)` are idempotent; pending events that can't
be shipped within the context deadline are spooled.

## Consent hook

```go
type ConsentHook interface {
    Granted(ctx context.Context) bool
}
```

This package owns the interface; the actual prompt UX, persistence,
and decision-source resolution live in
[`hop.top/kit/go/core/consent`](../../core/consent/). kit-consent
installs the persisted-decision reader at `cobra.OnInitialize` via
`SetConsentHook(...)`.

Defaults:

- Default-deny. A nil `ConsentHook` resolves to an internal
  `denyHook{}`. Upgrading kit never starts a telemetry stream by
  surprise.
- `SetConsentHook(nil)` resets to default-deny rather than storing
  nil; `CurrentConsentHook()` is always non-nil.
- `WithConsentHook(ctx, h)` is a per-invocation override; tests use it
  to install a permissive hook without touching global state. A
  `nil` ctx is tolerated and treated as `context.Background()`.

The `bool` return is deliberately narrower than a `State` /
`decision_source` enum: the cross-package contract only needs the
emit-gate decision. The richer Decision value object (state + source)
lives inside kit-consent and never crosses the emitter boundary.

## Wire format

Topic: `kit.telemetry.event.recorded` (default). Adopters who set
`WithTopicPrefix("spaced.telemetry.event")` publish on
`spaced.telemetry.event.recorded`. Bus `Source` is
`kit.runtime.telemetry`.

Reserved-but-not-yet-emitted future actions in the same Object space:
`flushed`, `dropped`, `spool_overflowed`. The namespace is claimed
now to avoid a future collision with the bus grammar validator.

Audit topic: `kit.telemetry.redact.matched` — `kit-telemetry-compliance`
T-0702 subscribes to verify redact actually fired before any Full
payload reached a sink.

**Anon payload:**

```json
{
  "schema_version": "1",
  "sdk_lang": "go",
  "sdk_version": "v0.4.0-alpha.2",
  "installation_id": "0e3b…f9",
  "mode": "anon",
  "command_path": ["spaced", "search"],
  "exit_code": 0,
  "duration_ms": 142,
  "occurred_at": "2026-05-19T12:34:56.789012345Z",
  "kit_version": "v0.4.0-alpha.2"
}
```

**Full payload** (adds `args`/`flags`, both post-redact):

```json
{
  "schema_version": "1",
  "sdk_lang": "go",
  "sdk_version": "v0.4.0-alpha.2",
  "installation_id": "0e3b…f9",
  "mode": "full",
  "command_path": ["spaced", "search"],
  "exit_code": 0,
  "duration_ms": 142,
  "occurred_at": "2026-05-19T12:34:56.789012345Z",
  "kit_version": "v0.4.0-alpha.2",
  "args": ["foo", "<REDACTED:email>"],
  "flags": {"--token": "<REDACTED:openai-api-key>"}
}
```

Contract notes:

- `schema_version` is a **string** (`"1"`), not an int — weakly-typed
  SDKs round-trip int/float/string inconsistently.
- Timestamp key is **`occurred_at`** (matches past-tense action
  `recorded`). Never `timestamp` or `ts`.
- Flag KEYS are preserved verbatim; only VALUES go through redact.
- `Event.Validate()` enforces the schema contract; tier-specific rules
  (Anon MUST NOT include `args`/`flags`) are enforced by the emitter
  earlier in the pipeline. Sentinels: `ErrSchemaVersion`,
  `ErrInstallID`, `ErrMode`, `ErrCommandPath`, `ErrOccurredAt`,
  `ErrSDKLang`. Match with `errors.Is`.

### SDKVersion and vendored embeds

The emitter auto-discovers `sdk_version` via `debug.ReadBuildInfo()` at
process start, reading the module version of `hop.top/kit` from the
build graph. This works for the common case — adopters who consume
kit as a Go module dependency.

It does NOT work for **vendored embeds**: when an adopter copies
`go/runtime/telemetry` (or the entire kit module) into their own
`vendor/` tree or rewrites it via `replace`, `debug.ReadBuildInfo()`
returns no module info for kit and `sdk_version` falls back to the
empty string on the wire. Downstream consumers then can't tell which
kit revision produced the event.

Adopters with vendored builds (or any build mode that strips module
info — including `go build -trimpath` combined with certain ldflag
configurations) should set the SDK version explicitly. Two options:

```go
// Option 1: explicit option at emitter construction
emitter, _ := telemetry.New(
    telemetry.WithSDKVersion("0.4.0"),
    // ... other options
)

// Option 2: build-time ldflags (when the version is known at link time)
//   go build -ldflags "-X hop.top/kit/go/runtime/telemetry.sdkVersionOverride=0.4.0"
```

When set explicitly, `WithSDKVersion` short-circuits the
`ReadBuildInfo` path entirely and the supplied string is what flows
onto the wire as `sdk_version`. See T-0674 for the discovery context.

## Concurrency notes

- `CurrentMode()` resolves the env-precedence chain inside a
  `sync.Once`, establishing a happens-before edge between the
  goroutine that consults the env and concurrent first-callers.
  Without it, CAS-losers would race the CAS-winner's `Store` and
  frequently observe the stale `ModeOff` default.
- `globalMode` is an `atomic.Int32`; `appPrefix`, `globalHook`, and
  `redactObserver` are `atomic.Value` storing fixed-type wrappers
  (`hookHolder`, `observerBox`) — `atomic.Value` panics on
  type-inconsistent `Store`, and a `nil`-returning hook would
  nil-deref the emit hot path.
- `InstallIDPath()` is serialised by a package-level `pathMu`. The
  underlying `adrg/xdg` package mutates package-level state inside
  `Reload()`; without the mutex two goroutines calling
  `InstallationID()` simultaneously race on those globals (`-race`
  flags it). The mutex is contended only during the `StateFile`
  resolve itself — microseconds.
- `Rotate()` writes to `installation_id.new` then `os.Rename` — POSIX
  atomic rename guarantees readers always see either the old or the
  new file, never a half-written one.
- `HTTPSSink` separates `mu` (ring) from `spoolMu` (disk I/O) so a
  slow disk does not block publishers; `flushCh` is buffered cap 1
  for coalesced signals; the background worker exits on `doneCh`
  close (set by `Close`).

## See also

- [`go/core/consent/`](../../core/consent/) — persisted decision +
  resolver + `consent.NewHook(...)` that satisfies `ConsentHook`.
- [`go/core/redact/`](../../core/redact/) — redact rule loading;
  `MustLoadRedactor()` is the kit-blessed entry point.
- [`go/runtime/bus/`](../bus/) — topic grammar validator,
  `bus.NewEvent`, `bus.Sink`.
- [`go/runtime/provenance/`](../provenance/) — the `Mode` + atomic +
  one-shot env-read idiom this package mirrors.
- Sibling tracks: `kit-consent`, `kit-telemetry-compliance`,
  `cmdsurf-telemetry`, `sdk-telemetry`, `sdk-telemetry-php`.
