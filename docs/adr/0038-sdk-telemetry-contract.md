# ADR-0038: SDK telemetry contract (delta-from-Go for py/ts/rs/php)

## Status

Proposed

## Date

2026-05-19

## Context

The kit project ships telemetry from four runtimes: a Go canonical
implementation (`kit-telemetry`) plus three (soon four) SDKs — Python,
TypeScript, Rust, and an experimental PHP — that adopters embed into
their own polyglot stacks. The Go canonical pins the event schema,
consent persistence format, install_id derivation, and env-precedence
ladder via ADR-0035 (telemetry canonical), ADR-0036 (consent
precedence), and ADR-0037 (compliance posture).

The SDKs do not re-decide any of these contracts. They consume them.
However, a handful of SDK-specific concerns have no analogue in the Go
implementation and must be pinned here so all four language clients
implement them consistently:

- SDKs are library code embedded in adopter applications. They never
  own a TTY and must not prompt for consent.
- SDKs share the install_id with the Go-side kit binary by reading the
  same on-disk file. The bytes-on-disk and hash-on-read posture is the
  cross-language interop boundary.
- The Go `go/core/redact` package is rich and stateful. The SDKs ship
  a lighter best-effort redactor with a custom-callback escape hatch
  for adopters with stricter needs.
- Caller-facing `record()` MUST NOT block. Each language uses its
  idiomatic background-drain primitive.
- Polyglot stacks may pin different SDK majors; the schema_version
  compat window pins what versions can read/write each other's events.

This ADR is the single source of SDK-side decisions. It is intentionally
delta-only: every contract that already exists on the Go side defers to
ADR-0035 (canonical schema), ADR-0036 (consent), and ADR-0037
(compliance). PHP-specific addenda land in this same ADR — there is no
separate ADR-0039 for sdk-telemetry-php; the PHP track appends to this
document.

## Decision

### 1. Read-only consent posture

SDKs NEVER write consent state. They never prompt. They never mutate
the persisted consent file. They consume the canonical YAML written by
the Go-side `kit telemetry status|enable|disable|reset` subcommands
(see ADR-0036).

Concretely, every SDK's consent reader:

- Reads `<XDG_CONFIG_HOME>/kit/telemetry.yaml`.
- Deserialises the canonical schema (state, decided_at, prompt_version,
  decision_source) per ADR-0036.
- Returns an in-memory value object with `allowed = (state == "granted")`.
- Never raises / never panics / never throws on missing or malformed
  input — surface a "denied" result and log at debug.

The persisted file does NOT carry a `mode:` field. Mode is env-only
(see §5). SDKs that find no consent file or an unreadable file surface
denied + default Mode::Off.

### 2. install_id sharing via canonical file path

All five runtimes (Go + 4 SDKs) read the same file:

```
<XDG_STATE_HOME>/kit/telemetry/installation_id
```

Format on disk: **32 raw bytes** (cryptographically random,
`secrets.token_bytes(32)` / `crypto.randomBytes(32)` / `getrandom`).
Permissions: `0o600` on the file, `0o700` on the parent directory.

Surface identifier: `sha256(bytes)` hex string.

If the file is absent on first read, the SDK generates the same
primitive and writes it atomically (write-temp-then-rename) so the
next Go boot inherits the SDK-written id without churn.

Two truly-simultaneous first-runs (Go process + SDK process both
booting and both finding an absent file) may briefly disagree on the
bytes; last-writer-wins on disk, and the next read on either side
re-aligns. A cross-language contract test pre-seeds the file with a
fixture byte sequence and asserts byte-identical hex output across
all SDKs.

### 3. Best-effort redactor delta vs `go/core/redact`

SDKs ship an opinionated regex-based redactor covering:

- Email (RFC-5322-ish, cheap regex).
- IPv4 dotted-quad + IPv6 colon-separated.
- `$HOME` prefix → `"$HOME"` literal.
- Token prefixes: `sk-`, `ghp_`/`gho_`/`ghu_`/`ghs_`/`ghr_`, `xoxb-`.

Replacements use deterministic `"<redacted:kind>"` placeholders so the
cross-language contract test can assert byte parity across py/ts/rs/php
on shared fixtures.

This is NOT full parity with `go/core/redact`. The Go redactor handles
structured-field policies, per-event-topic regex sets, and
configuration via the kit Go telemetry config. SDK adopters who need
that fidelity have two escape hatches:

1. **Custom redactor callback** — pass a `redactor=fn` (py/ts) /
   `Redactor` trait object (rs) / `RedactorInterface` (php) to the
   Client constructor. Custom redactor runs first; the default
   opinionated redactor runs after (defense in depth).
2. **Route events through a Go-side collector** — adopters with hard
   parity requirements stand up a Go-side bridge that consumes JSONL
   from the SDK sink and re-emits through `go/core/redact`. This is the
   recommended path for compliance-sensitive contexts; documented in
   each per-SDK README.

The opinionated regex set is reviewed quarterly. The redactor module
docstring in every SDK MUST flag this as best-effort + point at the
escape hatch.

### 4. Non-blocking emission contract

`record(event, attrs)` is fire-and-forget from the caller's POV:

- **Python**: `asyncio.Queue` (bounded, default 1024) + background
  drain task on `asyncio.get_event_loop()` if running, else a dedicated
  thread owning a private loop (so sync adopters work).
- **TypeScript**: in-memory ring (bounded, default 1024) + drain on
  `setImmediate` / `setInterval(5s)`.
- **Rust**: `tokio::sync::mpsc` bounded channel (default cap 1024) +
  `tokio::spawn` drain task. Adopters without a tokio runtime use the
  ship-along `tokio-current-thread` helper that owns a private runtime.
- **PHP**: short-lived process model — drain via shutdown handler +
  fire-once HTTP POST or JSONL append; PHP cannot keep a long-running
  drain across requests under php-fpm, so the contract is "flush before
  shutdown, drop if shutdown blocked".

Queue / channel full → increment a dropped-event counter (atomic),
return immediately. NEVER block. NEVER `.await`. NEVER `flush()` in
the caller path.

Queue size override: `KIT_TELEMETRY_QUEUE_SIZE` env var.

### 5. Env-precedence ladder for Mode resolution

```
1. DO_NOT_TRACK=1                         → Mode::Off (hard override)
2. KIT_TELEMETRY_CONSENT=granted|denied   → matches kit-consent state
3. <APP>_TELEMETRY_MODE=off|anon|full     → app-prefix wins when adopter
                                            wraps the SDK with a known
                                            app name
4. KIT_TELEMETRY_MODE=off|anon|full       → SDK-level env
5. persisted consent file                  → state: granted|denied
6. default Mode::Off
```

SDK adopters rarely have an app identity at runtime, so KIT_-only is
the common case. But the precedence MUST be:
`app-prefix > KIT > config > default`. This matches the Go-side
resolution (ADR-0036) and keeps polyglot stacks consistent.

Note the vocabulary: `KIT_TELEMETRY_CONSENT` takes `granted | denied`,
matching kit-consent's persisted `state:` field. Reject the earlier
`allow | deny` shorthand — this is a single canonical vocabulary
across the stack.

### 6. schema_version compat window

`schema_version` is a **string** (`"1"`), pinned by ADR-0035. SDKs
deserialise it as a string field, not an integer.

Compat window: an SDK at compiled `schema_version = N` MUST refuse to
emit if the persisted consent file's `schema_version` differs by more
than one major. Specifically:

- Same major (N == file.major): emit normally.
- One-major-behind (N == file.major - 1): emit with `attrs: {}`
  (drop attrs, keep envelope) and log a debug warning.
- Otherwise: refuse to emit, surface a clear error in the dropped-event
  counter.

This is the "polyglot version skew" mitigation: adopters who pin
incompatible SDK majors get explicit degradation rather than silent
divergence.

### 7. Event envelope additive fields

Top-level event JSON shape mirrors the Go canonical `Event` struct in
`hops/main/go/runtime/telemetry/event.go`. The on-wire field set,
JSON tags, field order, and `omitempty` placement are part of the
contract — the cross-language harness (T-0709) diffs them byte-wise.

As landed on the Go side:

```json
{
  "schema_version": "1",
  "sdk_lang": "py" | "ts" | "rs" | "php" | "go",
  "sdk_version": "0.4.2",
  "installation_id": "<sha256 hex, 64 chars lowercase>",
  "mode": "anon" | "full",
  "command_path": ["kit", "telemetry", "status"],
  "exit_code": 0,
  "duration_ms": 12,
  "occurred_at": "2026-05-19T12:00:00Z"
}
```

Notes:

- The install_id field is named `installation_id` (full word) on the
  wire, not `install_id`. SDK serialisers MUST emit the full word.
- Event routing is by the bus topic `kit.telemetry.event.recorded`
  (or `<app>.telemetry.event.recorded` when adopters set a topic
  prefix); see `hops/main/go/runtime/bus/` for the bus contract.
- `sdk_lang` and `sdk_version` are SDK-additive but already exist on
  the Go envelope (Go fills `sdk_lang = "go"`). SDKs override
  `sdk_lang` to their own value and fill `sdk_version` with the SDK
  package version.

#### Go-canonical vs SDK envelope-shape divergence

The Go canonical envelope identifies the operation by
`command_path: []string` because the in-tree consumers are all cobra
commands. The polyglot SDKs (py/ts/rs/php) instead emit
`event: string` + `attrs: map<string,any>` because adopter
applications are not necessarily CLIs — a Python script calling
`client.record("user_signup", {"tier": "premium"})` has no argv
chain. Both shapes are valid v1 producer envelopes; collectors
handle both.

`event` is ALWAYS present on SDK-emitted envelopes (caller-supplied,
no default). `attrs` is present in Full mode and stripped in Anon
mode (mirroring how the Go emitter strips `args` + `flags` in Anon).

This divergence is documented canonically — including SDK envelope
examples for both Anon and Full modes — in
[`hops/main/sdk/docs/telemetry-event-schema.md`](../../sdk/docs/telemetry-event-schema.md)
§1c. Future schemas (`schema_version >= "2"`) may unify the shapes;
for v1 they coexist as two co-equal producer envelopes.
- Mode `off` never emits (short-circuit at `record()` entry). The
  timestamp key is `occurred_at`, NOT `ts` — locked by ADR-0035.
- Future canonical envelope fields (e.g. trace_id, kit_version) land
  in the Go `Event` struct first and propagate to SDKs via the
  cross-language harness diff. SDK implementers track
  `hops/main/go/runtime/telemetry/event.go` as ground truth and the
  shared event-schema doc (T-0708) for the human-readable index.

#### Anon vs Full payload boundary

The Go emitter strips `Args` and `Flags`-equivalents from any event
where `mode == "anon"` defensively (post-redactor, pre-publish).
SDKs replicate the same boundary:

- `mode = "anon"`: SDKs MUST drop any caller-provided argv tail,
  flag map, or free-form attribute payload before the event reaches
  the sink, even if a custom redactor populated them. The envelope
  fields (`schema_version`, `sdk_lang`, `sdk_version`,
  `installation_id`, `mode`, `command_path`, `exit_code`,
  `duration_ms`, `occurred_at`) are the entire anon-tier payload.
- `mode = "full"`: post-redact argv tail and flag map are permitted.
  Flag KEYS are preserved verbatim; only VALUES route through redact.
- stdout / stderr are NEVER captured at any tier. That is an
  observability-platform concern, not a telemetry concern.

### 8. PHP-specific addenda (sdk-telemetry-php)

When the PHP track ships, it appends to THIS ADR rather than minting
its own ADR-0039. Expected PHP-specific decisions to capture here:

- Drain semantics under php-fpm vs cli-server vs swoole.
- Composer package boundary + autoload posture.
- The drop-on-shutdown contract (§4) and its observability.

PHP authors: add a "PHP-specific addenda" section below §8 and link it
back to this header.

## Consequences

**Positive**:

- One mental model across py/ts/rs/php SDKs. Adopters reading any
  per-SDK README see the same contract.
- install_id sharing means polyglot adopters get a single telemetry
  identity even when running Go + py + ts side-by-side.
- Non-blocking contract is a hard guarantee, not a best-effort claim.
- The compat-window protects polyglot stacks from silent version skew.
- All SDK divergences live in one ADR; readers always know where to
  look.

**Negative**:

- Best-effort redactor will miss some PII the Go redactor catches.
  Mitigated by the custom-callback escape hatch and the
  Go-side-collector route for compliance-sensitive contexts.
- The Rust SDK pulls tokio under the `telemetry` feature; non-tokio
  adopters get a non-trivial dep. Mitigated by the
  `tokio-current-thread` helper.
- PHP's short-lived process model means drop-on-shutdown is a real
  failure mode; the dropped counter must be surfaced in PHP-side
  observability docs.

**Operational**:

- Adopter expectation: pin SDK + kit Go versions per release channel.
  Mixed-major polyglot stacks are explicitly unsupported beyond the
  major-N-minus-1 compat window.
- The cross-language contract test
  (`hops/main/sdk/tests/cross-lang/telemetry/`) is the regression gate.
  Any SDK change that affects envelope shape, install_id derivation, or
  redactor placeholders MUST update the harness in the same PR.

## Alternatives

1. **Per-SDK ADR** — separate ADR per language (0038 py, 0039 ts, 0040
   rs, 0041 php). Rejected: the divergences are SDK-shape concerns, not
   language concerns. Splitting fragments the canonical-vs-delta
   boundary.
2. **Re-pinning Go decisions in this ADR** — restate schema_version
   type, env vocabulary, install_id path here for reader convenience.
   Rejected: that's how contracts drift. ADR-0035 is canonical; this
   ADR defers. Adopters get one source of truth.
3. **In-SDK consent prompting** — let SDKs prompt when running in an
   interactive context. Rejected: SDKs are library code. Interactive
   prompting is a CLI concern owned by kit-consent. The read-only
   posture also makes the SDK contract testable without TTY emulation.
4. **Strict redactor parity with `go/core/redact`** — port the full Go
   redactor to each SDK. Rejected: maintenance cost grows linearly with
   SDK count, the Go redactor is config-driven (configs would need
   parity too), and the custom-callback escape hatch covers the
   adopter-with-strict-needs case at a fraction of the cost.

## References

- ADR-0035: kit-telemetry canonical event schema + install_id
  derivation
- ADR-0036: kit-consent persisted file format + env-precedence ladder
- ADR-0037: kit-telemetry compliance posture
- sdk-telemetry track plan:
  `.tlc/tracks/sdk-telemetry/plan.md`
- sdk-telemetry-php track plan (when minted):
  `.tlc/tracks/sdk-telemetry-php/plan.md`
- Cross-language contract test:
  `hops/main/sdk/tests/cross-lang/telemetry/` (proposed location)
- Shared event-schema doc:
  `hops/main/sdk/docs/telemetry-event-schema.md` (proposed location)

### Canonical Go-side packages (ground truth for SDK implementers)

SDK authors read these as the source of truth. Any divergence from
the Go behaviour is a bug in the SDK unless explicitly enumerated in
this ADR as a documented delta.

- `hops/main/go/runtime/telemetry/` — canonical `Event` struct,
  `Mode` resolver, `InstallationID()` helper, `Emitter`, `Redactor`,
  `sink_https` transport. The package-level doc and per-file
  package comments enumerate the on-wire contract.
- `hops/main/go/core/consent/` — `State` vocabulary, `Decision`
  struct, `FileStore` persistence format, `NewHook` adapter that
  satisfies the `ConsentHook` interface owned by kit-telemetry.
- `hops/main/go/runtime/bus/` — topic family
  `kit.telemetry.event.recorded` (and the `<app>.telemetry.event.recorded`
  prefix variant). SDKs that wire into a Go-side bridge route
  through this topic.
