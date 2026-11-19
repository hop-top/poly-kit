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
	// KindGrounding: visual grounding (referring-expression localization):
	// locate what Question.Instructions name in an image asset of the Case;
	// the answer is Answer.Box. Preview: the only backends are jev (itself
	// preview) and llmjudge Verbalized with a vision model.
	KindGrounding Kind = "grounding"
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
	// Abstain allows the "abstain" label; for KindGrounding it means the
	// target may be absent from the image.
	Abstain bool
}

// Case is one unit of judgment: state, questions, optional media.
type Case struct {
	State     map[string]any
	Questions []Question
	Assets    []llm.ContentPart
}

// AssetNameKey is the ContentPart.Metadata key naming an image asset, so
// Question.Instructions can point at it ("the mug in `photo`"). Backends
// that address assets by name (jev) key the image by it in the state.
const AssetNameKey = "name"

// ImageNames returns the name of each image part in assets, in order:
// Metadata[AssetNameKey] when it is a non-empty string, else "image" for
// the only image or "image1", "image2", ... among several. Non-image parts
// are skipped. Validate rejects duplicate names.
func ImageNames(assets []llm.ContentPart) []string {
	n := 0
	for _, p := range assets {
		if p.Type == llm.PartTypeImage {
			n++
		}
	}
	var names []string
	for _, p := range assets {
		if p.Type != llm.PartTypeImage {
			continue
		}
		name, _ := p.Metadata[AssetNameKey].(string)
		switch {
		case name != "":
		case n == 1:
			name = "image"
		default:
			name = "image" + strconv.Itoa(len(names)+1)
		}
		names = append(names, name)
	}
	return names
}

// Answer is one question's typed result.
type Answer struct {
	// Value is the chosen label (see Labels); "abstain" when Abstained.
	Value     string
	Abstained bool
	// Score in [0,1]; KindScore only. Expected value over anchors when
	// Probs is set; 0 when Abstained.
	Score float64
	// Box is the located region; KindGrounding only, nil when Abstained.
	Box *Box
	// Probs: label to probability; empty when the backend has none.
	// When set: keys are valid labels, sum is 1 within ProbEpsilon.
	Probs map[string]float64
	// Confidence is P(Value); with Probs it equals Probs[Value]. For
	// KindGrounding it is the backend's confidence in Box, 0 when the
	// backend gives none (0 means unknown, not "certainly wrong").
	Confidence float64
	Why        string
	// Source names what produced the answer (e.g. "logprob", "verbalized").
	Source string
}

// Box is a bounding box as fractions of the image: edges in [0,1], origin
// top-left, Top above Bottom, Left before Right. Top and Bottom are
// fractions of the image height, Left and Right of its width.
type Box struct {
	Top, Bottom, Left, Right float64
}

// Valid reports whether b's edges are in [0,1] and ordered
// (Top < Bottom, Left < Right).
func (b Box) Valid() bool {
	return unit(b.Top) && unit(b.Bottom) && unit(b.Left) && unit(b.Right) &&
		b.Top < b.Bottom && b.Left < b.Right
}

// IoU is the intersection over union of b and o, in [0,1]; 0 when they
// do not overlap or either has no area.
func (b Box) IoU(o Box) float64 {
	w := math.Min(b.Right, o.Right) - math.Max(b.Left, o.Left)
	h := math.Min(b.Bottom, o.Bottom) - math.Max(b.Top, o.Top)
	if w <= 0 || h <= 0 {
		return 0
	}
	inter := w * h
	union := b.area() + o.area() - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

func (b Box) area() float64 {
	w, h := b.Right-b.Left, b.Bottom-b.Top
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
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
// KindGrounding has no labels (its answer is Answer.Box) and returns nil,
// abstain included; test q.Abstain for that.
func Labels(q Question) []string {
	if q.Kind == KindGrounding {
		return nil
	}
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
