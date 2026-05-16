package scenario

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"

	"hop.top/kit/go/conformance/scenario/verbs"
)

// ValidateError is one issue found during validator.Validate. Field
// identifies the offending key path (best-effort, e.g. "assertions[2].kind");
// Issue is a human-readable message. Multiple ValidateErrors are
// joined via errors.Join.
type ValidateError struct {
	Field string
	Issue string
}

func (e *ValidateError) Error() string {
	if e.Field == "" {
		return e.Issue
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Issue)
}

// ValidationErrors aggregates multiple ValidateError values into one
// error. The CLI boundary unwraps + counts; library callers
// errors.As(err, &ValidationErrors{}) to inspect.
type ValidationErrors struct {
	Errors []*ValidateError
}

func (e *ValidationErrors) Error() string {
	if e == nil || len(e.Errors) == 0 {
		return "no validation errors"
	}
	parts := make([]string, 0, len(e.Errors))
	for _, v := range e.Errors {
		parts = append(parts, v.Error())
	}
	return fmt.Sprintf("scenario invalid (%d issue(s)): %s",
		len(e.Errors), strings.Join(parts, "; "))
}

// scenarioIDRegex enforces the kebab-case + dot/underscore-extension
// shape from design §11 step 3.
var scenarioIDRegex = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)

// contentHashRegex enforces the design's "sha256:<lowercase-64-hex>"
// shape. Strict so a typo in the hash surfaces at validate time, not
// "story hash mismatch" at grade time.
var contentHashRegex = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Validate runs the structural + referential + per-verb argument
// checks on s. Returns a *ValidationErrors aggregating every issue
// found (no short-circuit on first failure).
//
// Validate is pure: no I/O, no network. Safe to call from any
// goroutine.
func Validate(s *Scenario) error {
	if s == nil {
		return &ValidationErrors{Errors: []*ValidateError{
			{Field: "", Issue: "scenario is nil"},
		}}
	}
	var errs []*ValidateError

	// 1. schema_version
	if s.SchemaVersion == "" {
		errs = append(errs, &ValidateError{Field: "schema_version", Issue: "required"})
	} else if !IsSupportedSchemaVersion(s.SchemaVersion) {
		errs = append(errs, &ValidateError{
			Field: "schema_version",
			Issue: fmt.Sprintf("unsupported %q; supported %v", s.SchemaVersion, SupportedSchemaVersions),
		})
	}

	// 2. engine_min_grader_version
	if s.EngineMinGraderVersion != "" {
		want, err := semver.NewVersion(s.EngineMinGraderVersion)
		if err != nil {
			errs = append(errs, &ValidateError{
				Field: "engine_min_grader_version",
				Issue: fmt.Sprintf("not a valid semver: %v", err),
			})
		} else {
			have, _ := semver.NewVersion(GraderVersion)
			if have != nil && have.LessThan(want) {
				errs = append(errs, &ValidateError{
					Field: "engine_min_grader_version",
					Issue: fmt.Sprintf("scenario requires grader >= %s; this binary is %s (upgrade kit)", s.EngineMinGraderVersion, GraderVersion),
				})
			}
		}
	}

	// 3. scenario_id
	if s.ScenarioID == "" {
		errs = append(errs, &ValidateError{Field: "scenario_id", Issue: "required"})
	} else if !scenarioIDRegex.MatchString(s.ScenarioID) {
		errs = append(errs, &ValidateError{
			Field: "scenario_id",
			Issue: fmt.Sprintf("%q does not match ^[a-z][a-z0-9._-]*$", s.ScenarioID),
		})
	}

	// 4. binary
	if s.Binary == "" {
		errs = append(errs, &ValidateError{Field: "binary", Issue: "required"})
	}

	// 5. factor_coverage
	if len(s.FactorCoverage) == 0 {
		errs = append(errs, &ValidateError{Field: "factor_coverage", Issue: "required, must be non-empty"})
	} else {
		seen := map[int]struct{}{}
		for i, f := range s.FactorCoverage {
			if f < 1 || f > 12 {
				errs = append(errs, &ValidateError{
					Field: fmt.Sprintf("factor_coverage[%d]", i),
					Issue: fmt.Sprintf("%d not in [1..12]", f),
				})
			}
			if _, dup := seen[f]; dup {
				errs = append(errs, &ValidateError{
					Field: fmt.Sprintf("factor_coverage[%d]", i),
					Issue: fmt.Sprintf("duplicate factor %d", f),
				})
			}
			seen[f] = struct{}{}
		}
	}

	// 6. tier
	if s.Tier < 1 || s.Tier > 3 {
		errs = append(errs, &ValidateError{
			Field: "tier",
			Issue: fmt.Sprintf("%d not in {1,2,3}", s.Tier),
		})
	}

	// 7. story_ref
	if s.StoryRef.StoryID == "" {
		errs = append(errs, &ValidateError{Field: "story_ref.story_id", Issue: "required"})
	}
	if s.StoryRef.StoryPath == "" {
		errs = append(errs, &ValidateError{Field: "story_ref.story_path", Issue: "required"})
	}
	if s.StoryRef.ContentHash == "" {
		errs = append(errs, &ValidateError{Field: "story_ref.content_hash", Issue: "required"})
	} else if !contentHashRegex.MatchString(s.StoryRef.ContentHash) {
		errs = append(errs, &ValidateError{
			Field: "story_ref.content_hash",
			Issue: "must match ^sha256:[0-9a-f]{64}$",
		})
	}

	// 8. steps
	if len(s.Steps) == 0 {
		errs = append(errs, &ValidateError{Field: "steps", Issue: "required, must be non-empty"})
	} else {
		seen := map[string]struct{}{}
		for i, st := range s.Steps {
			fpref := fmt.Sprintf("steps[%d]", i)
			if st.ID == "" {
				errs = append(errs, &ValidateError{Field: fpref + ".id", Issue: "required"})
			} else if _, dup := seen[st.ID]; dup {
				errs = append(errs, &ValidateError{
					Field: fpref + ".id",
					Issue: fmt.Sprintf("duplicate step id %q", st.ID),
				})
			}
			seen[st.ID] = struct{}{}
			if len(st.Invoke) == 0 {
				errs = append(errs, &ValidateError{
					Field: fpref + ".invoke",
					Issue: "required, must have at least one element",
				})
			}
		}
	}

	// Build step + actor + judge ID sets for referential checks.
	stepIDs := map[string]struct{}{}
	for _, st := range s.Steps {
		stepIDs[st.ID] = struct{}{}
	}
	actorIDs := map[string]struct{}{"default": {}}
	for _, a := range s.Actors {
		actorIDs[a.ID] = struct{}{}
	}
	judgeIDs := map[string]struct{}{}
	for _, j := range s.Judges {
		judgeIDs[j.ID] = struct{}{}
	}

	// Step.Actor referential check.
	for i, st := range s.Steps {
		if st.Actor != "" {
			if _, ok := actorIDs[st.Actor]; !ok {
				errs = append(errs, &ValidateError{
					Field: fmt.Sprintf("steps[%d].actor", i),
					Issue: fmt.Sprintf("references undeclared actor %q", st.Actor),
				})
			}
		}
	}

	// 9. assertions
	if len(s.Assertions) == 0 {
		errs = append(errs, &ValidateError{Field: "assertions", Issue: "required, must be non-empty"})
	} else {
		seen := map[string]struct{}{}
		// Track judge IDs referenced by assertions for §10 cross-check.
		referencedJudges := map[string]struct{}{}
		anyJudgeAssertion := false
		for i, a := range s.Assertions {
			fpref := fmt.Sprintf("assertions[%d]", i)
			if a.ID == "" {
				errs = append(errs, &ValidateError{Field: fpref + ".id", Issue: "required"})
			} else if _, dup := seen[a.ID]; dup {
				errs = append(errs, &ValidateError{
					Field: fpref + ".id",
					Issue: fmt.Sprintf("duplicate assertion id %q", a.ID),
				})
			}
			seen[a.ID] = struct{}{}

			if a.Kind == "" {
				errs = append(errs, &ValidateError{Field: fpref + ".kind", Issue: "required"})
				continue
			}
			if !verbs.IsKnown(a.Kind) {
				errs = append(errs, &ValidateError{
					Field: fpref + ".kind",
					Issue: fmt.Sprintf("%q not in v1 verb roster", a.Kind),
				})
				continue
			}

			if a.Factor < 1 || a.Factor > 12 {
				errs = append(errs, &ValidateError{
					Field: fpref + ".factor",
					Issue: fmt.Sprintf("%d not in [1..12]", a.Factor),
				})
			}

			// On-step reference. judge_score_above carries its own
			// on via the JudgeBlock; the assertion-level on is
			// optional for that verb (the block's on wins).
			if a.Kind != verbs.KindJudgeScoreAbove && a.On != "" {
				if _, ok := stepIDs[a.On]; !ok {
					errs = append(errs, &ValidateError{
						Field: fpref + ".on",
						Issue: fmt.Sprintf("references undeclared step %q", a.On),
					})
				}
			}

			// Per-kind argument shape.
			if argErrs := verbs.ValidateArgs(a.Kind, a.Args); len(argErrs) > 0 {
				for _, ae := range argErrs {
					errs = append(errs, &ValidateError{
						Field: fpref,
						Issue: fmt.Sprintf("%s: %s", a.Kind, ae),
					})
				}
			}

			if a.Kind == verbs.KindJudgeScoreAbove {
				anyJudgeAssertion = true
				if jid, ok := a.Args["judge_id"].(string); ok && jid != "" {
					referencedJudges[jid] = struct{}{}
					if _, jok := judgeIDs[jid]; !jok {
						errs = append(errs, &ValidateError{
							Field: fpref + ".judge_id",
							Issue: fmt.Sprintf("references undeclared judge %q", jid),
						})
					}
				}
			}
		}

		// 10. judges — required iff any assertion is
		// judge_score_above; every declared judge must be referenced.
		if anyJudgeAssertion && len(s.Judges) == 0 {
			errs = append(errs, &ValidateError{
				Field: "judge",
				Issue: "required when any assertion is judge_score_above",
			})
		}
		// Per-judge shape.
		seenJ := map[string]struct{}{}
		for i, j := range s.Judges {
			fpref := fmt.Sprintf("judge[%d]", i)
			if j.ID == "" {
				errs = append(errs, &ValidateError{Field: fpref + ".id", Issue: "required"})
			} else if _, dup := seenJ[j.ID]; dup {
				errs = append(errs, &ValidateError{
					Field: fpref + ".id",
					Issue: fmt.Sprintf("duplicate judge id %q", j.ID),
				})
			}
			seenJ[j.ID] = struct{}{}
			if j.On == "" {
				errs = append(errs, &ValidateError{Field: fpref + ".on", Issue: "required"})
			} else if _, ok := stepIDs[j.On]; !ok {
				errs = append(errs, &ValidateError{
					Field: fpref + ".on",
					Issue: fmt.Sprintf("references undeclared step %q", j.On),
				})
			}
			if j.Prompt == "" && j.PromptRef == "" {
				errs = append(errs, &ValidateError{
					Field: fpref,
					Issue: "exactly one of prompt or prompt_ref required",
				})
			}
			if j.Prompt != "" && j.PromptRef != "" {
				errs = append(errs, &ValidateError{
					Field: fpref,
					Issue: "prompt and prompt_ref are mutually exclusive",
				})
			}
			if j.Model == "" {
				errs = append(errs, &ValidateError{Field: fpref + ".model", Issue: "required"})
			}
			if len(j.ModelAllowlist) == 0 {
				errs = append(errs, &ValidateError{
					Field: fpref + ".model_allowlist",
					Issue: "required, must be non-empty",
				})
			} else if j.Model != "" {
				ok := false
				for _, m := range j.ModelAllowlist {
					if m == j.Model {
						ok = true
						break
					}
				}
				if !ok {
					errs = append(errs, &ValidateError{
						Field: fpref + ".model",
						Issue: fmt.Sprintf("%q not in model_allowlist %v", j.Model, j.ModelAllowlist),
					})
				}
			}
			if j.ID != "" {
				if _, used := referencedJudges[j.ID]; !used {
					errs = append(errs, &ValidateError{
						Field: fpref + ".id",
						Issue: fmt.Sprintf("judge %q not referenced by any judge_score_above assertion", j.ID),
					})
				}
			}
		}
	}

	// 11. grading
	if s.Grading != nil && s.Grading.PassIf != "" {
		switch s.Grading.PassIf {
		case PassIfAll, PassIfAny:
		default:
			errs = append(errs, &ValidateError{
				Field: "grading.pass_if",
				Issue: fmt.Sprintf("%q not in {%q, %q}", s.Grading.PassIf, PassIfAll, PassIfAny),
			})
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return &ValidationErrors{Errors: errs}
}

// IsValidationError reports whether err is or wraps a
// *ValidationErrors.
func IsValidationError(err error) bool {
	var ve *ValidationErrors
	return errors.As(err, &ve)
}
