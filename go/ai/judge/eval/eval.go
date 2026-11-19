package eval

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/calibrate"
)

var (
	// ErrMismatch: reference and candidate (or a case) disagree on which
	// questions exist, their kinds, or which labels are valid.
	ErrMismatch = errors.New("eval: mismatch")
	// ErrNoReference: an entry has no recorded reference and no reference
	// judge was given.
	ErrNoReference = errors.New("eval: no reference")
)

// DefaultThresholds are the confidence cut-offs reported when
// Options.Thresholds is empty.
var DefaultThresholds = []float64{0.5, 0.7, 0.8, 0.9, 0.95}

// DefaultBins is the ECE bin count when Options.Bins is 0.
const DefaultBins = 10

// Reference is the ground truth for one case: a recorded verdict (another
// judge's answers) and/or per-question labels. Labels win per question
// over Verdict answers.
type Reference struct {
	Verdict *judge.Verdict    `json:"verdict,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
}

func (r *Reference) empty() bool {
	return r == nil || (r.Verdict == nil && len(r.Labels) == 0)
}

// Entry is one recorded case with its optional reference.
type Entry struct {
	Case      judge.Case `json:"case"`
	Reference *Reference `json:"reference,omitempty"`
}

// Options tunes Compare.
type Options struct {
	// Parallel caps concurrent Judge calls per judge (see judge.Batch).
	Parallel int
	// Thresholds are the confidence cut-offs for coverage / agreement;
	// default DefaultThresholds. Each must be in [0,1].
	Thresholds []float64
	// Bins is the ECE bin count; default DefaultBins.
	Bins int
}

// Compare replays set through cand and scores it against the references.
//
// An entry's reference is its recorded Reference when it has one; else ref
// judges the case. ref may be nil when every entry is recorded. Reference
// and candidate must answer exactly the case's questions, else ErrMismatch.
// Both judges run through judge.Batch with opt.Parallel.
func Compare(ctx context.Context, ref, cand judge.Judge, set []Entry, opt Options) (Report, error) {
	if cand == nil {
		return Report{}, errors.New("eval: nil candidate judge")
	}
	ths, bins, err := opt.resolve()
	if err != nil {
		return Report{}, err
	}
	cases := make([]judge.Case, len(set))
	for i, e := range set {
		cases[i] = e.Case
	}

	judged, err := judgeUnrecorded(ctx, ref, set, opt.Parallel)
	if err != nil {
		return Report{}, err
	}
	cvs, err := judge.Batch(ctx, cand, cases, opt.Parallel)
	if err != nil {
		return Report{}, fmt.Errorf("eval: candidate: %w", err)
	}

	acc := newAccumulator(ths, bins)
	for i, e := range set {
		refs, err := resolveRef(e.Case, e.Reference, judged[i])
		if err != nil {
			return Report{}, fmt.Errorf("case %d: reference: %w", i, err)
		}
		if err := checkVerdict(e.Case, cvs[i]); err != nil {
			return Report{}, fmt.Errorf("case %d: candidate: %w", i, err)
		}
		if err := acc.add(e.Case, refs, cvs[i]); err != nil {
			return Report{}, fmt.Errorf("case %d: %w", i, err)
		}
	}
	return acc.report(len(set)), nil
}

// Record judges cases with j and returns them as entries whose reference is
// j's verdict: the recorded day to replay later against another judge.
func Record(ctx context.Context, j judge.Judge, cases []judge.Case, parallel int) ([]Entry, error) {
	vs, err := judge.Batch(ctx, j, cases, parallel)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(cases))
	for i, c := range cases {
		v := vs[i]
		out[i] = Entry{Case: c, Reference: &Reference{Verdict: &v}}
	}
	return out, nil
}

func (o Options) resolve() ([]float64, int, error) {
	ths := slices.Clone(o.Thresholds)
	if len(ths) == 0 {
		ths = slices.Clone(DefaultThresholds)
	}
	for _, t := range ths {
		if t < 0 || t > 1 || t != t {
			return nil, 0, fmt.Errorf("eval: threshold %v outside [0,1]", t)
		}
	}
	slices.Sort(ths)
	bins := o.Bins
	if bins < 0 {
		return nil, 0, fmt.Errorf("eval: bins %d < 0", bins)
	}
	if bins == 0 {
		bins = DefaultBins
	}
	return ths, bins, nil
}

// judgeUnrecorded runs ref on entries lacking a reference; the result is
// indexed like set (nil where recorded).
func judgeUnrecorded(ctx context.Context, ref judge.Judge, set []Entry, parallel int) ([]*judge.Verdict, error) {
	out := make([]*judge.Verdict, len(set))
	var idx []int
	var cs []judge.Case
	for i, e := range set {
		if e.Reference.empty() {
			idx = append(idx, i)
			cs = append(cs, e.Case)
		}
	}
	if len(idx) == 0 {
		return out, nil
	}
	if ref == nil {
		return nil, fmt.Errorf("%w: case %d has none and no reference judge was given", ErrNoReference, idx[0])
	}
	vs, err := judge.Batch(ctx, ref, cs, parallel)
	if err != nil {
		return nil, fmt.Errorf("eval: reference: %w", err)
	}
	for k, i := range idx {
		out[i] = &vs[k]
	}
	return out, nil
}

// refAnswer is the reference side of one question.
type refAnswer struct {
	label    string
	score    float64
	hasScore bool
}

func resolveRef(c judge.Case, r *Reference, judged *judge.Verdict) (map[string]refAnswer, error) {
	v := judged
	if r != nil && r.Verdict != nil {
		v = r.Verdict
	}
	out := map[string]refAnswer{}
	if v != nil {
		if err := checkVerdict(c, *v); err != nil {
			return nil, err
		}
		for _, q := range c.Questions {
			a := v.Answers[q.Name]
			out[q.Name] = refAnswer{
				label:    a.Value,
				score:    a.Score,
				hasScore: q.Kind == judge.KindScore && !a.Abstained,
			}
		}
	}
	if r == nil {
		return out, nil
	}
	qs := map[string]judge.Question{}
	for _, q := range c.Questions {
		qs[q.Name] = q
	}
	for name, label := range r.Labels {
		q, ok := qs[name]
		if !ok {
			return nil, fmt.Errorf("%w: label for unknown question %q", ErrMismatch, name)
		}
		if !slices.Contains(judge.Labels(q), label) {
			return nil, fmt.Errorf("%w: label %q not valid for question %q", ErrMismatch, label, name)
		}
		ra := refAnswer{label: label}
		if q.Kind == judge.KindScore && label != judge.LabelAbstain {
			idx, err := strconv.Atoi(label)
			if err != nil {
				return nil, fmt.Errorf("%w: score label %q for question %q is not an anchor index", ErrMismatch, label, name)
			}
			ra.score, ra.hasScore = judge.ScoreFromIndex(float64(idx), len(q.Anchors)), true
		}
		out[name] = ra
	}
	return out, nil
}

// checkVerdict: v answers exactly c's questions (ErrMismatch), each valid
// (judge.ErrInvalid).
func checkVerdict(c judge.Case, v judge.Verdict) error {
	names := map[string]bool{}
	for _, q := range c.Questions {
		names[q.Name] = true
		if _, ok := v.Answers[q.Name]; !ok {
			return fmt.Errorf("%w: no answer for question %q", ErrMismatch, q.Name)
		}
	}
	for n := range v.Answers {
		if !names[n] {
			return fmt.Errorf("%w: answer for unknown question %q", ErrMismatch, n)
		}
	}
	return judge.ValidateVerdict(c, v)
}

// sample builds a calibrate.Sample from a candidate answer and its label.
func sample(question string, probs map[string]float64, label string) calibrate.Sample {
	return calibrate.Sample{Question: question, Probs: probs, Label: label}
}
