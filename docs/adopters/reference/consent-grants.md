# Consent Grants Reference

> Per-capability consent for kit-based tools (`hop.top/kit/go/core/consent`).
> Grant model, lookup and expiry, the prompter seam, refusals and exit
> codes, audit records, confirmed delivery to the bus, on-disk layout,
> telemetry as a capability.

## Who this is for

Go authors whose tool reads something sensitive on the user's behalf:
the clipboard, the screen, a microphone, files fed into an LLM prompt.
Grants answer *may this subject use this capability now?* and leave a
record of every answer. They do not ask the OS for permission and do not
capture anything: the tool (or a shell's sensitive I/O broker) owns the
OS prompt, the capture and what happens to the data.

The cross-language contract is
[`contracts/consent-v1`](../../../contracts/consent-v1/README.md); every
SDK port runs it.

Rust: `hop_top_kit::consent` (feature `consent`, wasm-safe core;
`consent-file` for the files and the relay) follows this page; see
[its README](../../../sdk/rs/src/consent/README.md).

## Grant model

| Field | Values | Notes |
|-------|--------|-------|
| `Capability` | `telemetry`, or two or more `^[a-z][a-z0-9_]*$` segments joined by `.` (<= 128 bytes) | Name the resource and the act: `io.clipboard.read`, `io.screen.capture`, `io.camera.capture`, `io.microphone.capture`, `io.files.read` |
| `Subject` | empty (any subject), or 1-256 bytes, no whitespace or control characters | Who uses it: `app:<id>`, `tool:<name>`, `principal:<id>` by convention |
| `Decision` | `allow`, `deny` | |
| `Duration` | `once`, `session`, `until`, `always` | `until` needs `ExpiresAt` after `DecidedAt` |
| `DecidedAt` | time | Stamped by the clock when zero |
| `DecisionSource` | `prompt`, `flag`, `env`, `config` | Same taxonomy as telemetry consent |
| `PromptVersion` | int >= 0 | Which wording the user saw |

`once` and `session` grants live in the `Grants` value (one value is one
session); `until` and `always` go to the `GrantStore`.

## Lookup

For `(capability, subject)`: the subject's own grant first, then the
any-subject grant. Within one key every record held (in memory and
stored) is combined deny-wins: any deny is a deny. Storing a decision
for a key drops its in-memory grant. An
`until` grant is expired once `now >= ExpiresAt` and counts as absent.

`Verdict.Grant` is the governing grant; for `expired`, the grant that
expired.

| `Verdict.Reason` | Meaning |
|------------------|---------|
| `allowed` | an allow grant holds |
| `denied` | a deny grant holds (telemetry: also an env opt-out) |
| `unset` | nothing holds |
| `expired` | the governing grant expired and nothing else holds |

## API

```go
g, err := consent.OpenGrants(opts...)            // per-tool FileStore + FileAuditor
g := consent.NewGrants(store, opts...)           // any GrantStore; no audit unless WithAuditor

g.Check(ctx, cap, subject) (Verdict, error)      // no prompt, no audit
g.Request(ctx, cap, subject, prompter) (Verdict, error)
g.Use(ctx, cap, subject) (Grant, error)          // gate before every tap
g.Decide(ctx, grant) error                       // record from your own UI
g.Revoke(ctx, cap, subject, source) (bool, error)
g.List(ctx) ([]Grant, error)
g.EndSession()
```

Options: `WithTool` (OpenGrants; default `DefaultTool()`: the
registered telemetry app prefix, lowercased, else `kit`),
`WithDelivery(bus)` (OpenGrants builds the Relay), `WithRelay`,
`WithAuditor`, `WithClock`, `WithEnv`, `WithAppPrefix`,
`WithPromptVersion` (default 1). `g.Relay()` returns the relay.

- `Request` returns an allow verdict when one holds. Otherwise: a
  standing deny is refused without prompting; with no prompter it is
  refused; else the `Prompter` is asked and its `Answer` is recorded with
  `DecisionSource` `prompt`. A prompter error or an invalid answer is a
  refusal; a deny answer is refused as `declined`.
- `Use` re-checks (a revoke between `Request` and `Use` wins), records a
  `used` audit event, then consumes a `once` grant. It never prompts. If
  the auditor fails, the use is refused with that error and a `once`
  grant is kept.
- `Revoke` removes the exact key in memory and in the store; it reports
  whether anything was removed and audits only then.

### Prompter seam

```go
type Prompter interface {
    Ask(ctx context.Context, p Prompt) (Answer, error)
}
```

`Prompt` carries the capability, subject, prompt version and why there
is no decision (`unset` or `expired`). Return an error when you cannot
ask (no TTY, headless, dismissed). `Request` releases its lock while the
prompter runs, so the prompter may call `Check`.

## Refusals and exit codes

Every refusal is a `*RefusedError` (`errors.Is(err, consent.ErrRefused)`)
with `Capability`, `Subject` and `Reason`. It implements `AsCLIError`, so
a cobra `RunE` can return it as is.

| Reason | Code | Exit | Transience | Cleared by |
|--------|------|------|------------|------------|
| `denied` | `UNAUTHORIZED` | 5 | permanent | only the user changing or revoking the grant; `--yes`, `--confirm=yes` and retries cannot |
| `unset`, `expired`, `declined` | `CONSENT_REFUSED` | 7 | transient | asking again where a prompt can run |

Store and validation failures are not refusals: they come back as
`*GrantError` (`consent.KindOf(err)`: `invalid_capability`,
`invalid_subject`, `invalid_decision`, `invalid_duration`,
`invalid_expiry`, `invalid_source`, `unsupported_scope`,
`malformed_store`) and must stop the caller just the same. A malformed
store is never rewritten by a decide.

## Audit

`AuditEvent` is one record: `v`, `log`, `seq`, `event` (`granted`,
`denied`, `revoked`, `used`, `refused`), `at`, `capability`, `subject`,
then `decision`, `duration`, `expires_at` (until only),
`decision_source`, `prompt_version`. `revoked` carries only
`decision_source`, naming who revoked. `refused` carries
`decision_source` (of the governing grant, `config` when none) and
`reason`.

`denied` is a decision of no. `refused` is a refusal that was not one:

| `reason` | When |
|----------|------|
| `no-prompter` | `Request` without a prompter, or `Use` with no grant |
| `prompter-error` | the prompter failed or gave an invalid answer |
| `store-unreadable` | the grants file (or the legacy telemetry record) cannot be read |
| `standing-deny` | a deny grant (or `DO_NOT_TRACK`, `KIT_TELEMETRY_MODE=off`) holds |
| `expired` | the governing grant expired and nothing can ask |
| `audit-failed` | the `used` record could not be written; published on the bus only, without a key |

Each (capability, subject, reason) is recorded once per session
(`EndSession` resets it), so a hot `Use` loop on a standing deny writes
one record. Writing or publishing a `refused` record is best effort: its
errors, a bus veto included, are ignored and never change the outcome.

| Auditor | Writes |
|---------|--------|
| `NewFileAuditor()`, `NewFileAuditorFor(tool)` | JSON lines, `<XDG_STATE_HOME>/<tool>/consent/audit.jsonl`, 0600 in a 0700 dir; stamps `log` and `seq` |
| `NewFileAuditorAt(path)` | same, at `path` |
| `BusAuditor(pub, DefaultTopics)` | publishes inline; the publisher's error refuses the use; nothing kept for redelivery |
| `MultiAuditor(a, b, ...)` | each in order, stops at the first error |

## Confirmed delivery

The audit log is the outbox. `NewRelay(fileAuditor, bus)` (or
`WithDelivery`) publishes every record after the ack cursor, in `seq`
order:

| Field | Value |
|-------|-------|
| topic | `kit.core.consent.granted`, `.denied`, `.revoked`, `.used`, `.refused` |
| source | `core.consent` |
| id | `<log>:<seq>`, the idempotency key; dedupe on it |
| subject | the capability |
| payload | the `AuditEvent` |

A record is acked when `Publish` returns nil: on an in-process bus,
every sync subscriber (`Subscribe`) returned nil. The cursor
(`audit.cursor`, `{"log","seq"}`, written atomically, 0600) then moves
past it. A subscriber error stops the flush at that record; the next
flush, `Run` retry or restart sends it again with the same id, so
delivery is at-least-once and in order, and a subscriber that always
errors stalls the ones behind it (`FlushResult.Pending`).

```go
g, _ := consent.OpenGrants(consent.WithTool("mytool"), consent.WithDelivery(b))
go g.Relay().Run(ctx) // flushes at start, on every new record, and every 5s after a failure
```

Records are published as read from the log. An event or reason this
version does not know (written by a newer kit) is kept and published
under its own topic, `kit.core.consent.<event>`, with every field it
carries. An event name whose topic fails the bus grammar (lowercase
segments, past-tense action, at most 128 bytes) makes the log
unreadable: no publish, and the next `Use` is refused.

Not covered by the ack: `SubscribeAsync` handlers, and peers behind a
network adapter (the bus stream has no publisher-visible ack). Delivery
runs after the decision and never blocks or changes it; `Use` does not
wait for acks.

## On-disk layout

Per tool, under `<XDG_STATE_HOME>/<tool>/consent/` (0700): `grants.yaml`
(written atomically, 0600), `audit.jsonl`, `audit.cursor`. config.yaml
is never read for grants, so a typo there cannot refuse a capability,
and a grant write cannot clobber it.

```yaml
version: 1
grants:
  - capability: io.screen.capture
    subject: app:dek
    decision: allow
    duration: until
    decided_at: "2026-03-01T00:00:00Z"
    expires_at: "2026-03-01T01:00:00Z"
    decision_source: prompt
    prompt_version: 1
  - capability: telemetry
    subject: ""
    decision: allow
    duration: always
    decided_at: "2026-03-01T00:00:00Z"
    decision_source: prompt
    prompt_version: 1
```

Decoding is strict: an unknown key, a missing field, `version` other
than 1, a `once`/`session` record, a duplicate key or a scoped
`telemetry` grant makes the whole file `malformed_store`, so every
check fails closed, and no write repairs it until the file is fixed.

## Telemetry as a capability

`consent.CapTelemetry` (`telemetry`) is an allow or deny, always and
any-subject (other scopes are `unsupported_scope`).

- Read: every source, combined deny-wins: the grants file, the legacy
  `kit.telemetry.consent` record in `<XDG_CONFIG_HOME>/kit/config.yaml`,
  and `kit/telemetry.yaml`. Any deny is the answer, whatever the
  timestamps (a newer allow in one source does not beat a deny in
  another); allow needs at least one allow and no deny. A source that
  cannot be read fails the check closed.
- Write (`Decide`, `FileStore.Set`, so `kit telemetry enable|disable`):
  the grants file, then a mirror into `kit.telemetry.consent` and into
  `kit/telemetry.yaml` when it holds a record, so a re-grant through the
  kit converges every source. A config.yaml that does not parse is left
  alone.
- Revoke (`Revoke`, `FileStore.Clear`, so `kit telemetry reset`): the
  grants file entry and both legacy records, other keys kept.
- Scope: grants files are per tool, so revoking in tool B never clears
  tool A's grants. Telemetry, though, is effectively shared through the
  legacy record every tool and SDK reads and writes: a "no" recorded by
  any of them (or by the user in config.yaml) denies telemetry in every
  tool, and deny-wins is what makes that refusal stick over an older
  "yes" in a tool's own grants file.
- Check runs the telemetry resolver over it, so `KIT_TELEMETRY_MODE=off`
  and `DO_NOT_TRACK` are a standing deny and `KIT_TELEMETRY_CONSENT`
  still overrides. `Install`, `NewHook` and `Resolve` are unchanged and
  read the same answer.

## See also

- [`go/core/consent`](../../../go/core/consent/README.md)
- [`telemetry.md`](telemetry.md), [`telemetry-compliance.md`](telemetry-compliance.md)
- [`output.md`](output.md): exit codes and transience
- [`bus-api.md`](bus-api.md): topics and publishers
