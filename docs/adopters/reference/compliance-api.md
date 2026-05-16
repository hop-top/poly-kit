# Compliance API Reference

> Static + runtime checker that validates CLI tools against the
> [12-factor AI CLI spec](../../../README.md). Three-port API: Go, TS,
> Python. CLI exposed via `spaced compliance`.

## Who this is for

Tool authors and CI engineers who want their CLI to pass the
12-factor contract before release.

## Recommended path

Run the static check first — it needs only your `*.toolspec.yaml`:

```bash
spaced compliance --static --format json | jq -e '.score == 12'
```

If that passes, run the full check (static + runtime, requires the
built binary):

```bash
spaced compliance --format json | jq -e '.score == 12'
```

### From Go

```go
import "hop.top/kit/go/core/compliance"

report, err := compliance.Run(binaryPath, toolspecPath)
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

`score` is the count of passing factors (0–12). All-green is `12`.

```bash
spaced compliance --format json | jq '{score, status: .status}'
```

Status values: `pass`, `fail`, `skip`, `warn`.

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
spaced compliance --format json | jq -e '.score == 12'

# Or in Go tests
go test ./compliance/... -v
```

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

Factors 3 (Stream Discipline), 9 (Observable Ops), 10 (Provenance)
are skipped in static-only mode.

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

### API surface

All three ports expose identical APIs:

| Function                       | Description                          |
|--------------------------------|--------------------------------------|
| `RunStatic(toolspecPath)`      | static checks only                   |
| `RunRuntime(binaryPath, toolspecPath)` | runtime checks only         |
| `Run(binaryPath, toolspecPath)`| both; empty binary = static only     |
| `FormatReport(report, format)` | render as `"text"` or `"json"`       |

### CLI flags

| Flag        | Description                            |
|-------------|----------------------------------------|
| `--static`  | Static checks only                     |
| `--format`  | `text` (default) or `json`             |

## Related pages

- Top-level [README — 12-factor AI CLI spec](../../../README.md)
- [`cli-parity-guide.md`](../guides/cli-parity-guide.md) — required flags
- [`toolspec-api.md`](toolspec-api.md) — `*.toolspec.yaml` schema
