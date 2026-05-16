# go/conformance/story — kit story DSL

`go/conformance/story/` is the Go API for kit's story DSL: the
closed-key YAML shape, parser, three-tier validator, and helpers
that scenario tooling consumes. The user-facing CLI wrapper is
`kit conformance verify-stories`; the rationale +
scenario-coupling contract lives in ADR-0026.

## What stories are

A story describes **what a user is trying to do**: plain-English
intent plus a command sequence. Stories ship in the adopter's public
repo (`e2e/stories/*.yaml`); scenarios — which carry the grading
rubric — live in a separate private repo and reference stories by
`story_id`.

Stories are deliberately structurally distinct from scenarios. They
do not carry assertions, judges, or cassette guards. The closed-key
YAML schema and the metadata-key denylist enforce this
structurally — `verify-no-leak` will never fire on a valid story.

## Package layout

| Path | Role |
|------|------|
| `schema/` | Go types + YAML tags for `Story`, `Step`, `Reference`. The closed-key set. |
| `parser/` | `ParseFile` / `ParseBytes` — decode YAML with `KnownFields(true)`. |
| `validator/` | Three-tier validator (`ValidateOne`, `ValidateAll`). |
| `toolspec/` | Minimal toolspec projection used by tier 3. |
| `story.go` (this package) | `Discover`, `Index`, `ContentSHA256`, `ReadStory`. |

## Validator tiers

| Tier | Scope | Default |
|------|-------|---------|
| 1 | Schema validity (closed-key, regex, length, enum). | always on |
| 1.5 | Metadata-key denylist (sourced from `contracts/scenario-rules.json`). | always on |
| 2 | Referential validity (uniqueness, invoke vs binary, URL parsing). | on |
| 3 | Toolspec semantic validity (every invoked command + flag must be declared). | opt-in (`--strict-toolspec`) |

A fourth tier — runtime execution — is explicitly out of scope.
Stories are validated, never executed.

## Embedding the validator

```go
import (
    "hop.top/kit/go/conformance/scenariorules"
    "hop.top/kit/go/conformance/story/parser"
    "hop.top/kit/go/conformance/story/validator"
)

doc, _ := scenariorules.LoadDefault()
ps, err := parser.ParseFile("e2e/stories/launch-dry-run.yaml")
if err != nil {
    // closed-key violations surface here as `field "X" not found`
}
findings := validator.ValidateOne(ps, validator.Options{Rules: doc})
for _, f := range findings {
    fmt.Printf("%s:%d: %s — %s\n", f.File, f.Line, f.Rule, f.Message)
}
```

## Linking from scenarios

Scenarios reference stories by `story_id`. Three coupling tiers are
recommended (the scenario track makes the final call):

| Tier | Scenario carries | Drift detection |
|------|------------------|------------------|
| loose | `story_id` | none |
| versioned | `story_id`, `story_schema_min` | floor only |
| strict | `story_id`, `story_schema_min`, `story_content_sha256` | full |

For strict pinning, compute the digest at authoring time:

```go
import "hop.top/kit/go/conformance/story"

s, _ := story.ReadStory("e2e/stories/launch.yaml")
digest, _ := story.ContentSHA256(s)
// store digest alongside story_id in the scenario file
```

`ContentSHA256` is stable across whitespace / comment / key-order
changes — re-marshalling through `yaml.v3` normalizes formatting
before hashing.

## Leak-rule resistance

`go/conformance/story/leak_resistance_test.go` is the live cross-
check: every reference story under `examples/spaced/e2e/stories/`
must pass both the story validator AND the leak detector on the
same bytes. A regression on either side fails the test, so the
structural-distinctness claim is enforced rather than asserted in
prose.

## Schema version policy

- `schema_version: "1"` is the only accepted value in v1.
- Additive fields within v1 are allowed in minor bumps.
- Major bump (v2) is breaking; the v2 validator will ship a
  v1-compat mode.

The wire-format JSON Schema for cross-language adopters is at
[`contracts/story-schema.json`](../../../contracts/story-schema.json).
