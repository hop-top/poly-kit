# Telemetry event schema (cross-language contract)

> **Status**: Canonical contract for `schema_version = "1"`.
> **Ground truth**: [`hops/main/go/runtime/telemetry/event.go`](../../go/runtime/telemetry/event.go).
> **Diffed by**: cross-language contract test under
> `hops/main/sdk/tests/cross-lang/telemetry/`.

This document is the single source of truth for the on-wire JSON shape
that every kit telemetry runtime emits — the Go canonical
implementation plus the polyglot SDKs (`py`, `ts`, `rs`, `php`). Each
per-language SDK reads THIS doc to know exactly what bytes to put on
the bus.

If a field appears in `event.go` but not here, the doc is stale: fix
the doc. If a field appears here but not in `event.go`, the doc is
wrong: fix the doc. Code is canonical.

## 1. Canonical JSON examples

### 1a. Anon mode

```json
{"schema_version":"1","sdk_lang":"go","sdk_version":"0.4.0","installation_id":"8c98a22cdc9a22a5233b2b241a5e28eb3daf4ffc6a4ba04287b55e59d5357ac4","mode":"anon","command_path":["spaced","launch"],"exit_code":0,"duration_ms":142,"occurred_at":"2026-05-19T12:00:00.123456789Z","kit_version":"0.4.0","trace_id":"550e8400-e29b-41d4-a716-446655440000"}
```

Pretty-printed for human reading only (NOT the wire format):

```json
{
  "schema_version": "1",
  "sdk_lang": "go",
  "sdk_version": "0.4.0",
  "installation_id": "8c98a22cdc9a22a5233b2b241a5e28eb3daf4ffc6a4ba04287b55e59d5357ac4",
  "mode": "anon",
  "command_path": ["spaced", "launch"],
  "exit_code": 0,
  "duration_ms": 142,
  "occurred_at": "2026-05-19T12:00:00.123456789Z",
  "kit_version": "0.4.0",
  "trace_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

### 1b. Full mode (adds `args` and `flags`, both post-redact)

```json
{"schema_version":"1","sdk_lang":"go","sdk_version":"0.4.0","installation_id":"8c98a22cdc9a22a5233b2b241a5e28eb3daf4ffc6a4ba04287b55e59d5357ac4","mode":"full","command_path":["spaced","auth","login"],"exit_code":0,"duration_ms":287,"occurred_at":"2026-05-19T12:00:00.123456789Z","kit_version":"0.4.0","args":["--token","<redacted:token>"],"flags":{"--config":"<redacted:path>"}}
```

Pretty-printed:

```json
{
  "schema_version": "1",
  "sdk_lang": "go",
  "sdk_version": "0.4.0",
  "installation_id": "8c98a22cdc9a22a5233b2b241a5e28eb3daf4ffc6a4ba04287b55e59d5357ac4",
  "mode": "full",
  "command_path": ["spaced", "auth", "login"],
  "exit_code": 0,
  "duration_ms": 287,
  "occurred_at": "2026-05-19T12:00:00.123456789Z",
  "kit_version": "0.4.0",
  "args": ["--token", "<redacted:token>"],
  "flags": {"--config": "<redacted:path>"}
}
```

## 1c. SDK divergence: `event` + `attrs` vs `command_path`

The Go canonical and the polyglot SDKs (`py`, `ts`, `rs`, `php`) emit
two *different envelope shapes* under the same `schema_version = "1"`.
Collectors and downstream consumers MUST handle both.

**Go canonical.** The Go emitter is wired into the cobra command
lifecycle. It identifies the operation as a `command_path: []string`
(argv0 + subcommand chain, e.g. `["spaced","auth","login"]`). There is
no top-level `event` string and no top-level `attrs` map. Optional
post-redact `args` + `flags` ride along under Full mode.

**SDKs (py/ts/rs/php).** Adopters embedding an SDK invoke
`Client.record(event, attrs)`. They are *not* cobra commands — a
Python script calling `client.record("user_signup", {"tier": "premium"})`
has no argv chain. The SDKs therefore emit:

- `event: string` — caller-supplied opaque event name (e.g.
  `"user_signup"`). REQUIRED on the wire, ALWAYS present in SDK
  envelopes (the SDK constructor does not default it; callers always
  pass a name).
- `attrs: map<string, any>` — caller-supplied free-form properties,
  routed through the SDK redactor before publish.
- NO `command_path` field. SDK envelopes carry neither argv0 nor a
  subcommand chain.

Anon vs Full payload boundary: Anon-mode SDK envelopes MUST strip
`attrs` (mirroring how the Go emitter strips `args` + `flags` in
Anon). Anon-tier envelopes still carry `event` because the event name
is identity, not attribute payload; only the free-form `attrs` map is
tier-gated.

**Wire format MAY contain EITHER shape under v1.** Collectors and
diff tools MUST treat the following two envelopes as both-valid
producer outputs for `schema_version = "1"`:

- Go shape: includes `command_path` (and optionally `args` + `flags`
  in Full).
- SDK shape: includes `event` + `attrs` (and OMITS `command_path`).

A future `schema_version = "2"` MAY unify these (e.g.
`event: string | string[]` accepting either an opaque event name or a
command-path array). For now they coexist as two co-equal v1 producer
shapes, each owned by its respective runtime (Go vs SDKs).

### Example: SDK-emitted envelope (Full mode)

```json
{
  "schema_version": "1",
  "sdk_lang": "py",
  "sdk_version": "0.4.0",
  "installation_id": "8c98a22cdc9a22a5233b2b241a5e28eb3daf4ffc6a4ba04287b55e59d5357ac4",
  "mode": "full",
  "occurred_at": "2026-05-19T12:00:00.123456789Z",
  "event": "user_signup",
  "attrs": {
    "tier": "premium",
    "contact": "<redacted:email>"
  }
}
```

### Example: SDK-emitted envelope (Anon mode)

```json
{
  "schema_version": "1",
  "sdk_lang": "py",
  "sdk_version": "0.4.0",
  "installation_id": "8c98a22cdc9a22a5233b2b241a5e28eb3daf4ffc6a4ba04287b55e59d5357ac4",
  "mode": "anon",
  "occurred_at": "2026-05-19T12:00:00.123456789Z",
  "event": "user_signup"
}
```

Anon strips `attrs` entirely — same defensive posture as the Go
emitter dropping `args` + `flags` for Anon (§2 row 11–12). The
`event` identity field remains.

## 2. Field reference

All fields are listed in **declaration order** as they appear in the
Go `Event` struct. Declaration order IS the wire-emission order
(see §6 below). JSON keys are always `snake_case`; per-language
property/attribute names follow each SDK's idiomatic casing but the
serialiser MUST emit `snake_case` keys.

| # | JSON key | Go type | JSON type | Presence | Anon | Full | Notes |
|---|----------|---------|-----------|----------|------|------|-------|
| 1 | `schema_version` | `string` | string | required | yes | yes | Always `"1"` for this schema. STRING, not int. See §5. |
| 2 | `sdk_lang` | `string` | string | required | yes | yes | One of `go`, `py`, `ts`, `rs`, `php`. See §4. |
| 3 | `sdk_version` | `string` | string | optional (omitempty) | yes when set | yes when set | The SDK package version (e.g. `"0.4.0"`). Omitted only if unset by the emitter. |
| 4 | `installation_id` | `string` | string | required | yes | yes | 64-char lowercase hex SHA-256 digest of the 32 raw bytes on disk. Validator rejects uppercase or wrong length. |
| 5 | `mode` | `string` | string | required | yes | yes | Wire values: `"anon"` or `"full"`. `"off"` NEVER appears on the wire (emitter short-circuits). |
| 6 | `command_path` | `[]string` | array of strings | required, non-empty | yes | yes | argv0 plus subcommands (e.g. `["spaced","auth","login"]`). Always at least one element. |
| 7 | `exit_code` | `int` | number (integer) | required | yes | yes | Process exit code. `0` for success; non-zero for failure. |
| 8 | `duration_ms` | `int64` | number (integer) | required | yes | yes | Wall-clock command duration in milliseconds. Integer; never quoted. |
| 9 | `occurred_at` | `time.Time` | string | required | yes | yes | RFC 3339 with nanosecond precision and trailing `Z` (UTC). Zero value rejected by validator. |
| 10 | `kit_version` | `string` | string | optional (omitempty) | yes when set | yes when set | The kit Go binary version. Omitted if unset. |
| 11 | `args` | `[]string` | array of strings | optional (omitempty) | **NEVER** | yes, post-redact | The post-redact argv tail. Anon emitter defensively strips it pre-publish. |
| 12 | `flags` | `map[string]string` | object | optional (omitempty) | **NEVER** | yes, post-redact | Flag KEYS preserved verbatim; only VALUES routed through redact. |
| 13 | `trace_id` | `string` | string | optional (omitempty) | yes when set | yes when set | Cross-process correlation id. Free-form; UUID is the recommended shape. |
| 14 | `event` | (n/a — SDK only) | string | **SDK-only**, required when present | yes (identity) | yes | Caller-supplied opaque event name (e.g. `"user_signup"`). Emitted by py/ts/rs/php SDKs in lieu of `command_path`. Absent on Go-emitted envelopes. See §1c. |
| 15 | `attrs` | (n/a — SDK only) | object | **SDK-only**, optional | **NEVER** (stripped in Anon) | yes, post-redact | Caller-supplied free-form properties. Emitted by py/ts/rs/php SDKs in lieu of `args`/`flags`. Absent on Go-emitted envelopes. See §1c. |

### 2a. Per-language naming map

The wire keys are `snake_case` always. Each SDK exposes idiomatic
names internally and maps to/from the wire shape at the serialiser
boundary:

| Wire | Go field | Python attr | TS property | Rust field | PHP property |
|------|----------|-------------|-------------|------------|--------------|
| `schema_version` | `SchemaVersion` | `schema_version` | `schemaVersion` (with `@SerializedName`) | `schema_version` (with `#[serde(rename)]` only if struct field is `camelCase`) | `schemaVersion` (with mapping in serialiser) |
| `sdk_lang` | `SDKLang` | `sdk_lang` | `sdkLang` | `sdk_lang` | `sdkLang` |
| `sdk_version` | `SDKVersion` | `sdk_version` | `sdkVersion` | `sdk_version` | `sdkVersion` |
| `installation_id` | `InstallationID` | `installation_id` | `installationId` | `installation_id` | `installationId` |
| `mode` | `Mode` | `mode` | `mode` | `mode` | `mode` |
| `command_path` | `CommandPath` | `command_path` | `commandPath` | `command_path` | `commandPath` |
| `exit_code` | `ExitCode` | `exit_code` | `exitCode` | `exit_code` | `exitCode` |
| `duration_ms` | `DurationMS` | `duration_ms` | `durationMs` | `duration_ms` | `durationMs` |
| `occurred_at` | `OccurredAt` | `occurred_at` | `occurredAt` | `occurred_at` | `occurredAt` |
| `kit_version` | `KitVersion` | `kit_version` | `kitVersion` | `kit_version` | `kitVersion` |
| `args` | `Args` | `args` | `args` | `args` | `args` |
| `flags` | `Flags` | `flags` | `flags` | `flags` | `flags` |
| `trace_id` | `TraceID` | `trace_id` | `traceId` | `trace_id` | `traceId` |

Each SDK MUST round-trip a fixture JSON file from
`hops/main/sdk/tests/cross-lang/telemetry/fixtures/` and assert that
re-serialisation yields byte-identical output (see §6).

## 3. Reserved / extension fields

The Go `Event` struct does NOT include a free-form `attrs` map; SDKs
DO emit one (see §1c). `attrs` is therefore part of the v1 wire
contract on the SDK side only. Per the compat-window rule, an SDK
reading an event with `attrs` set under a future `schema_version > "1"`
MUST drop the field and emit the envelope unchanged when
one-major-behind.

Implementers MUST NOT add ad-hoc top-level fields. Any *new* canonical
field lands in `event.go` first, propagates here via the cross-language
contract test, and is documented in this section before any SDK ships
it. The SDK-only `event` + `attrs` pair (§1c) is the one carve-out
already pinned.

Reserved future `attrs` sub-keys (carved out now so SDK adopters don't
squat them):

- `attrs.trace_span_id` — OpenTelemetry-style span correlation.
- `attrs.user_segment` — adopter-supplied opaque segmentation token.
- `attrs.app_revision` — adopter build/commit revision distinct from
  `kit_version`.

These keys are reserved at the schema level. They are documented here
so SDK adopters recognise them as off-limits for caller-supplied
attribute keys until they ship as first-class envelope concerns.

## 4. `sdk_lang` enum

| Wire value | Runtime | Set by |
|------------|---------|--------|
| `go` | Go canonical (`hops/main/go/runtime/telemetry`) | `telemetry.SDKLang` const |
| `py` | Python SDK (`hops/main/sdk/py`) | per-SDK module constant |
| `ts` | TypeScript SDK (`hops/main/sdk/ts`) | per-SDK module constant |
| `rs` | Rust SDK (`hops/main/sdk/experimental/rs`) | per-SDK crate constant |
| `php` | PHP SDK (`hops/main/sdk/experimental/php`) | per-SDK package constant |

Any value not in this list is a producer bug. The cross-language
contract test asserts each SDK emits its exact lang code; adopters
diff this field to attribute events to a runtime.

## 5. `mode` enum

| Wire value | Meaning | Emits? |
|------------|---------|--------|
| `anon` | Identity + command + outcome only | yes |
| `full` | Adds `args` + `flags` post-redact | yes |
| ~~`off`~~ | Telemetry disabled | **NEVER on wire** — emitter short-circuits before publish |

The Go `Mode{Off, Anon, Full}` enum (`go/runtime/telemetry/mode.go`)
has three values. The WIRE has two. SDKs MUST treat receipt of a
`"mode": "off"` event as a producer bug and surface it on the dropped
counter.

## 6. `schema_version` policy

- **Wire type**: STRING (`"1"`). NOT integer, NOT float. Weakly-typed
  SDK languages round-trip int vs float vs string inconsistently, so
  the wire shape is fixed.
- **Current value**: `"1"`. Defined in
  `hops/main/go/runtime/telemetry/event.go` as the `SchemaVersion`
  const.
- **Bump policy**: bump major (`"2"`, `"3"`, ...) on breaking shape
  changes — removed field, renamed field, type change, semantics
  change. Additive optional fields with `omitempty` do NOT bump.
- **Compat window**: an SDK at compiled
  `schema_version = N` MUST refuse to emit when the persisted consent
  file's `schema_version` differs by more than one major. One-major-
  behind: emit with attrs dropped + log a debug warning. Otherwise:
  refuse + bump the dropped-event counter.
- **Release coordination**: any bump lands in `event.go` first, then
  ripples through SDK release coordination per task T-0710.

## 7. Canonical serialisation (byte-parity contract)

The cross-language contract test (T-0709) asserts byte-identical
output from every SDK on shared fixtures. To meet that bar:

- **Field order**: declaration order from the Go `Event` struct (see
  the order in §2). This is NOT alphabetical — it is the semantic
  order kit chose to make the canonical envelope read top-down:
  schema → emitter identity → installation identity → mode → command
  → outcome → time → version → tier-only fields → correlation. Per-
  language serialisers MUST preserve this order; do NOT alphabetise.
- **Whitespace**: compact JSON. NO trailing newline. NO indentation.
  NO spaces between tokens. The pretty-printed examples in §1 are for
  human reading ONLY; the wire shape is the single-line variant.
- **Number format**: integers as JSON numbers (e.g. `0`, `142`), never
  quoted strings. `exit_code` and `duration_ms` are integers.
- **Time format**: `occurred_at` is RFC 3339 with **nanosecond**
  precision and trailing `Z` (UTC). Example:
  `"2026-05-19T12:00:00.123456789Z"`. SDKs that lack native
  nanosecond timestamps render available precision (microsecond,
  millisecond) and pad with trailing zeros to nine digits; the
  contract test fixtures pin the exact string.
- **Map key order**: `flags` is a `map[string]string`. JSON object key
  order is NOT semantically meaningful per RFC 8259, but for byte-
  parity, SDKs MUST emit `flags` keys in lexicographic (Unicode
  codepoint) sort order. Go's `encoding/json` already does this for
  maps; py / ts / rs / php must explicitly sort.
- **Unicode**: emit raw UTF-8. Do NOT `\u`-escape ASCII; do NOT
  HTML-escape `<`, `>`, `&` (Go default is to escape — SDKs that diff
  byte-wise against Go output must match Go's default escape policy
  OR adjust the Go emitter encoder to disable HTML escape; the
  cross-language test fixtures pin the chosen variant).
- **Trailing whitespace**: none. No newline after the closing `}`.

## 8. Topic format (where this event ships)

Events publish on the kit bus under topic:

- **Canonical**: `kit.telemetry.event.recorded`
- **Adopter-prefixed**: `<app>.telemetry.event.recorded` (e.g.
  `cmdsurf.telemetry.event.recorded`)

Topic prefix is configured via:

- **Go**: `telemetry.WithTopicPrefix(prefix)` constructor option.
- **Python**: `Client(topic_prefix="cmdsurf")`.
- **TypeScript**: `new TelemetryClient({ topicPrefix: "cmdsurf" })`.
- **Rust**: `Client::builder().topic_prefix("cmdsurf").build()`.
- **PHP**: `new TelemetryClient(['topicPrefix' => 'cmdsurf'])`.

When no prefix is set, the canonical `kit.` prefix is used. The topic
grammar is `[Source].[Category].[Object].[Action]`, past-tense.

## 9. Audit topic

Redactor matches publish on:

- **Canonical**: `kit.telemetry.redact.matched`

This topic is subscribed by the `kit-telemetry-compliance` track
(task T-0702 redact-check) to verify that redact actually fired on
any Full payload before it reached a sink. Payload shape on the
audit topic is OUT OF SCOPE for this document — it is a different
contract.

SDK emitters do NOT publish on the audit topic directly. The redactor
observer wired into the Go-side emitter is the canonical source. SDK-
side redactors may emit their own audit signal under
`<sdk_lang>.telemetry.redact.matched` if the SDK wires a local
audit subscriber; this is documented in each per-SDK README.

## 10. Per-SDK example payloads

For now (T-0708 lands BEFORE per-SDK implementations), the Go example
is real. The other four are TEMPLATES that SDK authors fill in as
they implement T-0701..T-0707 and the cross-language contract test
(T-0709) green-lights byte-parity.

### 10a. Go (canonical)

See §1a / §1b. Emitter:
`hops/main/go/runtime/telemetry/emitter.go` (when T-0712 lands).

### 10b. Python SDK (template)

```json
{"schema_version":"1","sdk_lang":"py","sdk_version":"0.1.0","installation_id":"<64 hex>","mode":"anon","command_path":["spaced","launch"],"exit_code":0,"duration_ms":142,"occurred_at":"2026-05-19T12:00:00.123456Z","kit_version":"0.4.0"}
```

> Note Python's `datetime.isoformat(timespec="microseconds")` yields
> 6-digit precision; the contract-test fixture is the source of truth
> for whether to pad to 9.

### 10c. TypeScript SDK (template)

```json
{"schema_version":"1","sdk_lang":"ts","sdk_version":"0.1.0","installation_id":"<64 hex>","mode":"anon","command_path":["spaced","launch"],"exit_code":0,"duration_ms":142,"occurred_at":"2026-05-19T12:00:00.123Z","kit_version":"0.4.0"}
```

> Note JavaScript `Date.prototype.toISOString()` yields millisecond
> precision (3 digits). Pad to 9 per §7 fixture convention.

### 10d. Rust SDK (template)

```json
{"schema_version":"1","sdk_lang":"rs","sdk_version":"0.1.0","installation_id":"<64 hex>","mode":"anon","command_path":["spaced","launch"],"exit_code":0,"duration_ms":142,"occurred_at":"2026-05-19T12:00:00.123456789Z","kit_version":"0.4.0"}
```

> `chrono::DateTime<Utc>` with `.to_rfc3339_opts(SecondsFormat::Nanos, true)`
> matches Go's native rendering exactly.

### 10e. PHP SDK (template)

```json
{"schema_version":"1","sdk_lang":"php","sdk_version":"0.1.0","installation_id":"<64 hex>","mode":"anon","command_path":["spaced","launch"],"exit_code":0,"duration_ms":142,"occurred_at":"2026-05-19T12:00:00.123456Z","kit_version":"0.4.0"}
```

> PHP `DateTime::format(DateTimeInterface::RFC3339_EXTENDED)` yields
> 6-digit precision. Pad to 9 per §7 fixture convention.

## 11. Validation

Producers MUST validate before publish. The Go emitter calls
`Event.Validate()` immediately before bus publish; SDKs implement an
equivalent. The minimum-bar checks are:

- `schema_version == "1"` (else `ErrSchemaVersion`).
- `sdk_lang != ""` (else `ErrSDKLang`).
- `installation_id` is exactly 64 lowercase hex chars (else
  `ErrInstallID`).
- `mode` is `"anon"` or `"full"` (else `ErrMode`). Receipt of `"off"`
  is a producer bug.
- `command_path` is non-empty (else `ErrCommandPath`).
- `occurred_at` is non-zero (else `ErrOccurredAt`).

Schema validation enforces the WIRE contract. Tier validation
(`anon` MUST NOT carry `args`/`flags`) is enforced earlier in the
emitter pipeline, NOT inside the schema validator. The Go canonical
treats `Validate` as the schema gate; the emitter handles tier
semantics upstream.

## 12. References

- **Code (canonical)**:
  [`hops/main/go/runtime/telemetry/event.go`](../../go/runtime/telemetry/event.go)
- **Cross-language contract test**:
  `hops/main/sdk/tests/cross-lang/telemetry/`
