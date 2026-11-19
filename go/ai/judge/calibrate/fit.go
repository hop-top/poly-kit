package calibrate

import (
	"fmt"
	"math"
	"sort"

	"hop.top/kit/go/ai/judge"
)

// Method selects a calibrator family for Fit.
type Method string

const (
	MethodTemperature Method = "temperature"
	MethodPlatt       Method = "platt"
)

// Sample is one labeled prediction: the backend's Probs for Question and
// the ground-truth Label.
type Sample struct {
	Question string
	Probs    map[string]float64
	Label    string
}

// FitOptions tunes Fit.
type FitOptions struct {
	// Method for questions not in PerQuestion; default temperature.
	Method Method
	// PerQuestion overrides Method by question name.
	PerQuestion map[string]Method
	// Positive overrides the Platt positive label by question name
	// (default "yes").
	Positive map[string]string
}

// Fit groups samples by question and fits one calibrator each. Every
// question needs at least 2 samples.
func Fit(samples []Sample, opt FitOptions) (Params, error) {
	by := map[string][]Sample{}
	for _, s := range samples {
		by[s.Question] = append(by[s.Question], s)
	}
	names := make([]string, 0, len(by))
	for n := range by {
		names = append(names, n)
	}
	sort.Strings(names)
	out := Params{Questions: map[string]QuestionParams{}}
	for _, n := range names {
		m := opt.Method
		if pm, ok := opt.PerQuestion[n]; ok {
			m = pm
		}
		switch m {
		case "", MethodTemperature:
			t, err := FitTemperature(by[n])
			if err != nil {
				return Params{}, fmt.Errorf("question %q: %w", n, err)
			}
			out.Questions[n] = QuestionParams{Temperature: &t}
		case MethodPlatt:
			pos := opt.Positive[n]
			if pos == "" {
				pos = judge.LabelYes
			}
			pl, err := FitPlatt(by[n], pos)
			if err != nil {
				return Params{}, fmt.Errorf("question %q: %w", n, err)
			}
			out.Questions[n] = QuestionParams{Platt: &pl}
		default:
			return Params{}, fmt.Errorf("%w: question %q: unknown method %q", ErrParams, n, m)
		}
	}
	return out, nil
}

// validateSample rejects genuinely invalid input: empty label, no probs,
// NaN/Inf/negative/>1 probs, or probs not summing to 1 within
// judge.ProbEpsilon. A true label with probability 0 is valid (confident
// wrong answer); fitting clamps it to tiny. needLabel also requires Label
// to be a key of Probs.
func validateSample(s Sample, needLabel bool) error {
	if s.Label == "" {
		return fmt.Errorf("%w: empty label", ErrParams)
	}
	if len(s.Probs) == 0 {
		return fmt.Errorf("%w: no probabilities", ErrParams)
	}
	var sum float64
	for k, p := range s.Probs {
		if math.IsNaN(p) || p < 0 || p > 1 {
			return fmt.Errorf("%w: probability %v for %q not in [0,1]", ErrParams, p, k)
		}
		sum += p
	}
	if math.Abs(sum-1) > judge.ProbEpsilon {
		return fmt.Errorf("%w: probabilities sum %v, want 1 +/- %g", ErrParams, sum, judge.ProbEpsilon)
	}
	if _, ok := s.Probs[s.Label]; needLabel && !ok {
		return fmt.Errorf("%w: true label %q missing from probabilities", ErrParams, s.Label)
	}
	return nil
}

// FitTemperature minimizes the negative log-likelihood of the true labels
// under softmax(log p / T). Probabilities are floored at 1e-12 (log-loss
// convention; a confident-wrong sample costs a large finite loss, not an
// error, and samples are never dropped). No renormalisation: scaling all
// p by one constant shifts every log p equally, which softmax cancels.
// NLL is convex in 1/T, so a golden-section search over log T in
// [log 0.01, log 100] converges to the global optimum.
func FitTemperature(samples []Sample) (Temperature, error) {
	if len(samples) < 2 {
		return Temperature{}, fmt.Errorf("%w: need >= 2 samples, got %d", ErrParams, len(samples))
	}
	for i, s := range samples {
		if err := validateSample(s, true); err != nil {
			return Temperature{}, fmt.Errorf("sample %d: %w", i, err)
		}
	}
	nll := func(logT float64) float64 {
		inv := math.Exp(-logT)
		var total float64
		for _, s := range samples {
			maxz := math.Inf(-1)
			for _, p := range s.Probs {
				maxz = math.Max(maxz, math.Log(math.Max(p, tiny))*inv)
			}
			var sum float64
			for _, p := range s.Probs {
				sum += math.Exp(math.Log(math.Max(p, tiny))*inv - maxz)
			}
			total += math.Log(sum) + maxz - math.Log(math.Max(s.Probs[s.Label], tiny))*inv
		}
		return total / float64(len(samples))
	}
	lo, hi := math.Log(0.01), math.Log(100)
	g := (math.Sqrt(5) - 1) / 2
	a, b := hi-g*(hi-lo), lo+g*(hi-lo)
	fa, fb := nll(a), nll(b)
	for range 200 {
		if hi-lo < 1e-10 {
			break
		}
		if fa < fb {
			hi, b, fb = b, a, fa
			a = hi - g*(hi-lo)
			fa = nll(a)
		} else {
			lo, a, fa = a, b, fb
			b = lo + g*(hi-lo)
			fb = nll(b)
		}
	}
	return Temperature{T: math.Exp((lo + hi) / 2)}, nil
}

// FitPlatt fits A, B of sigmoid(A*logit(P(positive)) + B) by Newton's
// method on the logistic log-likelihood (tiny ridge keeps separable data
// finite). Samples whose Label is neither positive nor another label are
// still binary: Label == positive is y=1, anything else y=0. Abstain mass
// is excluded: P(positive) is taken as p/(1-p_abstain). P(positive) of 0 or
// 1 is clamped inside logit, so confident-wrong samples fit without error.
func FitPlatt(samples []Sample, positive string) (Platt, error) {
	if len(samples) < 2 {
		return Platt{}, fmt.Errorf("%w: need >= 2 samples, got %d", ErrParams, len(samples))
	}
	xs := make([]float64, 0, len(samples))
	ys := make([]float64, 0, len(samples))
	var npos int
	for i, s := range samples {
		if err := validateSample(s, false); err != nil {
			return Platt{}, fmt.Errorf("sample %d: %w", i, err)
		}
		live := 1 - s.Probs[judge.LabelAbstain]
		if live <= tiny || s.Label == judge.LabelAbstain {
			continue
		}
		xs = append(xs, logit(s.Probs[positive]/live))
		if s.Label == positive {
			ys = append(ys, 1)
			npos++
		} else {
			ys = append(ys, 0)
		}
	}
	if len(xs) < 2 || npos == 0 || npos == len(xs) {
		return Platt{}, fmt.Errorf("%w: platt needs both positive and negative samples", ErrParams)
	}
	const ridge = 1e-6
	a, b := 1.0, 0.0
	for range 100 {
		var ga, gb, haa, hab, hbb float64
		for i, x := range xs {
			p := sigmoid(a*x + b)
			r := p - ys[i]
			w := p * (1 - p)
			ga += r * x
			gb += r
			haa += w * x * x
			hab += w * x
			hbb += w
		}
		ga += ridge * (a - 1) // shrink toward identity
		haa += ridge
		hbb += ridge
		det := haa*hbb - hab*hab
		if det <= 0 {
			break
		}
		da := (hbb*ga - hab*gb) / det
		db := (haa*gb - hab*ga) / det
		a -= da
		b -= db
		if math.Abs(da) < 1e-10 && math.Abs(db) < 1e-10 {
			break
		}
	}
	return Platt{A: a, B: b, Positive: positive}, nil
}
