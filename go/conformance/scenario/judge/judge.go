// Package judge defines the AIJudge interface the scenario grader
// uses for judge_score_above assertions. Production implementations
// (e.g., Anthropic, OpenAI, Bedrock) live in the svc service
// repo; the kit library ships only the interface and a Canned stub
// for tests.
//
// The prompt itself is treated as out-of-repo (rubric content stays
// adopter-private). The library never loads from filesystem; the
// caller passes a JudgePromptResolver that materializes prompt_ref
// references into prompt bodies.
package judge

import (
	"context"
	"fmt"
	"time"
)

// AIJudge is the abstract interface a scenario grader consults for
// judge_score_above assertions. Implementations are responsible for
// invoking the underlying model, parsing the response, and
// returning a Response with at minimum a Score in [0,1].
//
// Score must be deterministic across retries when given identical
// inputs (caching is permitted). Errors are wire-level: parse
// failure, model unavailable, etc. — the grader maps these to
// JudgeStatusUngradable rather than a top-level error.
type AIJudge interface {
	Score(ctx context.Context, req Request) (Response, error)
}

// Request is the payload one judge_score_above assertion sends to
// the AIJudge implementation. JudgeID identifies which JudgeBlock
// in the scenario this came from (useful for batching / caching).
//
// Input is typically captured stdout for the step the judge runs
// against; the grader is responsible for trimming + redacting before
// invocation per its tier policy.
type Request struct {
	JudgeID   string
	Prompt    string
	Model     string
	Input     string
	Timeout   time.Duration
	MaxTokens int
}

// Response is the structured judge output. Score is the [0,1] gauge
// the grader compares against the assertion's value:. Rationale is
// free-form text the model returned (clipped before persistence).
// TokensIn / TokensOut are accounting fields the service may use
// for budgeting.
type Response struct {
	Score     float64
	Rationale string
	TokensIn  int
	TokensOut int
}

// PromptResolver materializes a JudgeBlock.PromptRef into a prompt
// body. The grader threads the caller-supplied resolver through
// Input.JudgePromptResolver; nil means "no prompt_ref support",
// which causes judge blocks with prompt_ref set to surface
// JUDGE_PROMPT_UNRESOLVED.
type PromptResolver func(promptRef string) (string, error)

// Canned is a fixed-score AIJudge stub for tests. Scores maps
// JudgeID → score; missing IDs cause Score to return an error.
//
// Construct via NewCanned for the typical static-table case; tests
// that want dynamic behavior can wrap their own AIJudge impl.
type Canned struct {
	Scores    map[string]float64
	Rationale string
}

// NewCanned constructs a Canned judge with the given score table.
func NewCanned(scores map[string]float64) *Canned {
	out := &Canned{Scores: make(map[string]float64, len(scores))}
	for k, v := range scores {
		out.Scores[k] = v
	}
	return out
}

// Score returns the table entry for req.JudgeID. Unknown JudgeIDs
// return an error so test fixtures cannot silently skip a judge.
func (c *Canned) Score(_ context.Context, req Request) (Response, error) {
	if c == nil || c.Scores == nil {
		return Response{}, fmt.Errorf("judge.Canned: no score table; cannot score %q", req.JudgeID)
	}
	s, ok := c.Scores[req.JudgeID]
	if !ok {
		return Response{}, fmt.Errorf("judge.Canned: no canned score for %q", req.JudgeID)
	}
	rationale := c.Rationale
	if rationale == "" {
		rationale = "canned"
	}
	return Response{Score: s, Rationale: rationale}, nil
}
