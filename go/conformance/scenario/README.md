# scenario

## What it answers

How a conformance scenario is parsed, validated and graded: the
library-side parser, validator and grader for the YAML files that
carry the grading rubric. The wire-format vocabulary (verb roster,
top-level keys, compound detection rules) lives in
`contracts/scenario-rules.json` and is shared with the verify-no-leak
detector. The plain-English intent a scenario grades against is the
sibling `story` package.

## Use it when

- read a scenario file → `ParseFile`, `ParseBytes` (yaml.v3 `KnownFields`)
- check one before grading → `Validate(*Scenario)`
- grade captured steps → `Grade(ctx, Input)`
- redact a result before surfacing it → `Result.ToTier(n)`
- supply an AI judge → `Input.Judge`, the `judge` sub-package's `AIJudge` interface and `Canned` stub
- resolve a `prompt_ref` → `Input.JudgePromptResolver`; the library never reads from disk
- round-trip a scenario locally → `kit conformance grade <scenario.yaml> <cassette-dir>`

## Quick start

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
```

## Contract

- Required top-level keys: `schema_version` (currently `"1"`), `scenario_id` (kebab-case, `^[a-z][a-z0-9._-]*$`), `binary`, `factor_coverage` (1..12, non-empty, unique), `tier` (1, 2 or 3), `story_ref`, `steps` and `assertions` (both non-empty). Optional: `description`, `engine_min_grader_version`, `judge`, `preconditions`, `actors`, `grading`, `metadata`; `preconditions` and `actors` are reserved and ignored by the v1 grader.
- The verb roster is a **closed enum** of 22 verbs. Adding one requires a `rules_version` bump in `contracts/scenario-rules.json` plus an evaluator registration under `verbs/`; `verbs/registry_test.go` enforces parity between the JSON and the Go registry.
- Output-verb path syntax is a JSONPath subset (gjson dotted notation; a leading `$.` is accepted).
- The grader emits Tier 3 internally; the caller redacts. Identifying fields (`scenario_id`, `schema_version`, `verdict`, `scored_at`, `grader_version`, `rules_version`, `tier`) appear at every tier.
- Every scenario carries a `story_ref.content_hash` (SHA-256 of the story file's bytes). The grader hashes `Input.StoryContent` at grade time and refuses to grade on mismatch (`STORY_HASH_MISMATCH`, exit 4), so a scenario cannot drift past its story without an explicit re-author and rehash.
- A nil `Input.Judge` plus any `judge_score_above` assertion yields `VerdictUngradable` with `JUDGE_UNAVAILABLE`.
- `auth_lifecycle_clean` parses but grades as `status: not_implemented` until the auth-lifecycle harness lands.
- Symbolic grader codes map onto existing kit numeric exit codes; no new numeric codes are allocated.

## Neighbours

- [`verbs/`](verbs/): the closed-enum verb registry and per-verb evaluators.
- [`judge/`](judge/): the `AIJudge` interface and the `Canned` stub. The production registry, which invokes models, lives in `go/conformance/svc`.
- `go/conformance/story`: the story DSL a scenario binds to by `story_id` and content hash.
- `go/conformance/harness/predicates`: the `testing.T`-free assertion kernel the grader reuses.
- [`contracts/scenario-rules.json`](../../../contracts/scenario-rules.json): the shared wire-format vocabulary.

## See also

- [Conformance reference](../../../docs/adopters/reference/conformance.md#scenario-dsl-and-grader): the package layout, the full 22-verb roster with payload shapes, judge blocks, the tier system, the grader exit-code table, the grade CLI and its cassette directory layout, and the steps for adding a verb
