package verbs

import (
	"context"
	"fmt"
)

// judge_score_above: { judge_id: string, value: float 0..1 }

func init() {
	register(&Entry{
		Kind:     KindJudgeScoreAbove,
		Validate: validateJudgeScoreAbove,
		Evaluate: evalJudgeScoreAbove,
	})
}

func validateJudgeScoreAbove(args map[string]any) []string {
	var out []string
	if s, ok := args["judge_id"].(string); !ok || s == "" {
		out = append(out, "judge_id must be a non-empty string")
	}
	if _, ok := args["value"]; !ok {
		out = append(out, "missing required key value")
	} else {
		f, ok := toFloat(args["value"])
		if !ok {
			out = append(out, "value must be a number")
		} else if f < 0 || f > 1 {
			out = append(out, fmt.Sprintf("value %g not in [0,1]", f))
		}
	}
	return out
}

// inModelAllowlist reports whether want is in the configured
// allowlist.
func inModelAllowlist(want string, allow []string) bool {
	for _, m := range allow {
		if m == want {
			return true
		}
	}
	return false
}

func evalJudgeScoreAbove(ctx context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	jb := vctx.JudgeBlock
	if jb == nil {
		return Ungradable("judge_score_above: judge block missing from VerbContext")
	}
	if vctx.Judge == nil {
		return Ungradable("judge_score_above: no AIJudge configured (JUDGE_UNAVAILABLE)")
	}
	if !inModelAllowlist(jb.Model, jb.ModelAllowlist) {
		return Ungradable(fmt.Sprintf("judge_score_above: model %q not in allowlist %v (JUDGE_MODEL_REJECTED)", jb.Model, jb.ModelAllowlist))
	}
	prompt := jb.Prompt
	if prompt == "" && jb.PromptRef != "" {
		if vctx.PromptRes == nil {
			return Ungradable("judge_score_above: prompt_ref set but no resolver configured (JUDGE_PROMPT_UNRESOLVED)")
		}
		resolved, err := vctx.PromptRes(jb.PromptRef)
		if err != nil {
			return Ungradable(fmt.Sprintf("judge_score_above: prompt_ref resolve failed: %v (JUDGE_PROMPT_UNRESOLVED)", err))
		}
		prompt = resolved
	}
	if prompt == "" {
		return Ungradable("judge_score_above: empty prompt after resolution")
	}

	threshold, _ := toFloat(spec.Args["value"])
	req := judgeRequestFromBlock(jb, prompt, string(vctx.Capture.Stdout))
	resp, err := vctx.Judge.Score(ctx, req)
	if err != nil {
		return Ungradable(fmt.Sprintf("judge_score_above: model call failed: %v (JUDGE_PARSE_FAILED)", err))
	}
	trace := &JudgeTraceOut{
		JudgeID:     jb.ID,
		AssertionID: spec.ID,
		Model:       jb.Model,
		Score:       resp.Score,
		Rationale:   resp.Rationale,
		TokensIn:    resp.TokensIn,
		TokensOut:   resp.TokensOut,
	}
	if resp.Score >= threshold {
		return EvalResult{
			Status:     StatusPass,
			Observed:   resp.Score,
			Expected:   fmt.Sprintf(">=%g", threshold),
			JudgeTrace: trace,
		}
	}
	return EvalResult{
		Status:     StatusFail,
		Observed:   resp.Score,
		Expected:   fmt.Sprintf(">=%g", threshold),
		Message:    fmt.Sprintf("judge score %g below threshold %g", resp.Score, threshold),
		JudgeTrace: trace,
	}
}
