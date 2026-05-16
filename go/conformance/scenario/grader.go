package scenario

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"hop.top/kit/go/conformance/scenario/verbs"
)

// Grade evaluates in against its scenario, emitting a Tier-3
// Result. The caller redacts with Result.ToTier(n) before
// surfacing to untrusted readers.
//
// Grade is the single library entry point. It:
//
//  1. Pre-checks the story content hash against scenario.StoryRef.
//     Mismatch ⇒ ungradable + StoryHashMismatch error.
//  2. Resolves cassette dirs to absolute paths.
//  3. Walks scenario.Assertions in declaration order, dispatching
//     each to its verb evaluator.
//  4. Rolls up assertion statuses into per-factor facets.
//  5. Aggregates the top-level Verdict per scenario.Grading.PassIf
//     (default: all_assertions_pass).
//
// Grade always returns a *Result; on hard failure (story hash, etc.)
// the result is populated with VerdictUngradable + Reason. The
// returned error is the wire-level *output.Error envelope for the
// CLI boundary; library callers can ignore it.
func Grade(ctx context.Context, in Input) (*Result, error) {
	now := time.Now().UTC()
	rulesVer := resolveRulesVersion()

	base := func(verdict Verdict, reason string) *Result {
		var sid, sver string
		var tier int
		if in.Scenario != nil {
			sid = in.Scenario.ScenarioID
			sver = in.Scenario.SchemaVersion
			tier = in.Scenario.Tier
		}
		return &Result{
			ScenarioID:    sid,
			SchemaVersion: sver,
			Verdict:       verdict,
			Reason:        reason,
			ScoredAt:      now,
			GraderVersion: GraderVersion,
			RulesVersion:  rulesVer,
			Tier:          tier,
		}
	}

	if in.Scenario == nil {
		r := base(VerdictUngradable, "scenario is nil")
		return r, &GraderError{Code: "GRADER_INTERNAL", Message: "Grade: in.Scenario is nil", ExitCode: 1}
	}

	// Story hash check.
	want := in.Scenario.StoryRef.ContentHash
	if want != "" {
		got := sha256.Sum256(in.StoryContent)
		gotHex := "sha256:" + hex.EncodeToString(got[:])
		if gotHex != want {
			r := base(VerdictUngradable, "story_hash_mismatch")
			return r, &GraderError{
				Code:     "STORY_HASH_MISMATCH",
				Message:  fmt.Sprintf("story content hash %s does not match scenario.story_ref.content_hash %s", gotHex, want),
				ExitCode: 4,
			}
		}
	}

	// Resolve cassette dirs to absolute. The grader leaves
	// CassetteDir empty when no resolution is possible; verbs handle
	// the empty-dir case by surfacing ungradable / pass per their
	// semantics.
	resolved := make(map[string]Capture, len(in.StepCaptures))
	for id, c := range in.StepCaptures {
		if c.CassetteDir != "" && !filepath.IsAbs(c.CassetteDir) {
			c.CassetteDir = filepath.Join(in.CassetteRoot, c.CassetteDir)
		}
		resolved[id] = c
	}

	// Walk assertions.
	results := make([]AssertionResult, 0, len(in.Scenario.Assertions))
	var traces []JudgeTrace
	for _, a := range in.Scenario.Assertions {
		ares := evalOne(ctx, in.Scenario, &a, resolved, in)
		results = append(results, ares.assertion)
		if ares.trace != nil {
			traces = append(traces, *ares.trace)
		}
	}

	// Roll up facets.
	facets := rollupFacets(results)

	// Aggregate verdict.
	verdict := aggregateVerdict(results, in.Scenario.Grading)
	reason := ""
	if verdict == VerdictUngradable {
		reason = ungradableReason(results)
	}

	r := base(verdict, reason)
	r.Facets = facets
	r.Assertions = results
	r.JudgeTraces = traces
	return r, nil
}

// evalOneResult bundles the per-assertion evaluation output.
type evalOneResult struct {
	assertion AssertionResult
	trace     *JudgeTrace
}

// evalOne dispatches one assertion through its verb evaluator.
// Missing capture ⇒ ungradable. Unknown verb ⇒ ungradable (validator
// should have caught it; defense-in-depth).
func evalOne(ctx context.Context, s *Scenario, a *Assertion, captures map[string]Capture, in Input) evalOneResult {
	res := AssertionResult{
		ID:     a.ID,
		Kind:   a.Kind,
		Factor: a.Factor,
		Status: StatusUngradable,
	}
	entry := verbs.Lookup(a.Kind)
	if entry == nil {
		res.Status = StatusUngradable
		res.Message = fmt.Sprintf("verb %q not registered in grader", a.Kind)
		return evalOneResult{assertion: res}
	}
	if entry.Evaluate == nil {
		res.Status = StatusNotImplemented
		res.Message = fmt.Sprintf("verb %q parsed but not implemented in v1", a.Kind)
		return evalOneResult{assertion: res}
	}

	// Resolve on-step capture.
	var cap Capture
	if a.On != "" {
		c, ok := captures[a.On]
		if !ok {
			res.Status = StatusUngradable
			res.Message = fmt.Sprintf("step %q has no recorded capture", a.On)
			return evalOneResult{assertion: res}
		}
		cap = c
	}

	// Build VerbContext.
	other := make(map[string]verbs.Capture, len(captures))
	for k, c := range captures {
		if k == a.On {
			continue
		}
		other[k] = toVerbsCapture(c)
	}
	vctx := verbs.VerbContext{
		Capture:       toVerbsCapture(cap),
		OtherCaptures: other,
		CassetteRoot:  in.CassetteRoot,
		Judge:         in.Judge,
		PromptRes:     in.JudgePromptResolver,
	}
	if a.Kind == verbs.KindJudgeScoreAbove {
		jid, _ := a.Args["judge_id"].(string)
		jb := findJudgeBlock(s, jid)
		if jb != nil {
			jbSpec := JudgeBlockSpec{*jb}
			vctx.JudgeBlock = &verbs.JudgeBlockSpec{
				ID:             jbSpec.JudgeBlock.ID,
				On:             jbSpec.JudgeBlock.On,
				Prompt:         jbSpec.JudgeBlock.Prompt,
				PromptRef:      jbSpec.JudgeBlock.PromptRef,
				Model:          jbSpec.JudgeBlock.Model,
				ModelAllowlist: append([]string(nil), jbSpec.JudgeBlock.ModelAllowlist...),
				RequiredScore:  jbSpec.JudgeBlock.RequiredScore,
			}
			// Capture for judge is the JudgeBlock's on-step, not the
			// assertion's on (which may be empty for judge verbs).
			if jc, ok := captures[jb.On]; ok {
				vctx.Capture = toVerbsCapture(jc)
			}
		}
	}

	spec := verbs.AssertionSpec{
		ID:     a.ID,
		Kind:   a.Kind,
		On:     a.On,
		Factor: a.Factor,
		Args:   a.Args,
	}
	er := entry.Evaluate(ctx, spec, vctx)
	res.Status = Status(er.Status)
	res.Observed = er.Observed
	res.Expected = er.Expected
	res.Message = er.Message
	out := evalOneResult{assertion: res}
	if er.JudgeTrace != nil {
		out.trace = &JudgeTrace{
			JudgeID:     er.JudgeTrace.JudgeID,
			AssertionID: er.JudgeTrace.AssertionID,
			Model:       er.JudgeTrace.Model,
			Score:       er.JudgeTrace.Score,
			Rationale:   er.JudgeTrace.Rationale,
			TokensIn:    er.JudgeTrace.TokensIn,
			TokensOut:   er.JudgeTrace.TokensOut,
		}
	}
	return out
}

// JudgeBlockSpec is a thin wrapper used to keep grader → verbs
// translation locally readable. Not exported because callers go
// through the public Scenario.Judges slice.
type JudgeBlockSpec struct {
	JudgeBlock JudgeBlock
}

func toVerbsCapture(c Capture) verbs.Capture {
	return verbs.Capture{
		ExitCode:    c.ExitCode,
		Stdout:      c.Stdout,
		Stderr:      c.Stderr,
		DurationMS:  c.DurationMS,
		CassetteDir: c.CassetteDir,
	}
}

func findJudgeBlock(s *Scenario, id string) *JudgeBlock {
	if s == nil || id == "" {
		return nil
	}
	for i := range s.Judges {
		if s.Judges[i].ID == id {
			return &s.Judges[i]
		}
	}
	return nil
}

// aggregateVerdict applies the scenario's grading policy to the
// per-assertion results.
func aggregateVerdict(results []AssertionResult, g *Grading) Verdict {
	policy := PassIfAll
	if g != nil && g.PassIf != "" {
		policy = g.PassIf
	}
	switch policy {
	case PassIfAny:
		for _, r := range results {
			if r.Status == StatusPass {
				return VerdictPass
			}
		}
		// No pass: ungradable wins if any are ungradable, else fail.
		for _, r := range results {
			if r.Status == StatusUngradable {
				return VerdictUngradable
			}
		}
		return VerdictFail
	default: // PassIfAll
		for _, r := range results {
			switch r.Status {
			case StatusFail:
				return VerdictFail
			case StatusUngradable, StatusNotImplemented:
				return VerdictUngradable
			}
		}
		return VerdictPass
	}
}

// ungradableReason returns the first non-pass non-fail reason, for
// surface text on the top-level Result.Reason.
func ungradableReason(results []AssertionResult) string {
	for _, r := range results {
		switch r.Status {
		case StatusUngradable:
			if r.Message != "" {
				return r.Message
			}
			return "assertion ungradable"
		case StatusNotImplemented:
			return "assertion_not_implemented"
		}
	}
	return ""
}

// GraderError is the wire-level error envelope the grader returns
// alongside a partial Result on hard failures. The CLI boundary
// converts it to *output.Error via AsCLIError.
type GraderError struct {
	Code     string
	Message  string
	ExitCode int
}

func (e *GraderError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

// resolveRulesVersion loads the embedded rules JSON via the shared
// scenariorules package and returns its RulesVersion. Falls back to
// "" if the load fails (defense-in-depth; the rules file ships in
// the binary).
func resolveRulesVersion() string {
	doc, err := loadDefaultRules()
	if err != nil || doc == nil {
		return ""
	}
	return doc.RulesVersion
}
