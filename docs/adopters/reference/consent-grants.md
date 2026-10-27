# Consent Grants Reference

> Per-capability consent for kit-based tools (`hop.top/kit/go/core/consent`).
> Grant model, lookup and expiry, the prompter seam, refusals and exit
> codes, audit records and bus topics, on-disk format, telemetry as a
> capability.

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
any-subject grant. Within one key the in-memory grant beats the stored
one, and storing a decision for a key drops its in-memory grant. An
`until` grant is expired once `now >= ExpiresAt` and counts as absent.

| `Verdict.Reason` | Meaning |
|------------------|---------|
| `allowed` | an allow grant holds |
| `denied` | a deny grant holds (telemetry: also an env opt-out) |
| `unset` | nothing holds |
| `expired` | the governing grant expired and nothing else holds |

## API

```go
g, err := consent.OpenGrants(opts...)            // FileStore + FileAuditor
g := consent.NewGrants(store, opts...)           // any GrantStore; no audit unless WithAuditor

g.Check(ctx, cap, subject) (Verdict, error)      // no prompt, no audit
g.Request(ctx, cap, subject, prompter) (Verdict, error)
g.Use(ctx, cap, subject) (Grant, error)          // gate before every tap
g.Decide(ctx, grant) error                       // record from your own UI
g.Revoke(ctx, cap, subject, source) (bool, error)
g.List(ctx) ([]Grant, error)
g.EndSession()
```

Options: `WithAuditor`, `WithClock`, `WithEnv`, `WithAppPrefix`,
`WithPromptVersion` (default 1).

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

`AuditEvent` is one record: `v`, `event` (`granted`, `denied`,
`revoked`, `used`), `at`, `capability`, `subject`, then `decision`,
`duration`, `expires_at` (until only), `decision_source`,
`prompt_version`. `revoked` carries only `decision_source`, naming who
revoked.

| Auditor | Writes |
|---------|--------|
| `NewFileAuditor()` | JSON lines, `<XDG_STATE_HOME>/kit/consent/audit.jsonl`, perms 0600 |
| `NewFileAuditorAt(path)` | same, at `path` |
| `BusAuditor(pub, DefaultTopics)` | `kit.core.consent.granted`, `.denied`, `.revoked`, `.used`, source `core.consent`, payload `AuditEvent`; a publisher veto refuses the use |
| `MultiAuditor(a, b, ...)` | each in order, stops at the first error |

## On-disk format

Grants live in the kit AppConfig, `<XDG_CONFIG_HOME>/kit/config.yaml`,
next to the telemetry record; every other key is preserved and writes
are atomic (`*.tmp` + rename, 0600).

```yaml
kit:
  consent:
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
  telemetry:
    consent:
      state: granted
      decided_at: "2025-01-01T00:00:00Z"
      prompt_version: 1
      decision_source: prompt
```

Decoding is strict: an unknown key, a missing field, `version` other
than 1, a `once`/`session` record, a duplicate key or `telemetry` inside
the list makes the whole store `malformed_store`, so every check fails
closed until the file is fixed.

## Telemetry as a capability

`consent.CapTelemetry` (`telemetry`) maps onto the existing
`kit.telemetry.consent` record: `granted` is an allow, `denied` a deny,
always and any-subject (other scopes are `unsupported_scope`). Nothing is
rewritten on upgrade, so `Install`, `NewHook`, `Resolve`, `kit telemetry`
and the other SDKs keep reading the same record. `Check` runs the
telemetry resolver over it, so `KIT_TELEMETRY_MODE=off` and
`DO_NOT_TRACK` are a standing deny and `KIT_TELEMETRY_CONSENT` still
overrides.

Revoking telemetry (and `FileStore.Clear`, so `kit telemetry reset`)
removes the record from `config.yaml` and from the legacy
`kit/telemetry.yaml`, keeping other keys, so the reset is not undone by
the legacy fallback.

## See also

- [`go/core/consent`](../../../go/core/consent/README.md)
- [`telemetry.md`](telemetry.md), [`telemetry-compliance.md`](telemetry-compliance.md)
- [`output.md`](output.md): exit codes and transience
- [`bus-api.md`](bus-api.md): topics and publishers
