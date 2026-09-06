# Compliance API Reference

> Static + runtime checker that validates CLI tools against the
> [12-factor AI CLI spec](../../../README.md), plus the opt-in 13th
> factor, Consenting Telemetry. Three-port API: Go, TS, Python. CLI
> exposed via `spaced compliance`.

## Who this is for

Tool authors and CI engineers who want their CLI to pass the
12-factor contract before release.

## Recommended path

Run the static check first — it needs only your `*.toolspec.yaml`:

```bash
spaced compliance --static --format json | jq -e '.score == .total'
```

If that passes, run the full check (static + runtime, requires the
built binary):

```bash
spaced compliance --format json | jq -e '.score == .total'
```

Compare against `.total`, not a literal — the denominator is 12 for a
tool that does not opt into telemetry and 13 for one that does. See
[Scoring](#scoring) below.

### From Go

```go
import "hop.top/kit/go/core/compliance"

report, err := compliance.Run(binaryPath, toolspecPath)
if err != nil {
    return err
}
fmt.Print(compliance.FormatReport(report, "text"))
```

### From TypeScript

```ts
import { run, formatReport } from "@hop-top/kit/compliance";

const report = run(binaryPath, toolspecPath);
console.log(formatReport(report, "text"));
```

### From Python

```python
from hop_top_kit.compliance import run, format_report

report = run(binary_path, toolspec_path)
print(format_report(report, "text"))
```

## Verify the result

`score` is the count of passing factors. `total` is the denominator.
All-green is `score == total`.

```bash
spaced compliance --format json | jq '{score, total}'
```

`status` lives on each entry of `results`, not on the report, and is
one of `pass`, `fail`, `skip`, `warn`. To see what failed:

```bash
spaced compliance --format json |
  jq -r '.results[] | select(.status == "fail") | "\(.factor) \(.name): \(.suggestion)"'
```

### Scoring

`total` is 12 unless your toolspec sets `telemetry.enabled: true`, in
which case F13 joins the denominator and `total` is 13:

```bash
# a toolspec with no telemetry block, or enabled: false
spaced compliance --static --format json | jq '.total'   # 12

# a toolspec with telemetry.enabled: true
spaced compliance --static --format json | jq '.total'   # 13
```

The denominator counts factors *eligible* to contribute, so factors that
skip for other reasons — the runtime-only checks on a `--static` run,
Auth Lifecycle on a tool with no `auth_commands` — still count toward it.
Only an F13 skip on a non-opt-in toolspec is excluded.

All three ports apply the same rule, so a toolspec scores identically
whichever one checks it.

---

## Troubleshooting: fix a failing factor

Each `CheckResult` includes a `suggestion` field with actionable
fix instructions. Common fixes by factor:

| # | Factor             | Fix                                          |
|---|--------------------|----------------------------------------------|
| 1 | Self-Describing    | Add `commands` array to toolspec             |
| 2 | Structured I/O     | Add `output_schema` to read commands         |
| 4 | Contracts          | Add `contract.idempotent` + `side_effects`   |
| 5 | Preview            | Add `preview_modes: [--dry-run]`             |
| 6 | Idempotency        | Declare `contract.idempotent: true/false`    |
| 7 | State Transparency | Add `state_introspection.config_commands`    |
| 8 | Safe Delegation    | Add `safety.requires_confirmation`           |
|11 | Evolution          | Set `schema_version` in toolspec root        |
|12 | Auth Lifecycle     | Add `state_introspection.auth_commands`      |
|13 | Consenting Telemetry | Complete the `telemetry` block — see below |

F13 only runs when `telemetry.enabled: true`. Its `details` field lists
every unsatisfied sub-condition at once, so one run tells you everything
left to fix:

| Sub-condition | Fix |
|---------------|-----|
| `categories is empty` | Add `telemetry.categories` |
| `consent_subcommands missing required entries` | Declare all of `status, enable, disable, reset, inspect` |
| `kill_switch_envs missing DO_NOT_TRACK` | Add `DO_NOT_TRACK` to `telemetry.kill_switch_envs` |
| `missing a <APP>_TELEMETRY_MODE entry` | Add e.g. `MYTOOL_TELEMETRY_MODE` |
| `prompt_version is empty` | Set `telemetry.prompt_version` (the field name is fixed — `consent_version` is not read) |
| `redact_rules is empty` | Set `telemetry.redact_rules` |
| `declared but not in commands tree` | Add the named command to `commands` |

For the full contract behind each one, see
[`telemetry-compliance.md`](telemetry-compliance.md).

If `--static` passes but full run fails, the failure is in the
binary, not the spec. Common runtime symptoms:

| Symptom                              | Likely cause                       |
|--------------------------------------|------------------------------------|
| F1 fails: `--help` exits non-zero    | `--help` returns error code        |
| F2 fails: `--format json` invalid    | non-JSON noise on stdout           |
| F3 fails: stderr has JSON            | mixed stream discipline            |
| F4 fails: `--bogus-arg` exits 0      | unknown flags accepted silently    |
| F10 fails: no `_meta` field          | structured output missing provenance |

---

## CI integration

```bash
# Fail CI if not fully compliant
spaced compliance --format json | jq -e '.score == .total'

# Or in Go tests
go test ./go/core/compliance/... -v
```

`.score == .total` keeps the gate correct the day you flip
`telemetry.enabled: true` and the denominator moves from 12 to 13.

## Reference

### Static checks (toolspec YAML)

| # | Factor             | What's checked                              |
|---|--------------------|---------------------------------------------|
| 1 | Self-Describing    | `commands` array non-empty, all named       |
| 2 | Structured I/O     | >= 1 command has `output_schema`            |
| 4 | Contracts & Errors | mutating commands have `contract` fields    |
| 5 | Preview            | mutating commands have `preview_modes`      |
| 6 | Idempotency        | `contract.idempotent` declared              |
| 7 | State Transparency | `state_introspection.config_commands` exists |
| 8 | Safe Delegation    | dangerous commands have `safety` block      |
|11 | Evolution          | `schema_version` is set                     |
|12 | Auth Lifecycle     | `auth_commands` in state_introspection      |
|13 | Consenting Telemetry | `telemetry` block well-formed (opt-in only) |

Factors 3 (Stream Discipline), 9 (Observable Ops), 10 (Provenance)
are skipped in static-only mode. Factor 13 is skipped unless the
toolspec sets `telemetry.enabled: true`.

F13 is skipped, and drops out of `total`, unless the toolspec sets
`telemetry.enabled: true`. Once opted in it checks seven conditions
in one row: non-empty `categories`; `consent_subcommands` covering
`status`, `enable`, `disable`, `reset` and `inspect`, each mapping
to a real command in the tree; `kill_switch_envs` holding
`DO_NOT_TRACK` plus one `<APP>_TELEMETRY_MODE` entry; non-empty
`prompt_version` (that exact field name — aliases are dropped at
parse and read as missing); and non-empty `redact_rules`.

### Runtime checks (binary execution)

| # | Factor             | What's checked                                  |
|---|--------------------|-------------------------------------------------|
| 1 | Self-Describing    | `--help` exits 0, contains COMMANDS/USAGE       |
| 2 | Structured I/O     | read command `--format json` returns valid JSON |
| 3 | Stream Discipline  | stdout has data, stderr has no JSON             |
| 4 | Contracts & Errors | `--bogus-arg` causes non-zero exit              |
| 5 | Preview            | mutating command `--dry-run` exits 0            |
| 7 | State Transparency | `config show` exits 0                           |
| 8 | Safe Delegation    | dangerous commands have safety metadata         |
|10 | Provenance         | JSON output has `_meta` field                   |
|11 | Evolution          | `--version` exits 0                             |
|12 | Auth Lifecycle     | `auth status` exits 0 (or skip if no auth)      |
|13 | Consenting Telemetry | kill switches honoured, consent prompt and `inspect` behave; skip unless opted in |

### API surface

All three ports expose identical APIs and check the same thirteen
factors, F13 included:

Go additionally returns an `error` alongside every result; TS and
Python throw instead.

| Go | TS | Python | Description |
|----|----|--------|-------------|
| `RunStatic(toolspecPath)` | `runStatic` | `run_static` | static checks only |
| `RunRuntime(binaryPath, toolspecPath)` | `runRuntime` | `run_runtime` | runtime checks only |
| `Run(binaryPath, toolspecPath)` | `run` | `run` | both; empty binary = static only |
| `FormatReport(report, format)` | `formatReport` | `format_report` | render as `"text"` or `"json"` |

`RunStatic` and `RunRuntime` return the check results alone
(`[]CheckResult`); only `Run` aggregates them into a `Report` with
`score` and `total`. `format` defaults to `"text"` in TS and Python
and is required in Go.

### CLI flags

| Flag        | Description                            |
|-------------|----------------------------------------|
| `--static`  | Static checks only                     |
| `--format`  | `text` (default) or `json`             |

## Related pages

- Top-level [README — 12-factor AI CLI spec](../../../README.md)
- [`cli-parity-guide.md`](../guides/cli-parity-guide.md) — required flags
- [`toolspec-api.md`](toolspec-api.md) — `*.toolspec.yaml` schema
