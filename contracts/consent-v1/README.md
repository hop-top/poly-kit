# consent-v1

## What it answers

What `kit/core/consent` grants must store, decide, refuse and audit, independent of language: the capability and subject grammar, the `kit.consent` record format, check/request/use/revoke semantics with expiry and sessions, refusal exit codes, the audit record, and telemetry consent as the `telemetry` capability. Wrong place for OS permission prompts or capture code; those belong to the consumer (a shell's sensitive I/O broker).

## Use it when

- you port consent grants to an SDK: add a runner over these four files before writing the store
- you change grant semantics in Go: add a vector first, take its expectation from Go, then make every port pass

## Quick start

```sh
make test-parity-consent
```

## Contract

Go is canonical; a runner failure means the port drifted. Every file carries `version: "v1"` and a `description`. Runners are strict: unknown fields, another `version`, and a corpus file no runner reads all fail the run.

| File | Pins |
|------|------|
| `capabilities.json` | `capabilities[]`/`subjects[]` `{id or subject, ok, error_kind}`: `telemetry`, or two or more `^[a-z][a-z0-9_]*$` segments joined by `.`, <= 128 bytes; subject empty (any) or 1-256 bytes UTF-8, no whitespace or control characters |
| `records.json` | `cases[]` `{config_yaml, ok, grants or error_kind}`: the `kit.consent` partition decoded through the file store, telemetry block included; any bad record makes the whole store `malformed_store` |
| `scenarios.json` | `scenarios[]` of steps over one fresh session: decision semantics, refusals, `audit` trail, resulting `config_file` |
| `migration.json` | same shape: telemetry consent read in place from `config.yaml` or the legacy `telemetry.yaml`, env precedence, revoke clearing both files |

Scenario fields: `now` (the clock; never the wall clock), optional `env`, `prompt_version` (default 1), `config_yaml` / `legacy_yaml` (file bodies, `null` or absent = no file), `steps`, `audit` (exact list of audit records), and `config_file` / `legacy_file` (the file parsed as YAML and compared as a JSON value; `null` = the file must not exist; absent = not checked).

Steps: `decide {grant}` (a missing `decided_at` is stamped with `now`), `check`, `request` (with `answer {decision, duration, expires_at?}`, or `prompter: "none" | "error"`), `use`, `revoke {source}`, `list`, `advance {seconds}`, `end_session` (drops once and session grants), `reopen` (a new session over the same files). `expect` holds `allowed`/`reason`, `ok`, `prompted`, `refused {reason, code, exit_code, transience}`, `removed`, `grants` or `error_kind`; an `error_kind` expectation must not be a refusal.

Semantics a port can get wrong:

- Lookup: the subject's own grant, then the any-subject grant; within a key the session grant beats the stored one. An `until` grant is expired when `now >= expires_at` and then counts as absent; the verdict reason is `expired` when nothing else holds.
- `once` and `session` grants never reach disk; `once` allow is consumed by one `use`, `once` deny is audited and stored nowhere. Persisting a decision for a key drops its session grant.
- Fail closed: no grant and no prompter, a prompter error, an invalid answer, an unreadable store, or an audit failure on `use` all mean no. A standing deny is never prompted.
- Refusals: `denied` is `UNAUTHORIZED`, exit 5, `permanent` (only the user changes it); `unset`, `expired`, `declined` are `CONSENT_REFUSED`, exit 7, `transient`.
- Telemetry: `granted`/`denied` at `kit.telemetry.consent` is an always, any-subject allow/deny; other scopes are `unsupported_scope`. Checks run the telemetry resolver, so `KIT_TELEMETRY_MODE=off` and `DO_NOT_TRACK` are a standing deny.
- Error kinds: `invalid_capability`, `invalid_subject`, `invalid_decision`, `invalid_duration`, `invalid_expiry`, `invalid_source`, `unsupported_scope`, `malformed_store`. Message text is not contract.
- Timestamps are RFC 3339 UTC with whole seconds, on disk and in audit records.

Audit record (one JSON object; JSON lines in the file auditor; payload of `kit.core.consent.{granted,denied,revoked,used}`): `v` (1), `event`, `at`, `capability`, `subject`, then `decision`, `duration`, `expires_at` (until only), `decision_source`, `prompt_version`. `revoked` carries only `decision_source` (who revoked) after `subject`.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/core/consent/conformance_test.go` (`TestConformanceConsent*`) |

## Neighbours

- `go/core/consent/`: Go implementation and reference behaviour
- `../exit-taxonomy-v1/`: the exit codes and transience classes refusals map onto

## See also

- [consent-grants.md](../../docs/adopters/reference/consent-grants.md): adopter reference
