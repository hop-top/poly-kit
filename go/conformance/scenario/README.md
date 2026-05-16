# scenario — conformance scenario DSL + grader

Library-side parser, validator, and grader for kit conformance
scenarios. The Go package lives at `hop.top/kit/go/conformance/scenario`;
the wire-format vocabulary (verb roster, top-level keys, compound
detection rules) lives in `contracts/scenario-rules.json` and is
shared with the verify-no-leak detector.

This README is the adopter authoring guide. The full design
contract is in `.tlc/tracks/12fcc-scen/design.md`.

## Package shape

```
go/conformance/scenario/
├── doc.go         package overview
├── types.go       Scenario, Step, Assertion, StoryRef, JudgeBlock
├── parser.go      ParseFile / ParseBytes (yaml.v3 KnownFields)
├── validator.go   Validate(*Scenario) → *ValidationErrors
├── grader.go      Grade(ctx, Input) → *Result
├── result.go      Result, Verdict, Status; Result.ToTier(n)
├── input.go       Input, Capture, Env
├── errors.go      AsCLIError shims for kit/output integration
├── version.go     SchemaVersion, GraderVersion, SupportedSchemaVersions
├── verbs/         closed-enum verb registry + per-verb evaluators
├── judge/         AIJudge interface + Canned stub
└── testdata/      fixture scenarios (allowlisted via leak default)
```

## Authoring a scenario

A scenario is a YAML file declaring:

1. an identifying `scenario_id`,
2. the `binary` it grades,
3. one or more `steps` (CLI invocations to capture), and
4. one or more `assertions` keyed by verb (`kind`).

Minimal example:

```yaml
schema_version: "1"
scenario_id: launch-happy-path
binary: spaced
factor_coverage: [1, 2]
tier: 3
story_ref:
  story_id: launch-mission
  story_path: stories/launch.yaml
  content_hash: "sha256:<64-hex>"
steps:
  - id: launch
    invoke: ["launch", "--payload", "alpha"]
assertions:
  - id: exits-ok
    kind: exit_code_equals
    on: launch
    factor: 1
    value: 0
  - id: stream-ok
    kind: stream_discipline_pass
    on: launch
    factor: 2
```

### Required top-level keys

| Key | Type | Notes |
|-----|------|-------|
| `schema_version` | string | currently `"1"` |
| `scenario_id` | string | kebab-case, `^[a-z][a-z0-9._-]*$` |
| `binary` | string | the CLI being graded |
| `factor_coverage` | `[]int` | 1..12, non-empty, unique |
| `tier` | int | 1, 2, or 3 |
| `story_ref` | object | `{story_id, story_path, content_hash}` |
| `steps` | `[]Step` | non-empty |
| `assertions` | `[]Assertion` | non-empty |

### Optional top-level keys

`description`, `engine_min_grader_version`, `judge`,
`preconditions`, `actors`, `grading`, `metadata`.

`preconditions` and `actors` are reserved for future schema versions
and ignored by the v1 grader.

## Verb roster (22 verbs, v1)

Closed enum. Adding a verb requires a `rules_version` bump in
`contracts/scenario-rules.json` plus an evaluator registration in
`verbs/`. The leak-rule consistency test
(`verbs/registry_test.go`) enforces parity between the JSON and
the Go registry.

### Exit-code verbs

- `exit_code_equals` — `{value: int}`
- `exit_code_in` — `{values: []int}`
- `exit_code_class` — `{classes: []string}` (kit class names)

### Output verbs

- `output_field_equals` — `{path: string, value: any, parse?: "json"|"yaml"}`
- `output_field_present` — `{path: string, parse?: "json"|"yaml"}`
- `output_field_count` — `{path: string, equals: int}`
- `output_schema_matches` — `{schema_ref?: string, schema?: object}`

Path syntax is JSONPath subset (gjson dotted notation; leading
`$.` accepted).

### Cassette verbs

- `cassette_must_contain` — `{op_class, adapter?, match?}`
- `cassette_must_not_contain` — same shape
- `cassette_diff_equals` — `{against: step-id, expect: "empty"}`
- `cassette_diff_empty` — paired apply/replay (use `_diff_equals` instead)

`op_class` ∈ `{any, mutating, reading, destructive}`.
`adapter` ∈ `{http, sql, redis, grpc, exec, fs}`.
`match` is a closed-key predicate: `query_substring`,
`url_substring`, `method`, `command`, `argv_substring`.

### Behavioural verbs

- `destructive_gate_required` — `{when?: {flag_absent: "--yes"}}`
- `dry_run_no_mutation` — pure check; cassette must be Read-only
- `idempotency_replay_clean` — paired apply/replay; second capture
  must be at `<on>__replay`
- `capability_roundtrip` — `{leaves?: []string}`

### Stream / stderr verbs

- `stderr_contains` — `{value: string, regex?: bool}`
- `stderr_does_not_contain` — same shape
- `stream_discipline_pass` — stdout JSON-parseable, stderr non-JSON

### Provenance verbs

- `provenance_present` — `{paths?: []string}`
- `provenance_matches_cassette` — cross-checks declared sources
  against URLs recorded in the cassette

### Judge verbs

- `judge_score_above` — `{judge_id: string, value: float 0..1}`
  Requires a matching `JudgeBlock` in the scenario.

### Deferred verbs

- `auth_lifecycle_clean` — parsed; grader emits
  `status: not_implemented` per Q2. Wired in a follow-up track once
  the auth-lifecycle harness lands.

## Judge blocks

```yaml
judge:
  - id: clarity-judge
    on: report          # which step's stdout feeds the prompt
    prompt: |           # required, unless prompt_ref
      score the report's clarity on a 0..1 scale
    model: claude-sonnet-4-7
    model_allowlist: [claude-sonnet-4-7, claude-opus-4-7]
```

The library ships only the `AIJudge` interface and a `Canned`
stub. The production registry (model invocation) lives in the
`12fcc-svc` service track. Callers wire their AIJudge into
`Input.Judge`; nil + any `judge_score_above` assertion ⇒
`VerdictUngradable` with `JUDGE_UNAVAILABLE`.

When `prompt_ref` is set instead of inline `prompt`, the grader
calls `Input.JudgePromptResolver(prompt_ref)` to materialise the
prompt body. The library never reads from disk.

## Tier system

The grader emits **Tier 3** internally. The caller redacts before
surfacing via `result.ToTier(n)`:

- **Tier 1**: verdict + identifying metadata.
- **Tier 2**: + per-factor `facets[]`.
- **Tier 3**: + per-assertion `assertions[]` + `judge_traces[]`.

Identifying fields (`scenario_id`, `schema_version`, `verdict`,
`scored_at`, `grader_version`, `rules_version`, `tier`) appear at
every tier.

## Story coupling

Every scenario carries a `story_ref.content_hash` (SHA-256 of the
referenced story file's bytes). The grader hashes
`Input.StoryContent` at grade time and refuses to grade on
mismatch (`STORY_HASH_MISMATCH`, exit 4).

This prevents scenarios from drifting past their underlying user
story without an explicit re-author + rehash by the scenario
author.

## Exit codes

The grader's symbolic codes all map to existing kit numeric exit
codes (no new numeric codes allocated; see design §14):

| Code | Exit | When |
|------|------|------|
| `SCENARIO_PARSE_ERROR` | 2 | malformed YAML / unknown key |
| `SCENARIO_VALIDATE_ERROR` | 2 | shape OK but semantically broken |
| `SCENARIO_SCHEMA_UNSUPPORTED` | 1 | binary doesn't know this schema_version |
| `GRADER_TOO_OLD` | 1 | scenario requires newer grader |
| `STORY_HASH_MISMATCH` | 4 | story bytes hash != declared |
| `JUDGE_UNAVAILABLE` | 5 | no AIJudge wired |
| `JUDGE_PROMPT_UNRESOLVED` | 5 | prompt_ref + nil resolver |
| `JUDGE_MODEL_REJECTED` | 5 | model not in allowlist |
| `JUDGE_PARSE_FAILED` | 5 | model returned bad output |
| `GRADER_INTERNAL` | 1 | grader bug |

## CLI

`kit conformance grade <scenario.yaml> <cassette-dir>` is a
dev-only debug stub for local authoring round-trips. Hidden;
production graders run inside the `12fcc-svc` service.

Cassette dir layout the leaf consumes:

```
<cassette-dir>/
└── steps/
    └── <step-id>/
        ├── stdout            # captured stdout (or stdout.txt / stdout.json)
        ├── stderr            # captured stderr
        ├── exit_code         # decimal integer
        └── cassette/         # xrr cassette files (optional)
```

Flags:
- `--story PATH` — story file (default: derived from `story_ref.story_path`)
- `--tier 1|2|3` — output tier (default: 3)
- `--format json|yaml` — wire format (default: json)
- `--judge-stub id=score` — repeatable canned judge scores
- `--no-judge` — disable AI judges entirely

## Adding a verb

1. Append to `contracts/scenario-rules.json` `verbs[]`.
2. Bump `rules_version` (calendar timestamp).
3. Run `make embed` (or re-`go test ./...` if embed is auto).
4. Create a new file under `verbs/` with one `Entry`:
   ```go
   func init() {
       register(&Entry{
           Kind:     KindMyNewVerb,
           Validate: validateMyNewVerb,
           Evaluate: evalMyNewVerb,
       })
   }
   ```
5. Add a constant `KindMyNewVerb` to `verbs.go`.
6. Run `go test ./go/conformance/scenario/...` — the registry
   consistency test guards against drift.

## See also

- `.tlc/tracks/12fcc-scen/design.md` — full design contract.
- `.tlc/tracks/12fcc-leak/design.md` — leak-rule consistency.
- `go/conformance/story/` — story DSL the scenario binds to.
