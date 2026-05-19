# ADR-0035: Runtime telemetry — tier model, identity, topics, consent

- Status: Accepted (v2 amendments 2026-05-19)
- Date: 2026-05-19
- Tracks: kit-telemetry, kit-consent, kit-telemetry-compliance,
  cmdsurf-telemetry, sdk-telemetry, sdk-telemetry-php

## Context

Six tracks ship telemetry across the kit ecosystem in parallel:
`kit-telemetry` owns the Go runtime emitter; `kit-consent` owns the
user-facing gate and rotation CLI; `kit-telemetry-compliance` owns
the redact-check audit observer; `cmdsurf-telemetry` is the first
adopter (CLI command-surface analytics); `sdk-telemetry` and
`sdk-telemetry-php` mirror the contract into the polyglot SDKs.

A post-plan cross-track sweep surfaced ten contract drifts. This ADR
locks the canonical decisions BEFORE any track writes code, so every
downstream PR can cite a single source of truth instead of replaying
the design debate.

The shape of the design mirrors ADR-0024 (`kit/provenance` lint +
guardrail) deliberately: a `Mode` enum + atomic global + one-shot
env-var read + per-context override. The semantics differ —
provenance gates VALIDITY (does the call fail?), telemetry gates
WHAT (do we emit, and with how much detail?) — but the idiom is the
same, so adopters learn the pattern once.

Topic grammar follows ADR-0017 (`[Source].[Category].[Object].[Action]`,
past-tense). The `kit.telemetry.*` Object space was empty at the
time of writing (grep of `go/runtime/bus/topics.go` +
`topics_test.go` confirms no reservation).

## Decision

### 1. Tier model: `Mode{Off, Anon, Full}`

Three-tier `Mode` defined in `go/runtime/telemetry/mode.go`, mirroring
`go/runtime/provenance/mode.go` line-for-line where the idiom
transfers:

```go
type Mode int32

const (
    ModeOff  Mode = iota // default; zero-cost no-op emit path
    ModeAnon             // install_id + command + exit + duration only
    ModeFull             // adds args/flags AFTER redact
)
```

`Off` is the default. Existing adopters get silent no-ops on upgrade;
backward compatibility is the entire point.

We do NOT reuse provenance's `Off/Warn/Strict` tokens verbatim:
provenance gates whether `Render` returns an error on missing
entries (a VALIDITY decision); telemetry gates payload detail (a
WHAT-WE-EMIT decision). Reusing the same tokens with different
semantics would invite operator confusion.

### 2. Mode env precedence

Resolved bottom-up at the point of `Record`:

1. `WithMode(ctx, m)` per-invocation override wins.
2. `SetMode(m)` package-global (called from `init()`).
3. `<APP>_TELEMETRY_MODE` env var — app prefix is the build-time
   const `telemetry.AppName` (defaulting to `filepath.Base(os.Args[0])`).
4. `KIT_TELEMETRY_MODE` env var — universal fallback.
5. `ModeOff` default.

`<APP>_TELEMETRY_MODE` wins over `KIT_TELEMETRY_MODE` because the
adopter's brand of the CLI is the user's mental model. Both names
are read once per process via `envModeApplied atomic.Bool`.

### 3. Topic family

Two topics in v1, both under the reserved `kit.telemetry.*` Object
space:

| Topic | Direction | Subscriber |
|-------|-----------|------------|
| `kit.telemetry.event.recorded` | publish | adopters, sinks |
| `kit.telemetry.redact.matched` | publish (audit observer) | `kit-telemetry-compliance` T-0702 redact-check |

Adopters set their own prefix via the existing `WithTopicPrefix`
option (e.g. `cmdsurf.telemetry.event.recorded`).

Reserved-but-not-emitted future actions in the same Object space:
`flushed`, `dropped`, `spool_overflowed`. Claiming the namespace
now prevents collision with the bus-grammar validator when those
actions are needed.

`kit.telemetry.redact.matched` is NOT a dead topic — `kit-telemetry-
compliance` T-0702 subscribes to it to verify redact actually fired
before any Full payload reached a sink. Coordinated lifecycle.

### 4. Identity model

Anonymous, rotatable `installation_id`:

- **On-disk format**: 32 raw bytes from `crypto/rand`. NOT hex.
- **On-read derivation**: SHA-256 of those 32 bytes, rendered as
  lowercase hex (64 chars). The hash is the `installation_id` that
  flows through events.
- **Path**: `<XDG_STATE_HOME>/kit/telemetry/installation_id` —
  FIXED, not per-tool. Polyglot SDKs share the same file; per-tool
  interpolation would break SDK reads.
- **Permissions**: file `0600`, parent directory `0700`.
- **Atomic rotate**: write to `installation_id.new`, then `rename`.
- **Rotation API**: `func Rotate() (string, error)` lives in
  `go/runtime/telemetry` (this track).
- **Rotation CLI**: `kit consent reset` (or equivalent) lives in
  the `kit-consent` track. The Go runtime exposes only the function.

Bytes-on-disk (not hex-on-disk) is required because SDK plans
already pin bytes; if Go wrote hex and an SDK read it, the SDK
would re-hash the hex string and diverge.

### 5. Consent-hook interface

`kit-telemetry` owns the interface; `kit-consent` implements it.

```go
package telemetry

type ConsentHook interface {
    Granted(ctx context.Context) bool
}

// denyHook is the default when no hook is wired.
type denyHook struct{}

func (denyHook) Granted(context.Context) bool { return false }
```

Reject the alternative `Gate.Allow(ctx) (State, error)` shape from
the kit-consent draft: `bool` is sufficient for the emit-gate
decision. The `State` enum and `decision_source` enum are
subcommand concerns (rendering "why did this prompt show?") and
belong inside `kit-consent`'s public API, not on the cross-package
seam.

Default hook denies. A nil hook is treated as `denyHook{}`. Tests
in `kit-telemetry` use a permissive in-memory hook until
`kit-consent` lands.

### 6. Anon vs Full payload

| Field | Anon | Full |
|-------|------|------|
| `installation_id` | yes | yes |
| `command_path` (argv0 + subcommands) | yes | yes |
| `exit_code` | yes | yes |
| `duration_ms` | yes | yes |
| `occurred_at` (RFC 3339, nanos) | yes | yes |
| `kit_version` | yes | yes |
| `sdk_lang` ("go" canonical; "py", "ts", "rs", "php" reserved) | yes | yes |
| `sdk_version` | yes | yes |
| `args []string` | NEVER | yes, post-redact |
| `flags map[string]string` (values redacted, keys preserved) | NEVER | yes, post-redact |
| `stdout` / `stderr` | NEVER | NEVER |

The emitter defensively strips `args`/`flags` when `mode == ModeAnon`
even if the caller populated them. Capturing stdout/stderr is out
of scope at any tier — that's an observability-platform job, not a
telemetry job.

### 7. Event schema

```go
const SchemaVersion = "1" // STRING, not int

type Event struct {
    SchemaVersion  string            `json:"schema_version"`
    InstallationID string            `json:"installation_id"`
    Mode           string            `json:"mode"` // "anon" | "full"
    CommandPath    []string          `json:"command_path"`
    ExitCode       int               `json:"exit_code"`
    DurationMS     int64             `json:"duration_ms"`
    OccurredAt     time.Time         `json:"occurred_at"` // RFC 3339
    KitVersion     string            `json:"kit_version"`
    SDKLang        string            `json:"sdk_lang"`    // "go" | "py" | ...
    SDKVersion     string            `json:"sdk_version"`
    Args           []string          `json:"args,omitempty"`
    Flags          map[string]string `json:"flags,omitempty"`
}
```

- `schema_version` is a **string** (`"1"`), not an int. SDKs in
  weakly typed languages otherwise round-trip it inconsistently
  (e.g. `1` vs `1.0` vs `"1"`).
- Timestamp key is **`occurred_at`**. NOT `timestamp` (drifts from
  past-tense topic grammar) and NOT `ts` (cryptic).
- `sdk_lang` + `sdk_version` are additive; they let
  cross-language analytics distinguish the same `command_path`
  emitted from different SDK runtimes without bumping
  `schema_version`.

### 8. Spool locations

| Path | Owner | Purpose |
|------|-------|---------|
| `<XDG_STATE_HOME>/kit/telemetry/spool/YYYY-MM-DD.jsonl` | kit-telemetry HTTPS sink | retry spool when sink returns 5xx / network error |
| `<XDG_STATE_HOME>/kit/telemetry/inbox/php-<pid>.jsonl` | sdk-telemetry-php | per-process inbox drained by a (hypothetical, future) Go daemon |

Dated spool file (`YYYY-MM-DD.jsonl`) keeps unbounded growth bounded
by an externally enforceable retention policy (`find -mtime +N`).

PHP's per-pid inbox isolates emitters that share a `kit/telemetry`
directory but cannot share a buffer (no long-lived process).

### 9. ADR numbering

| ADR | Track | Subject |
|-----|-------|---------|
| 0035 | kit-telemetry (this ADR) | tier model, identity, topics, consent interface |
| 0036 | kit-consent | consent gate, decision sources, rotation CLI |
| 0037 | kit-telemetry-compliance | redact-check + audit subscribers |
| 0038 | sdk-telemetry | delta from the Go runtime contract (per-language storage, packaging) |

`kit-consent` and the SDK tracks read this ADR before claiming
their slot. `cmdsurf-telemetry` is an adopter; no ADR.

### 10. Future-proofing

- `bus.Qualifiers` is embedded in the on-wire `Event` shape (via
  `omitempty`), so reason / mechanism / property / circumstance are
  available without bumping `schema_version`.
- Adopters with huge `--config` blobs should set `ModeAnon`
  explicitly; redact is O(payload size) and telemetry sinks budget
  on the assumption of tiny payloads (single command line + handful
  of flags).

## Alternatives considered

### A. Reuse provenance's `Off/Warn/Strict`

Rejected: same tokens, different semantics (VALIDITY vs WHAT) would
confuse adopters who read both ADRs. The fact that the *idiom*
(atomic + one-shot env-read + ctx override) transfers is enough.

### B. Hex-on-disk for installation_id

Rejected: SDK plans pin bytes-on-disk. Hex-on-disk in Go would
diverge on SDK re-hash.

### C. `Gate.Allow(ctx) (State, error)` consent shape

Rejected: more surface than the seam needs. The boolean is enough
to gate emit; State + decision_source are presentation concerns
internal to `kit-consent`.

### D. Per-tool installation_id path

Rejected: polyglot identity sharing requires a fixed path. Per-tool
interpolation breaks SDK reads of the same machine's identity.

### E. `schema_version` as int

Rejected: weakly-typed SDKs (Python pre-typeshed, PHP, JS) round-
trip int vs float vs string inconsistently. String avoids the
edge case entirely.

### F. `timestamp` / `ts` JSON key

Rejected: `timestamp` is tense-neutral (grammar drift from past-
tense topics); `ts` is cryptic. `occurred_at` matches the topic
action `recorded` and is self-describing.

## Consequences

- **Adopters** get a single config knob (`Mode`) that maps to a
  three-tier privacy contract. Default-off means upgrading kit
  never starts a telemetry stream by surprise.
- **`kit-consent`** must implement `ConsentHook` exactly as defined
  here. Its public API (state machine, CLI commands) is otherwise
  free; the bool seam is the only cross-package contract.
- **`kit-telemetry-compliance`** must subscribe to
  `kit.telemetry.redact.matched`. Without that subscriber, the
  audit topic is dead and the redact-check race-test
  (T-0702) cannot run.
- **SDKs** read 32-byte `installation_id` files, hash on read,
  emit events with `schema_version: "1"` (string) and
  `occurred_at` (RFC 3339, nanos). Per-language ADR (ADR-0038)
  records storage / packaging deltas only.
- **Bus topic grammar (ADR-0017)** is preserved. The Object
  `telemetry` is now reserved.
- **Provenance idiom (ADR-0024)** is the reference for the
  `Mode` + atomic + env-read pattern; this is the second package
  to adopt it, establishing it as kit's house style for
  runtime-mode globals.

## References

- ADR-0017 — bus topic naming grammar and Qualifiers payload
- ADR-0024 — `kit/provenance` lint + guardrail (idiom source)
- `go/runtime/provenance/mode.go` — implementation reference for
  the `Mode` enum + atomic + env-read pattern
- `go/runtime/bus/topics.go` — topic grammar validator
- `core/redact` README — redact rule loading + audit observer
- `core/xdg` README — `xdg.StateFile` semantics
- Track plans:
  `.tlc/tracks/kit-telemetry/plan.md`,
  `.tlc/tracks/kit-consent/plan.md`,
  `.tlc/tracks/kit-telemetry-compliance/plan.md`,
  `.tlc/tracks/cmdsurf-telemetry/plan.md`,
  `.tlc/tracks/sdk-telemetry/plan.md`,
  `.tlc/tracks/sdk-telemetry-php/plan.md`

## v2 amendments (post-implementation reconciliation)

Date: 2026-05-19.

These amendments reconcile the ADR with what actually shipped. Tracks
T-0674, T-0694, T-0707, and T-0708 surfaced contract drifts between
this ADR's enumerations and the landed code in
`go/runtime/telemetry/event.go`, `sink_https.go`, and the cmdsurface
adopter (`go/transport/cmdsurface/sink_telemetry.go`). The original
analysis above is preserved verbatim — ADRs accrete history rather than
overwrite it, so future readers can see what was decided, what was
later observed, and what shifted.

### v2-A1. `Surface` field on the canonical Event (amends #6)

Decision #6 enumerated payload fields but omitted `Surface` — the
transport-surface name (`cli`, `rest`, `ws`, `lambda`, …) that the
cmdsurf-telemetry adopter needs in BOTH Anon and Full modes for
attribution. Today's workaround in `sink_telemetry.go` synthesises
`Flags["_surface"]` — but `Flags` is gated to Full mode by #6, so Anon
events currently lose surface attribution entirely.

Amendment:

- ADD `Surface string` to the canonical Event struct, present in BOTH
  Anon and Full modes.
- JSON key: `surface` (snake_case to match the rest of the wire shape).
- Anon-safety: `Surface` is a bounded small enum (per ADR-0040
  cmdsurface surface registry). It carries no user content, so it is
  safe to include at the Anon tier — the Anon promise ("no flags, no
  args") is preserved.
- Follow-up: the code change to add the field belongs to
  `hops/main/go/runtime/telemetry/event.go` and is FILED for
  engineering follow-up; this ADR amendment does NOT perform it.
- Migration: once the field lands, cmdsurf-telemetry should retire the
  `Flags["_surface"]` workaround in `shipOne`.

### v2-A2. `trace_id` enumerated in the wire schema (amends #7)

Decision #7's Go struct snippet enumerated schema_version, sdk_lang,
installation_id, mode, command_path, exit_code, duration_ms,
occurred_at, kit_version, sdk_lang, sdk_version, args, flags — but did
NOT enumerate `TraceID`. The shipped `event.go` HAS the field:

```go
TraceID string `json:"trace_id,omitempty"`
```

and `cmdsurface.TelemetrySink` already propagates it via
`inv.Meta.TraceID` for trace correlation.

Amendment:

- ADD `trace_id` to the Decision #7 wire-schema enumeration.
- Semantics: optional, `omitempty`; populated when the invocation
  carries trace context (cmdsurf-telemetry forwards `Meta.TraceID`).
- Anon-safety: a trace id is an opaque correlator with no user payload,
  safe at both tiers.
- Forward path: the cmdsurf-otel track will populate `Meta.TraceID`
  from W3C `traceparent`, so the field is the cross-language seam for
  trace propagation across language SDKs.

### v2-A3. `kit_version` omitempty clarification (amends #6)

Decision #6's payload table says `kit_version: yes` for both tiers,
which read as "always present on the wire". The shipped struct tag is:

```go
KitVersion string `json:"kit_version,omitempty"`
```

so when an adopter constructs an emitter WITHOUT
`WithKitVersion("...")` (or constructs cmdsurface
`TelemetrySink` without `WithKitVersion`), the field is absent from
the wire entirely.

Amendment:

- `kit_version` is OPTIONAL on the wire (`omitempty`). Set when the
  emitter / adopter sink is constructed with `WithKitVersion`.
- Practical guidance: most adopters should set it via build-time
  ldflags (`-X 'main.kitVersion=v1.2.3'`) and pass through to
  `WithKitVersion`. The field is `omitempty` so legacy adopters who
  upgrade kit but do NOT wire `WithKitVersion` are not broken — their
  events simply ship without the column.
- Tier rule unchanged: when present, `kit_version` is emitted at both
  Anon and Full. The change here is wire-presence, not tier-gating.

### v2-A4. `Stats()` observability surface on sinks (amends #8)

Decision #8 specified spool layout but did not document the
observability surface on sinks. The shipped code exposes a canonical
`Stats()` method on both the HTTPS sink and the cmdsurface adopter
sink, returning typed snapshots that `kit telemetry inspect` (and any
operator tooling) can sample.

Amendment — canonical `Stats()` API on emitter sinks:

- `(*telemetry.HTTPSSink).Stats() SpoolStats` — fields:
  - `PendingInMemory int` — events buffered in the ring, awaiting flush.
  - `SpoolFiles int` — count of `.jsonl` files under the spool dir.
  - `SpoolBytes int64` — total bytes across spool files.
  - `DroppedOverflow int64` — monotonic counter of ring-buffer
    evictions (oldest-event-dropped under back-pressure).
- `(*cmdsurface.TelemetrySink).Stats() TelemetryStats` — fields:
  - `Emitted int64` — events the drain successfully handed to the
    emitter (no error).
  - `DroppedFull int64` — events refused at `Emit` time because the
    cmdsurface→drain channel was saturated.
  - `DroppedOversize int64` — events the drain refused because the
    translated `telemetry.Event` JSON exceeded `MaxBytes`. (Note:
    truncation is deliberately NOT applied; truncating a redacted
    payload could leak a token prefix.)
  - `DroppedDenied int64` — events the emitter rejected with a non-nil
    error (hard failure: bus publish error, validate failure escaping
    the soft-refuse path). Mode/consent refusals are SOFT and counted
    as `Emitted`.
  - `RequestedAtMissing int64` — events whose surface failed to stamp
    `Meta.RequestedAt`; the event still ships with `DurationMS = -1`.

Standard counter-name vocabulary across sinks: `Emitted`,
`DroppedFull`, `DroppedOversize`, `DroppedDenied`, `DroppedOverflow`.
Future sinks SHOULD reuse these names so dashboards and operator
tooling can scrape uniformly.

Future surface: `kit telemetry inspect` (kit-consent track) is
expected to render these counters in a future release. The Go API is
already stable; only the CLI front-end is pending.

### v2-A5. Complete enumeration of `HTTPSSink` options (amends #8)

T-0694 noted the README references `WithRingCap` but Decision #8 of
this ADR did not enumerate the option set. Reading
`sink_https.go` the canonical set is:

- `WithBatchSize(n int)` — in-memory batch threshold. Default 100;
  values < 1 clamp to 1.
- `WithFlushInterval(d time.Duration)` — wall-clock flush deadline.
  Default 30s; `<= 0` disables time-based flushing (size-only).
- `WithSpoolDir(path string)` — overrides
  `<XDG_STATE_HOME>/kit/telemetry/spool`. For tests and adopters with
  custom state roots.
- `WithMaxSpoolBytes(n int64)` — caps the on-disk spool. Default 16 MiB;
  oldest spool files are evicted when exceeded.
- `WithHTTPClient(client *http.Client)` — overrides the default
  client (10s timeout, default transport).
- `WithTelemetryAuthEnv(envVar string)` — env var name consulted for
  the Bearer token. Default `KIT_TELEMETRY_AUTH_TOKEN`. Read at Drain
  time (dynamic; not snapshotted at construction).
- `WithMaxRetries(n int)` — total HTTP attempts per batch (initial
  try + retries). Default 5; values < 1 clamp to 1.
- `WithRingCap(n int)` — in-memory ring-buffer capacity. Default 1024.
  When full, Drain drops the OLDEST event (incrementing
  `DroppedOverflow`) rather than blocking the caller — newer-event-wins
  shedding so the latest signal stays alive under load.

Together these are the canonical operator/tuning surface for the
HTTPS sink. Adopters wiring their own sinks should mirror the
`With*` option-bag idiom for consistency.
