package scenario

import "time"

// Verdict is the top-level pass/fail/ungradable outcome a scenario
// resolves to after grading. Identifying field at every tier.
type Verdict string

const (
	// VerdictPass means the configured grading policy accepted the
	// observed assertions (default: all assertions pass).
	VerdictPass Verdict = "pass"

	// VerdictFail means the grading policy rejected the run
	// (default: at least one assertion failed).
	VerdictFail Verdict = "fail"

	// VerdictUngradable means the grader could not produce a
	// verdict — e.g., story hash mismatch, judge unavailable,
	// not_implemented verb under all_assertions_pass.
	VerdictUngradable Verdict = "ungradable"
)

// Status is the per-assertion / per-facet outcome. Distinct from
// Verdict so the wire format can surface "this assertion didn't run"
// without bleeding into the top-level outcome.
type Status string

const (
	// StatusPass means the verb evaluator returned a positive match.
	StatusPass Status = "pass"

	// StatusFail means the verb evaluator returned a negative match.
	StatusFail Status = "fail"

	// StatusNotImplemented means the verb is in the rules JSON but
	// the grader does not yet implement it (v1: auth_lifecycle_clean).
	StatusNotImplemented Status = "not_implemented"

	// StatusUngradable means a precondition for evaluation was not
	// satisfied (e.g., judge unavailable; on-step capture missing).
	StatusUngradable Status = "ungradable"
)

// Result is the Tier-3 (full trace) grading result. Callers redact
// to Tier 2 (facets) or Tier 1 (verdict-only) via ToTier(n).
//
// Identifying fields (ScenarioID, SchemaVersion, Verdict, Reason,
// ScoredAt, GraderVersion, RulesVersion, Tier) appear at every
// tier. The body fields (Facets, Assertions, JudgeTraces) are
// progressively stripped.
type Result struct {
	ScenarioID    string            `json:"scenario_id" yaml:"scenario_id"`
	SchemaVersion string            `json:"schema_version" yaml:"schema_version"`
	Verdict       Verdict           `json:"verdict" yaml:"verdict"`
	Reason        string            `json:"reason,omitempty" yaml:"reason,omitempty"`
	ScoredAt      time.Time         `json:"scored_at" yaml:"scored_at"`
	GraderVersion string            `json:"grader_version" yaml:"grader_version"`
	RulesVersion  string            `json:"rules_version" yaml:"rules_version"`
	Tier          int               `json:"tier" yaml:"tier"`
	Facets        []FactorFacet     `json:"facets,omitempty" yaml:"facets,omitempty"`
	Assertions    []AssertionResult `json:"assertions,omitempty" yaml:"assertions,omitempty"`
	JudgeTraces   []JudgeTrace      `json:"judge_traces,omitempty" yaml:"judge_traces,omitempty"`
}

// FactorFacet is the per-factor rollup surfaced at Tier 2. Each
// facet's Status is the worst of its contributing assertions:
// fail > ungradable > not_implemented > pass.
type FactorFacet struct {
	Factor int    `json:"factor" yaml:"factor"`
	Status Status `json:"status" yaml:"status"`
}

// AssertionResult is one entry in the Tier-3 trace. Observed and
// Expected are free-form values the verb implementation populates
// for diagnosability; both fields are stripped at Tier 2.
type AssertionResult struct {
	ID       string `json:"id" yaml:"id"`
	Kind     string `json:"kind" yaml:"kind"`
	Factor   int    `json:"factor" yaml:"factor"`
	Status   Status `json:"status" yaml:"status"`
	Observed any    `json:"observed,omitempty" yaml:"observed,omitempty"`
	Expected any    `json:"expected,omitempty" yaml:"expected,omitempty"`
	Message  string `json:"message,omitempty" yaml:"message,omitempty"`
}

// JudgeTrace records one AIJudge invocation, surfaced only at Tier 3
// (per Q8: judge facets do not appear separately at Tier 2; they
// roll up into the factor facet of their assertion).
type JudgeTrace struct {
	JudgeID     string  `json:"judge_id" yaml:"judge_id"`
	AssertionID string  `json:"assertion_id" yaml:"assertion_id"`
	Model       string  `json:"model" yaml:"model"`
	Score       float64 `json:"score" yaml:"score"`
	Rationale   string  `json:"rationale,omitempty" yaml:"rationale,omitempty"`
	TokensIn    int     `json:"tokens_in,omitempty" yaml:"tokens_in,omitempty"`
	TokensOut   int     `json:"tokens_out,omitempty" yaml:"tokens_out,omitempty"`
}

// ToTier returns a copy of r redacted to the requested tier. Invalid
// tier values default to Tier 1 (most conservative).
//
//	Tier 1: verdict + identifying metadata only.
//	Tier 2: + per-factor facets.
//	Tier 3: + per-assertion trace + judge traces.
//
// Per design Q8, judge facets are NOT surfaced separately at Tier 2;
// each judge_score_above assertion's outcome rolls up to its
// declared Factor (typically 4 or 9).
func (r *Result) ToTier(t int) *Result {
	if r == nil {
		return nil
	}
	out := *r
	out.Tier = t
	switch t {
	case 1:
		out.Facets = nil
		out.Assertions = nil
		out.JudgeTraces = nil
	case 2:
		out.Assertions = nil
		out.JudgeTraces = nil
		// Facets already populated by grader; pass through.
	case 3:
		// Full trace; pass through.
	default:
		out.Tier = 1
		out.Facets = nil
		out.Assertions = nil
		out.JudgeTraces = nil
	}
	return &out
}

// statusRank gives an order to Status values for facet aggregation.
// Higher rank wins when rolling up multiple assertions into one
// facet.
func statusRank(s Status) int {
	switch s {
	case StatusFail:
		return 4
	case StatusUngradable:
		return 3
	case StatusNotImplemented:
		return 2
	case StatusPass:
		return 1
	}
	return 0
}

// rollupFacets aggregates assertion results into per-factor facets.
// Each factor's status is the max-rank status of its contributing
// assertions.
func rollupFacets(assertions []AssertionResult) []FactorFacet {
	by := map[int]Status{}
	order := []int{}
	for _, a := range assertions {
		if a.Factor < 1 || a.Factor > 12 {
			continue
		}
		cur, ok := by[a.Factor]
		if !ok {
			by[a.Factor] = a.Status
			order = append(order, a.Factor)
			continue
		}
		if statusRank(a.Status) > statusRank(cur) {
			by[a.Factor] = a.Status
		}
	}
	out := make([]FactorFacet, 0, len(order))
	for _, f := range order {
		out = append(out, FactorFacet{Factor: f, Status: by[f]})
	}
	return out
}
