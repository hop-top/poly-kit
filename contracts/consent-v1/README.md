# consent-v1

## What it answers

What `kit/core/consent` grants must store, decide, refuse, audit and deliver, independent of language: the capability and subject grammar, the per-tool grants file, check/request/use/revoke semantics with expiry and sessions, refusal exit codes, the audit record and its `refused` events, confirmed delivery of the audit log onto the bus, and telemetry consent as the `telemetry` capability. Wrong place for OS permission prompts or capture code; those belong to the consumer.

## Use it when

- you port consent grants to an SDK: add a runner over these five files before writing the store
- you change grant semantics in Go: add a vector first, take its expectation from Go, then make every port pass

## Quick start

```sh
make test-parity-consent
```

## Contract

Go is canonical; a runner failure means the port drifted. Every file carries `version: "v1"` and a `description`. Runners are strict: unknown fields, another `version`, and a corpus file no runner reads all fail the run. Runners use tool `mytool`.

| File | Pins |
|------|------|
| `capabilities.json` | `capabilities[]`/`subjects[]` `{id or subject, ok, error_kind}`: `telemetry`, or two or more `^[a-z][a-z0-9_]*$` segments joined by `.`, <= 128 bytes; subject empty (any) or 1-256 bytes UTF-8, no whitespace or control characters |
| `records.json` | `cases[]` `{config_yaml?, grants_yaml, ok, grants or error_kind}`: the grants file decoded through the file store, legacy telemetry record included; any bad record makes the whole file `malformed_store`; a config typo never does |
| `scenarios.json` | `scenarios[]` of steps over one fresh session: decision semantics, refusals, `audit` trail with `refused` records, resulting `grants_file` / `config_file` |
| `migration.json` | same shape: telemetry read from the grants file, then the legacy records; migrate on write; revoke clearing all three; a corrupt grants file failing closed with config untouched |
| `delivery.json` | `scenarios[]` over one audit log, a relay and one sync subscriber: sequence numbers, ack cursor, redelivery, restart, crash before ack; `published` lists every publish attempt |

Files: grants `<XDG_STATE_HOME>/<tool>/consent/grants.yaml` (`version: 1`, `grants: [...]`), audit log `<XDG_STATE_HOME>/<tool>/consent/audit.jsonl`, ack cursor `<XDG_STATE_HOME>/<tool>/consent/audit.cursor` (`{"log", "seq"}`); directory 0700, files 0600, grants and cursor written atomically (temp + rename). Legacy telemetry record: `kit.telemetry.consent` in `<XDG_CONFIG_HOME>/kit/config.yaml`, then bare `telemetry.consent` in `kit/telemetry.yaml`.

Scenario fields: `now` (the clock), optional `env`, `prompt_version` (default 1), `config_yaml` / `legacy_yaml` / `grants_yaml` (file bodies; absent = no file), `steps`, `audit` (exact list, without `log`/`seq`), and `grants_file` / `config_file` / `legacy_file` (parsed as YAML, compared as JSON values; `null` = must not exist; absent = not checked).

Steps: `decide {grant}` (missing `decided_at` = `now`), `check`, `request` (`answer {decision, duration, expires_at?}` or `prompter: "none" | "error"`), `use`, `revoke {source}`, `list`, `advance {seconds}`, `end_session`, `reopen` (new session, same files). `expect`: `allowed`/`reason`, `ok`, `prompted`, `refused {reason, code, exit_code, transience}`, `removed`, `grants` or `error_kind` (never a refusal). Delivery steps: `record {record}` (`expect.seq`), `subscriber {fail_seq}` (0 = never fail), `flush` (`expect {delivered, cursor, pending, error?}`), `crash_after_publish` (the next cursor write fails), `restart`, `append {line}` (a raw log line, `$LOG` = the log id, as a newer writer appends it); `cursor_json` seeds the cursor file; a `published` entry's optional `payload` is compared as a JSON value.

Semantics a port can get wrong:

- Lookup: the subject's own grant, then the any-subject grant. One resolution rule for every record of one key (session and stored; for telemetry also both legacy records and the env): deny-wins, any deny is a deny whatever the timestamps, allow needs one allow and no deny, an unreadable source fails closed. An `until` grant is expired when `now >= expires_at` and counts as absent; reason `expired` when nothing else holds.
- `once` and `session` grants never reach disk; `once` allow is consumed by one `use`, `once` deny is audited and stored nowhere. Persisting a decision for a key drops its session grant.
- Fail closed: no grant and no prompter, a prompter error, an invalid answer, an unreadable store, or an audit failure on `use` all mean no. A standing deny is never prompted.
- Refusals: `denied` is `UNAUTHORIZED`, exit 5, `permanent`; `unset`, `expired`, `declined` are `CONSENT_REFUSED`, exit 7, `transient`.
- `refused` audit records mark a refusal that was not a decision, `reason` one of `no-prompter` (also `use` with no grant), `prompter-error` (error or invalid answer), `store-unreadable`, `standing-deny`, `expired`, `audit-failed`; `decision_source` is the governing grant's, `config` when none. Each (capability, subject, reason) is recorded once per session. `audit-failed` is published on the bus only, without a key. Recording or publishing a `refused` record never changes the outcome; its errors are ignored. A prompt answered deny is `denied`, not `refused`.
- Telemetry: allow/deny, always, any subject; other scopes are `unsupported_scope`. Reads combine the grants file, `kit.telemetry.consent` and `telemetry.yaml` deny-wins; writes go to the grants file and are mirrored into `kit.telemetry.consent` (and `telemetry.yaml` when it holds a record). Grants files are per tool, but telemetry is shared through the legacy record: a refusal recorded by any tool or SDK sticks everywhere. Checks run the telemetry resolver (`KIT_TELEMETRY_MODE=off`, `DO_NOT_TRACK` are a standing deny).
- Error kinds: `invalid_capability`, `invalid_subject`, `invalid_decision`, `invalid_duration`, `invalid_expiry`, `invalid_source`, `unsupported_scope`, `malformed_store`. Message text is not contract.
- Timestamps are RFC 3339 UTC with whole seconds.

Audit record (one JSON object per line): `v` (1), `log`, `seq`, `event`, `at`, `capability`, `subject`, then `decision`, `duration`, `expires_at` (until only), `decision_source`, `prompt_version`; `revoked` carries only `decision_source`; `refused` carries `decision_source` and `reason`.

Delivery (outbox): `seq` is 1, 2, 3, ... per `log`, an id minted when the log file is created. The relay publishes records after the cursor in order as bus events: topic `kit.core.consent.<event>`, source `core.consent`, id `<log>:<seq>` (the idempotency key), payload the record. A record is acked when Publish returns nil (every sync subscriber on the local bus returned nil); the cursor is then written. A failure stops the flush there. At-least-once: an unacked record is published again with the same key, also after a restart; acked records never are. A cursor naming another log is ignored; a corrupt cursor fails the flush. A record read back is published as read: an event or reason this version does not know (a newer writer's) is kept, under its own topic `kit.core.consent.<event>`, with every field; an event name whose topic fails the bus grammar (four `^[a-z][a-z0-9_]*$` segments, <= 128 bytes, past-tense action) makes the log unreadable, like bad JSON, `v` other than 1, an unparsable `at` or `expires_at`, or a broken log id or sequence.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/core/consent/conformance_test.go`, `conformance_delivery_test.go` (`TestConformanceConsent*`) |

## Neighbours

- `go/core/consent/`: Go implementation and reference behaviour
- `../exit-taxonomy-v1/`: the exit codes and transience classes refusals map onto

## See also

- [consent-grants.md](../../docs/adopters/reference/consent-grants.md): adopter reference
