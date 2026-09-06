# story

## What it answers

How a user story is parsed and validated: the closed-key YAML shape,
the parser, the three-tier validator, and the helpers scenario tooling
consumes. A story describes **what a user is trying to do**, plain
English intent plus a command sequence. It carries no assertions, no
judges and no cassette guards; those live in the sibling `scenario`
package. The user-facing CLI wrapper is
`kit conformance verify-stories`.

## Use it when

- read a story file → `parser.ParseFile`, `parser.ParseBytes`
- validate one, or a set → `validator.ValidateOne`, `validator.ValidateAll`
- load the shared rule document → `scenariorules.LoadDefault()`
- pin a scenario to exact story bytes → `story.ContentSHA256`
- walk a story tree → `story.Discover`, `story.Index`, `story.ReadStory`
- check every invoked command and flag is declared → `--strict-toolspec` (validator tier 3)

## Quick start

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
```

## Contract

- Stories ship in the adopter's public repo (`e2e/stories/*.yaml`); scenarios, which carry the grading rubric, live in a separate private repo and reference stories by `story_id`.
- Structural distinctness from scenarios is enforced, not asserted: the closed-key YAML schema plus the metadata-key denylist mean `verify-no-leak` will never fire on a valid story. `story/leak_resistance_test.go` cross-checks every reference story under `examples/spaced/e2e/stories/` against both the validator and the leak detector on the same bytes.
- Validator tiers: 1 schema validity (always on), 1.5 metadata-key denylist sourced from `contracts/scenario-rules.json` (always on), 2 referential validity (on), 3 toolspec semantic validity (opt-in, `--strict-toolspec`). A fourth tier, runtime execution, is explicitly out of scope: stories are validated, never executed.
- `ContentSHA256` is stable across whitespace, comment and key-order changes; re-marshalling through `yaml.v3` normalizes formatting before hashing.
- `schema_version: "1"` is the only accepted value in v1. Additive fields within v1 are allowed in minor bumps; a major bump is breaking, and the v2 validator will ship a v1-compat mode.

## Neighbours

- [`schema/`](schema/): Go types and YAML tags for `Story`, `Step`, `Reference`, the closed-key set.
- [`parser/`](parser/): YAML decode with `KnownFields(true)`.
- [`validator/`](validator/): the three-tier validator.
- [`toolspec/`](toolspec/): the minimal toolspec projection tier 3 uses.
- `go/conformance/scenario`: the grading rubric that references a story.
- [`contracts/story-schema.json`](../../../contracts/story-schema.json): the wire-format JSON Schema for cross-language adopters.

## See also

- [Conformance reference](../../../docs/adopters/reference/conformance.md#story-dsl): the package layout, the validator tier table, the embedding example, the three scenario-coupling tiers and how to compute a digest, leak-rule resistance, the schema version policy
- ADR-0026: rationale and the scenario-coupling contract
