// Package scenario implements the kit conformance scenario DSL: a
// closed-vocabulary YAML rubric that grades a captured CLI run for
// 12-factor conformance.
//
// Scenarios are authored in adopter-private repositories; the kit
// binary ships the schema, parser, validator, and grader library
// only. The shared service (separate track) owns model
// invocation for judge_score_above assertions and routes grading
// requests through the AIJudge interface defined in
// hop.top/kit/go/conformance/scenario/judge.
//
// Library shape:
//
//	types.go        Scenario, Step, Assertion, StoryRef, JudgeBlock,
//	                Grading, Precondition, Actor — closed-key YAML
//	                shapes consumed by yaml.v3 KnownFields(true).
//	parser.go       ParseBytes / ParseFile entry points.
//	validator.go    Validate(*Scenario) returning structured findings
//	                that map to *output.Error envelopes via the
//	                Code* constants in go/console/output.
//	grader.go       Grade(ctx, Input) *Result — per-verb dispatch.
//	result.go       Result, Verdict, AssertionResult, JudgeTrace,
//	                FactorFacet, Status; ToTier(n) redaction.
//	tier.go         (in result.go) — three-tier wire format.
//	version.go      SchemaVersion, GraderVersion, RulesVersion.
//	verbs/          one file per verb; 21 implemented, 1 parsed-only
//	                (auth_lifecycle_clean).
//	judge/          AIJudge interface + Canned stub; production
//	                registry lives in svc.
//	testdata/       fixture scenarios for parser/validator/grader
//	                tests. Allowlisted in the kit-internal leak
//	                default (suppress.DefaultKitInternalGlobs).
//
// Strong story coupling: every scenario carries a story_ref with a
// content_hash. The grader refuses to grade when the supplied story
// bytes hash to anything other than the declared value, preventing
// scenarios from drifting past their underlying user story without
// an explicit re-author.
//
// Tier system: the grader emits Tier 3 (full trace) internally; the
// caller redacts to Tier 1 (verdict-only) or Tier 2 (factor facets)
// via Result.ToTier(n). Identifying fields (scenario_id,
// schema_version, scored_at, grader_version, rules_version, tier,
// verdict) appear at every tier.
//
//	and ADR-0027 for the full
//
// design contract.
package scenario
