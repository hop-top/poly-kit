package judge

import (
	"context"
	"math"
	"sort"
	"strconv"

	"hop.top/kit/go/ai/llm"
)

// Kind is a question's answer type.
type Kind string

const (
	// KindChoice: pick one of Question.Options.
	KindChoice Kind = "choice"
	// KindScore: grade against ordered Question.Anchors.
	KindScore Kind = "score"
	// KindYesNo: yes / no (/ abstain when allowed).
	KindYesNo Kind = "yesno"
)

// Reserved labels.
const (
	LabelYes     = "yes"
	LabelNo      = "no"
	LabelAbstain = "abstain"
)

// Option is one choice label with an optional description for the backend.
type Option struct {
	Value       string
	Description string
}

// Question is one typed question about a Case.
type Question struct {
	Name         string
	Instructions string
	Kind         Kind
	// Options: KindChoice only; >= 2 unique values; "abstain" reserved.
	Options []Option
	// Anchors: KindScore only; >= 2 descriptions ordered low to high.
	Anchors []string
	// Abstain allows the "abstain" label.
	Abstain bool
}

// Case is one unit of judgment: state, questions, optional media.
type Case struct {
	State     map[string]any
	Questions []Question
	Assets    []llm.ContentPart
}

// Answer is one question's typed result.
type Answer struct {
	// Value is the chosen label (see Labels); "abstain" when Abstained.
	Value     string
	Abstained bool
	// Score in [0,1]; KindScore only. Expected value over anchors when
	// Probs is set; 0 when Abstained.
	Score float64
	// Probs: label to probability; empty when the backend has none.
	// When set: keys are valid labels, sum is 1 within ProbEpsilon.
	Probs map[string]float64
	// Confidence is P(Value); with Probs it equals Probs[Value].
	Confidence float64
	Why        string
	// Source names what produced the answer (e.g. "logprob", "verbalized").
	Source string
}

// Cost of a judgment.
type Cost struct {
	USD    float64
	Tokens int64
}

// Verdict is a Case's result: one Answer per Question, keyed by name.
type Verdict struct {
	Answers map[string]Answer
	Usage   llm.Usage
	Cost    Cost
	Backend string
	Model   string
}

// Judge answers one Case.
type Judge interface {
	Judge(ctx context.Context, c Case) (Verdict, error)
}

// BatchJudge is an optional Judge capability: answer many Cases in one
// call (provider batch APIs). Result order matches input order.
type BatchJudge interface {
	JudgeBatch(ctx context.Context, cs []Case) ([]Verdict, error)
}

// ProbEpsilon is the tolerance for probability-sum and derived-value checks.
const ProbEpsilon = 1e-6

// Labels returns q's valid answer labels in canonical order, abstain last.
func Labels(q Question) []string {
	var ls []string
	switch q.Kind {
	case KindChoice:
		for _, o := range q.Options {
			ls = append(ls, o.Value)
		}
	case KindYesNo:
		ls = []string{LabelYes, LabelNo}
	case KindScore:
		for i := range q.Anchors {
			ls = append(ls, strconv.Itoa(i))
		}
	}
	if q.Abstain {
		ls = append(ls, LabelAbstain)
	}
	return ls
}

// ScoreEV is the expected anchor index over probs scaled to [0,1]:
// sum(p_i * i) / (n-1). Same scale and arithmetic as the inbox decider's
// score_value (anchors evenly span 0..1; index i maps to i/(n-1); capped
// at 1; probs are not renormalized). Extension: "abstain" mass is
// excluded and the rest divided by 1-P(abstain) (0 when nothing is left).
// Unparsable or out-of-range keys are ignored. n < 2 yields 0.
func ScoreEV(probs map[string]float64, n int) float64 {
	if n < 2 {
		return 0
	}
	var ev float64
	for k, p := range probs {
		if i, err := strconv.Atoi(k); err == nil && i >= 0 && i < n {
			ev += p * float64(i)
		}
	}
	if ab, ok := probs[LabelAbstain]; ok {
		live := 1 - ab
		if live <= 0 {
			return 0
		}
		ev /= live
	}
	return clamp01(ev / float64(n-1))
}

// ScoreFromIndex scales a single anchor index to [0,1]: idx/(n-1), the
// no-probabilities fallback of the inbox decider's score_value.
func ScoreFromIndex(idx float64, n int) float64 {
	if n < 2 {
		return 0
	}
	return clamp01(idx / float64(n-1))
}

// Argmax returns the label with the highest probability; ties go to the
// label earlier in order. Labels missing from probs count as 0. Empty
// order returns "".
func Argmax(probs map[string]float64, order []string) string {
	best, bp := "", math.Inf(-1)
	for _, l := range order {
		if p := probs[l]; p > bp {
			best, bp = l, p
		}
	}
	return best
}

// SortedLabels returns probs' keys sorted; helper for stable iteration.
func SortedLabels(probs map[string]float64) []string {
	ks := make([]string, 0, len(probs))
	for k := range probs {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func clamp01(x float64) float64 {
	switch {
	case x < 0 || math.IsNaN(x):
		return 0
	case x > 1:
		return 1
	}
	return x
}
