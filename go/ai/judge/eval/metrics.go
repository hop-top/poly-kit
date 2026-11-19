package eval

import (
	"fmt"
	"math"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/calibrate"
)

// accumulator folds per-case answers into per-question statistics.
type accumulator struct {
	ths     []float64
	bins    int
	iou     float64
	order   []string
	qs      map[string]*qAcc
	samples []calibrate.Sample
}

type qAcc struct {
	kind                  judge.Kind
	n, matches            int
	refAbs, candAbs       int
	bothAns, bothMatch    int
	covered, coveredMatch []int // per threshold
	nProbs                int
	brierSum              float64
	binN                  []int
	binConf, binAcc       []float64
	nScore                int
	scoreErr              float64
	confusion             map[string]map[string]int
	nIoU                  int
	iouSum                float64
}

func newAccumulator(ths []float64, bins int, iou float64) *accumulator {
	return &accumulator{ths: ths, bins: bins, iou: iou, qs: map[string]*qAcc{}}
}

func (a *accumulator) get(q judge.Question) (*qAcc, error) {
	s, ok := a.qs[q.Name]
	if !ok {
		s = &qAcc{
			kind:         q.Kind,
			covered:      make([]int, len(a.ths)),
			coveredMatch: make([]int, len(a.ths)),
			binN:         make([]int, a.bins),
			binConf:      make([]float64, a.bins),
			binAcc:       make([]float64, a.bins),
			confusion:    map[string]map[string]int{},
		}
		a.qs[q.Name] = s
		a.order = append(a.order, q.Name)
	}
	if s.kind != q.Kind {
		return nil, fmt.Errorf("%w: question %q is %s and %s in different cases", ErrMismatch, q.Name, s.kind, q.Kind)
	}
	return s, nil
}

func (a *accumulator) add(c judge.Case, refs map[string]refAnswer, v judge.Verdict) error {
	for _, q := range c.Questions {
		r, ok := refs[q.Name]
		if !ok {
			continue // label-only reference left this question unlabeled
		}
		s, err := a.get(q)
		if err != nil {
			return err
		}
		ca := v.Answers[q.Name]
		s.n++
		match := ca.Value == r.label
		var iou float64
		if q.Kind == judge.KindGrounding {
			match, iou = a.groundingMatch(r, ca)
		}
		if match {
			s.matches++
		}
		refAbs, candAbs := r.label == judge.LabelAbstain, ca.Abstained
		if refAbs {
			s.refAbs++
		}
		if candAbs {
			s.candAbs++
		}
		if !refAbs && !candAbs {
			s.bothAns++
			if match {
				s.bothMatch++
			}
		}
		if q.Kind == judge.KindGrounding {
			if !refAbs && !candAbs {
				s.nIoU++
				s.iouSum += iou
			}
		} else {
			if s.confusion[r.label] == nil {
				s.confusion[r.label] = map[string]int{}
			}
			s.confusion[r.label][ca.Value]++
		}

		if !candAbs {
			for i, t := range a.ths {
				if ca.Confidence >= t {
					s.covered[i]++
					if match {
						s.coveredMatch[i]++
					}
				}
			}
		}
		if len(ca.Probs) > 0 {
			s.addProbs(q, ca, r.label, match, a.bins)
			a.samples = append(a.samples, sample(q.Name, ca.Probs, r.label))
		}
		if q.Kind == judge.KindScore && r.hasScore && !candAbs {
			s.nScore++
			s.scoreErr += math.Abs(ca.Score - r.score)
		}
	}
	return nil
}

// groundingMatch: both abstain, or both boxed with IoU >= a.iou. The IoU
// is returned when both boxed.
func (a *accumulator) groundingMatch(r refAnswer, ca judge.Answer) (bool, float64) {
	refAbs := r.label == judge.LabelAbstain
	if refAbs || ca.Abstained {
		return refAbs && ca.Abstained, 0
	}
	if r.box == nil || ca.Box == nil {
		return false, 0
	}
	iou := r.box.IoU(*ca.Box)
	return iou >= a.iou, iou
}

func (s *qAcc) addProbs(q judge.Question, ca judge.Answer, label string, match bool, bins int) {
	s.nProbs++
	for _, l := range judge.Labels(q) {
		t := 0.0
		if l == label {
			t = 1
		}
		d := ca.Probs[l] - t
		s.brierSum += d * d
	}
	b := min(int(ca.Confidence*float64(bins)), bins-1)
	b = max(b, 0)
	s.binN[b]++
	s.binConf[b] += ca.Confidence
	if match {
		s.binAcc[b]++
	}
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

func (a *accumulator) report(n int) Report {
	r := Report{N: n, Samples: a.samples}
	for _, name := range a.order {
		s := a.qs[name]
		q := QuestionReport{
			Name: name, Kind: s.kind, N: s.n, Matches: s.matches,
			Agreement:  ratio(s.matches, s.n),
			RefAbstain: s.refAbs, CandAbstain: s.candAbs,
			NAnswered:         s.bothAns,
			AgreementAnswered: ratio(s.bothMatch, s.bothAns),
			NProbs:            s.nProbs,
			NScore:            s.nScore,
			NIoU:              s.nIoU,
			Confusion:         s.confusion,
		}
		for i, t := range a.ths {
			q.Thresholds = append(q.Thresholds, ThresholdStat{
				T: t, Covered: s.covered[i],
				Coverage:  ratio(s.covered[i], s.n),
				Agreement: ratio(s.coveredMatch[i], s.covered[i]),
			})
		}
		if s.nProbs > 0 {
			q.Brier = s.brierSum / float64(s.nProbs)
		}
		w := 1 / float64(a.bins)
		for b := range a.bins {
			bin := Bin{Lo: float64(b) * w, Hi: float64(b+1) * w, N: s.binN[b]}
			if bin.N > 0 {
				bin.Confidence = s.binConf[b] / float64(bin.N)
				bin.Accuracy = s.binAcc[b] / float64(bin.N)
				q.ECE += float64(bin.N) / float64(s.nProbs) * math.Abs(bin.Accuracy-bin.Confidence)
			}
			q.Bins = append(q.Bins, bin)
		}
		if s.nScore > 0 {
			q.ScoreMAE = s.scoreErr / float64(s.nScore)
		}
		if s.nIoU > 0 {
			q.MeanIoU = s.iouSum / float64(s.nIoU)
		}
		r.Questions = append(r.Questions, q)
	}
	return r
}
