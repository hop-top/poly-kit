// Package calibrate post-hoc calibrates judge probabilities per question:
// temperature scaling (any label count) and Platt scaling (binary). Math
// plus a judge.Judge wrapper; no I/O.
//
// Conventions: a distribution p is calibrated through its logits log p, so
// zero-probability labels stay zero. After calibration Value is re-picked
// as the argmax (ties keep the original Value), Confidence = P(Value),
// Score is recomputed for KindScore.
package calibrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"hop.top/kit/go/ai/judge"
)

// ErrParams marks unusable calibration params or samples.
var ErrParams = errors.New("calibrate: invalid")

const tiny = 1e-12

// Temperature scaling: p'_i = softmax(log p_i / T). T > 1 softens, T < 1
// sharpens; argmax is preserved.
type Temperature struct {
	T float64 `json:"t"`
}

// Apply scales probs. T must be finite and > 0.
func (t Temperature) Apply(probs map[string]float64) (map[string]float64, error) {
	if !(t.T > 0) || math.IsInf(t.T, 0) {
		return nil, fmt.Errorf("%w: temperature T=%v", ErrParams, t.T)
	}
	z := map[string]float64{}
	maxz := math.Inf(-1)
	for k, p := range probs {
		if p <= 0 {
			continue
		}
		z[k] = math.Log(p) / t.T
		maxz = math.Max(maxz, z[k])
	}
	if len(z) == 0 {
		return nil, fmt.Errorf("%w: no positive probabilities", ErrParams)
	}
	var sum float64
	for k, v := range z {
		z[k] = math.Exp(v - maxz)
		sum += z[k]
	}
	out := make(map[string]float64, len(probs))
	for k := range probs {
		out[k] = z[k] / sum // absent keys: 0
	}
	return out, nil
}

// Platt scaling for a binary question: P'(positive) =
// sigmoid(A*logit(P(positive)) + B). A=1, B=0 is the identity. Abstain
// mass, if any, is kept; the rest is rescaled between positive and the
// one remaining label.
type Platt struct {
	A float64 `json:"a"`
	B float64 `json:"b"`
	// Positive is the label being calibrated; default "yes".
	Positive string `json:"positive,omitempty"`
}

func (p Platt) positive() string {
	if p.Positive == "" {
		return judge.LabelYes
	}
	return p.Positive
}

// Apply scales probs, which must hold exactly one label besides Positive
// and "abstain".
func (p Platt) Apply(probs map[string]float64) (map[string]float64, error) {
	if math.IsNaN(p.A) || math.IsNaN(p.B) || math.IsInf(p.A, 0) || math.IsInf(p.B, 0) {
		return nil, fmt.Errorf("%w: platt A=%v B=%v", ErrParams, p.A, p.B)
	}
	pos := p.positive()
	other := ""
	for k := range probs {
		if k == pos || k == judge.LabelAbstain {
			continue
		}
		if other != "" {
			return nil, fmt.Errorf("%w: platt needs a binary question, got labels %v", ErrParams, judge.SortedLabels(probs))
		}
		other = k
	}
	if _, ok := probs[pos]; !ok || other == "" {
		return nil, fmt.Errorf("%w: platt positive %q and one other label required, got %v", ErrParams, pos, judge.SortedLabels(probs))
	}
	abst := probs[judge.LabelAbstain]
	live := 1 - abst
	out := map[string]float64{}
	if abst > 0 {
		out[judge.LabelAbstain] = abst
	}
	if live <= tiny {
		out[pos], out[other] = 0, 0
		return out, nil
	}
	q := sigmoid(p.A*logit(probs[pos]/live) + p.B)
	out[pos], out[other] = q*live, (1-q)*live
	return out, nil
}

// QuestionParams holds one question's calibrator; set exactly one.
type QuestionParams struct {
	Temperature *Temperature `json:"temperature,omitempty"`
	Platt       *Platt       `json:"platt,omitempty"`
}

// Params maps question name to calibrator. Questions without an entry pass
// through uncalibrated.
type Params struct {
	Questions map[string]QuestionParams `json:"questions"`
}

// Marshal encodes p as indented JSON.
func (p Params) Marshal() ([]byte, error) { return json.MarshalIndent(p, "", "  ") }

// Parse decodes and validates JSON params; unknown fields are rejected.
func Parse(data []byte) (Params, error) {
	var p Params
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Params{}, fmt.Errorf("%w: %v", ErrParams, err)
	}
	return p, p.Validate()
}

// Validate checks every entry has exactly one calibrator with usable values.
func (p Params) Validate() error {
	var errs []error
	for name, q := range p.Questions {
		switch {
		case (q.Temperature == nil) == (q.Platt == nil):
			errs = append(errs, fmt.Errorf("%w: question %q: set exactly one of temperature, platt", ErrParams, name))
		case q.Temperature != nil && (!(q.Temperature.T > 0) || math.IsInf(q.Temperature.T, 0)):
			errs = append(errs, fmt.Errorf("%w: question %q: T=%v", ErrParams, name, q.Temperature.T))
		case q.Platt != nil && (!finite(q.Platt.A) || !finite(q.Platt.B)):
			errs = append(errs, fmt.Errorf("%w: question %q: platt A=%v B=%v", ErrParams, name, q.Platt.A, q.Platt.B))
		}
	}
	return errors.Join(errs...)
}

// Apply calibrates a for question q. Answers without Probs, abstained
// answers, and questions without params are returned unchanged.
func (p Params) Apply(q judge.Question, a judge.Answer) (judge.Answer, error) {
	qp, ok := p.Questions[q.Name]
	if !ok || len(a.Probs) == 0 || a.Abstained {
		return a, nil
	}
	var (
		np  map[string]float64
		err error
	)
	switch {
	case qp.Temperature != nil:
		np, err = qp.Temperature.Apply(a.Probs)
	case qp.Platt != nil:
		np, err = qp.Platt.Apply(a.Probs)
	default:
		return a, fmt.Errorf("%w: question %q: no calibrator", ErrParams, q.Name)
	}
	if err != nil {
		return a, fmt.Errorf("question %q: %w", q.Name, err)
	}
	out := a
	out.Probs = np
	// ties keep the original Value
	best := a.Value
	for _, l := range judge.Labels(q) {
		if np[l] > np[best]+tiny {
			best = l
		}
	}
	out.Value = best
	out.Abstained = best == judge.LabelAbstain
	out.Confidence = np[best]
	if q.Kind == judge.KindScore {
		out.Score = 0
		if !out.Abstained {
			out.Score = judge.ScoreEV(np, len(q.Anchors))
		}
	}
	return out, nil
}

func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

func logit(p float64) float64 {
	p = math.Min(math.Max(p, tiny), 1-tiny)
	return math.Log(p / (1 - p))
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
