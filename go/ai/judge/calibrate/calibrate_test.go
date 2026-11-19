package calibrate_test

import (
	"context"
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/calibrate"
	"hop.top/kit/go/ai/judge/judgetest"
)

func softmax(z []float64, t float64) []float64 {
	maxz := math.Inf(-1)
	for _, v := range z {
		maxz = math.Max(maxz, v/t)
	}
	out := make([]float64, len(z))
	var sum float64
	for i, v := range z {
		out[i] = math.Exp(v/t - maxz)
		sum += out[i]
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}

func draw(r *rand.Rand, p []float64) int {
	u := r.Float64()
	for i, v := range p {
		if u < v {
			return i
		}
		u -= v
	}
	return len(p) - 1
}

// Backend reports softmax(z); truth is drawn from softmax(z/tTrue), so the
// backend is miscalibrated by exactly tTrue.
func synthTemperature(tTrue float64, n int) []calibrate.Sample {
	r := rand.New(rand.NewPCG(1, 2))
	labels := []string{"a", "b", "c", "d"}
	var out []calibrate.Sample
	for range n {
		z := make([]float64, len(labels))
		for i := range z {
			z[i] = r.NormFloat64() * 2
		}
		rep, truth := softmax(z, 1), softmax(z, tTrue)
		s := calibrate.Sample{Question: "q", Probs: map[string]float64{}, Label: labels[draw(r, truth)]}
		for i, l := range labels {
			s.Probs[l] = rep[i]
		}
		out = append(out, s)
	}
	return out
}

func TestFitTemperatureRecoversKnownT(t *testing.T) {
	for _, tTrue := range []float64{0.6, 1.0, 2.5} {
		got, err := calibrate.FitTemperature(synthTemperature(tTrue, 20000))
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(got.T-tTrue)/tTrue > 0.08 {
			t.Errorf("T_true=%v fitted %v", tTrue, got.T)
		}
	}
}

func TestFitPlattRecoversKnownParams(t *testing.T) {
	const a, b = 0.6, -0.4
	r := rand.New(rand.NewPCG(3, 4))
	var ss []calibrate.Sample
	for range 50000 {
		x := r.NormFloat64() * 2
		p := 1 / (1 + math.Exp(-x)) // backend's reported P(yes)
		truth := 1 / (1 + math.Exp(-(a*x + b)))
		lab := "no"
		if r.Float64() < truth {
			lab = "yes"
		}
		ss = append(ss, calibrate.Sample{Question: "q", Label: lab, Probs: map[string]float64{"yes": p, "no": 1 - p}})
	}
	got, err := calibrate.FitPlatt(ss, "yes")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.A-a) > 0.04 || math.Abs(got.B-b) > 0.04 {
		t.Fatalf("fit A=%v B=%v, want %v %v", got.A, got.B, a, b)
	}
}

func TestFitPerQuestionAndJSONRoundTrip(t *testing.T) {
	ss := synthTemperature(2, 3000)
	r := rand.New(rand.NewPCG(5, 6))
	for range 3000 {
		p := r.Float64()*0.9 + 0.05
		lab := "no"
		if r.Float64() < p {
			lab = "yes"
		}
		ss = append(ss, calibrate.Sample{Question: "bin", Label: lab, Probs: map[string]float64{"yes": p, "no": 1 - p}})
	}
	params, err := calibrate.Fit(ss, calibrate.FitOptions{PerQuestion: map[string]calibrate.Method{"bin": calibrate.MethodPlatt}})
	if err != nil {
		t.Fatal(err)
	}
	if params.Questions["q"].Temperature == nil || params.Questions["bin"].Platt == nil {
		t.Fatalf("params = %+v", params)
	}
	data, err := params.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := calibrate.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if *back.Questions["q"].Temperature != *params.Questions["q"].Temperature ||
		*back.Questions["bin"].Platt != *params.Questions["bin"].Platt {
		t.Fatalf("round trip mismatch: %+v vs %+v", back, params)
	}
}

func TestParseRejects(t *testing.T) {
	for name, js := range map[string]string{
		"both":    `{"questions":{"q":{"temperature":{"t":1},"platt":{"a":1,"b":0}}}}`,
		"neither": `{"questions":{"q":{}}}`,
		"zero T":  `{"questions":{"q":{"temperature":{"t":0}}}}`,
		"unknown": `{"questions":{"q":{"temperature":{"t":1,"x":2}}}}`,
	} {
		if _, err := calibrate.Parse([]byte(js)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestTemperatureApply(t *testing.T) {
	p := map[string]float64{"a": 0.7, "b": 0.2, "c": 0.1, "d": 0}
	got, err := calibrate.Temperature{T: 1}.Apply(p)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range p {
		if math.Abs(got[k]-v) > 1e-12 {
			t.Fatalf("T=1 not identity: %v", got)
		}
	}
	soft, _ := calibrate.Temperature{T: 3}.Apply(p)
	if !(soft["a"] < 0.7 && soft["c"] > 0.1) || soft["d"] != 0 {
		t.Fatalf("T=3 should soften, keep zero: %v", soft)
	}
	var sum float64
	for _, v := range soft {
		sum += v
	}
	if math.Abs(sum-1) > 1e-12 {
		t.Fatalf("sum = %v", sum)
	}
	if _, err := (calibrate.Temperature{T: -1}).Apply(p); err == nil {
		t.Fatal("negative T accepted")
	}
}

func TestPlattApply(t *testing.T) {
	id, err := calibrate.Platt{A: 1}.Apply(map[string]float64{"yes": 0.8, "no": 0.2})
	if err != nil || math.Abs(id["yes"]-0.8) > 1e-12 {
		t.Fatalf("identity: %v %v", id, err)
	}
	// abstain mass preserved, rest rescaled
	ab, err := calibrate.Platt{A: 1, B: 0}.Apply(map[string]float64{"yes": 0.4, "no": 0.4, "abstain": 0.2})
	if err != nil || math.Abs(ab["abstain"]-0.2) > 1e-12 || math.Abs(ab["yes"]-0.4) > 1e-9 {
		t.Fatalf("abstain: %v %v", ab, err)
	}
	if _, err := (calibrate.Platt{A: 1}).Apply(map[string]float64{"yes": 0.4, "no": 0.3, "maybe": 0.3}); err == nil {
		t.Fatal("non-binary accepted")
	}
}

func TestCalibratedWrapsJudgetest(t *testing.T) {
	q := judge.Question{Name: "label", Kind: judge.KindChoice,
		Options: []judge.Option{{Value: "a"}, {Value: "b"}, {Value: "c"}}}
	sq := judge.Question{Name: "imp", Kind: judge.KindScore, Anchors: []string{"l", "m", "h"}}
	yq := judge.Question{Name: "ok", Kind: judge.KindYesNo, Abstain: true}
	c := judge.Case{Questions: []judge.Question{q, sq, yq}}
	raw := judge.Verdict{Backend: "fake", Answers: map[string]judge.Answer{
		"label": judgetest.Choice(map[string]float64{"a": 0.9, "b": 0.07, "c": 0.03}, "a", "b", "c"),
		"imp":   judgetest.Score(map[string]float64{"0": 0.05, "1": 0.05, "2": 0.9}, 3),
		"ok":    judgetest.Choice(map[string]float64{"yes": 0.6, "no": 0.3, "abstain": 0.1}, "yes", "no", "abstain"),
	}}
	fake := judgetest.Fixed(raw)
	params := calibrate.Params{Questions: map[string]calibrate.QuestionParams{
		"label": {Temperature: &calibrate.Temperature{T: 4}},
		"imp":   {Temperature: &calibrate.Temperature{T: 4}},
		"ok":    {Platt: &calibrate.Platt{A: 1, B: -2}},
	}}
	j := calibrate.Calibrated(fake, params)
	v, err := j.Judge(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err := judge.ValidateVerdict(c, v); err != nil {
		t.Fatalf("calibrated verdict invalid: %v", err)
	}
	if v.Backend != "fake" || len(fake.Calls()) != 1 {
		t.Fatalf("passthrough: %+v calls=%d", v, len(fake.Calls()))
	}
	la := v.Answers["label"]
	if la.Value != "a" || !(la.Confidence < 0.9) || la.Confidence != la.Probs["a"] {
		t.Fatalf("label not softened: %+v", la)
	}
	ia := v.Answers["imp"]
	if !(ia.Score < raw.Answers["imp"].Score) {
		t.Fatalf("score not recomputed: %v vs %v", ia.Score, raw.Answers["imp"].Score)
	}
	// Platt B=-2 pushes yes below no: Value flips, Confidence follows.
	oa := v.Answers["ok"]
	if oa.Value != "no" || oa.Confidence != oa.Probs["no"] || math.Abs(oa.Probs["abstain"]-0.1) > 1e-12 {
		t.Fatalf("platt flip: %+v", oa)
	}
	// input verdict not mutated
	if raw.Answers["label"].Confidence != 0.9 {
		t.Fatal("inner verdict mutated")
	}
}

func TestCalibratedPassThroughAndBatch(t *testing.T) {
	q := judge.Question{Name: "q", Kind: judge.KindYesNo}
	c := judge.Case{Questions: []judge.Question{q}}
	raw := judge.Verdict{Answers: map[string]judge.Answer{"q": judgetest.Choice(map[string]float64{"yes": 0.9, "no": 0.1}, "yes", "no")}}
	bf := judgetest.NewBatch(judgetest.Fixed(raw))
	params := calibrate.Params{Questions: map[string]calibrate.QuestionParams{"other": {Temperature: &calibrate.Temperature{T: 9}}}}
	j := calibrate.Calibrated(bf, params)
	bj, ok := j.(judge.BatchJudge)
	if !ok {
		t.Fatal("wrapper over BatchJudge must be BatchJudge")
	}
	vs, err := bj.JudgeBatch(context.Background(), []judge.Case{c, c})
	if err != nil || len(vs) != 2 || vs[0].Answers["q"].Confidence != 0.9 {
		t.Fatalf("vs=%v err=%v", vs, err)
	}
	if _, ok := calibrate.Calibrated(judgetest.Fixed(raw), params).(judge.BatchJudge); ok {
		t.Fatal("wrapper over plain Judge must not be BatchJudge")
	}
}

func TestCalibratedBadParams(t *testing.T) {
	bad := calibrate.Params{Questions: map[string]calibrate.QuestionParams{"q": {Temperature: &calibrate.Temperature{T: 0}}}}
	j := calibrate.Calibrated(judgetest.Fixed(judge.Verdict{}), bad)
	_, err := j.Judge(context.Background(), judge.Case{Questions: []judge.Question{{Name: "q", Kind: judge.KindYesNo}}})
	if err == nil || !strings.Contains(err.Error(), "T=0") {
		t.Fatalf("err = %v", err)
	}
}

// confidentWrong: backend put all mass on "a"; truth was "b" (prob 0).
func confidentWrong(n int) []calibrate.Sample {
	out := make([]calibrate.Sample, n)
	for i := range out {
		out[i] = calibrate.Sample{
			Question: "q",
			Probs:    map[string]float64{"a": 1, "b": 0, "c": 0, "d": 0},
			Label:    "b",
		}
	}
	return out
}

func TestFitTemperatureZeroProbTrueLabel(t *testing.T) {
	clean, err := calibrate.FitTemperature(synthTemperature(1.0, 2000))
	if err != nil {
		t.Fatal(err)
	}
	samples := append(synthTemperature(1.0, 2000), confidentWrong(40)...)
	got, err := calibrate.FitTemperature(samples)
	if err != nil {
		t.Fatalf("confident-wrong samples must not error: %v", err)
	}
	if math.IsNaN(got.T) || math.IsInf(got.T, 0) || got.T <= 0 {
		t.Fatalf("T = %v, want finite > 0", got.T)
	}
	if got.T <= clean.T {
		t.Errorf("T = %v with confident-wrong samples, want > clean %v (softer)", got.T, clean.T)
	}
	if _, err := got.Apply(map[string]float64{"a": 0.7, "b": 0.3}); err != nil {
		t.Errorf("fitted T unusable: %v", err)
	}
}

func TestFitZeroProbTrueLabelViaFitAndPlatt(t *testing.T) {
	ws := confidentWrong(5)
	for i := range ws {
		ws[i].Probs = map[string]float64{"yes": 1, "no": 0}
		ws[i].Label = "no"
	}
	rs := make([]calibrate.Sample, 5)
	for i := range rs {
		rs[i] = calibrate.Sample{Question: "q", Probs: map[string]float64{"yes": 0.9, "no": 0.1}, Label: "yes"}
	}
	samples := append(rs, ws...)
	for _, m := range []calibrate.Method{calibrate.MethodTemperature, calibrate.MethodPlatt} {
		p, err := calibrate.Fit(samples, calibrate.FitOptions{Method: m})
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		qp := p.Questions["q"]
		if qp.Temperature == nil && qp.Platt == nil {
			t.Fatalf("%s: no params", m)
		}
		if qp.Platt != nil && (math.IsNaN(qp.Platt.A) || math.IsNaN(qp.Platt.B)) {
			t.Errorf("platt NaN: %+v", qp.Platt)
		}
		if qp.Temperature != nil && (math.IsNaN(qp.Temperature.T) || math.IsInf(qp.Temperature.T, 0)) {
			t.Errorf("T not finite: %+v", qp.Temperature)
		}
	}
}

func TestFitRejectsInvalidSamples(t *testing.T) {
	ok := func() calibrate.Sample {
		return calibrate.Sample{Question: "q", Probs: map[string]float64{"yes": 0.6, "no": 0.4}, Label: "yes"}
	}
	for name, mut := range map[string]func(*calibrate.Sample){
		"NaN":           func(s *calibrate.Sample) { s.Probs["yes"] = math.NaN() },
		"negative":      func(s *calibrate.Sample) { s.Probs["yes"], s.Probs["no"] = -0.1, 1.1 },
		"sum != 1":      func(s *calibrate.Sample) { s.Probs["no"] = 0.9 },
		"all zero":      func(s *calibrate.Sample) { s.Probs["yes"], s.Probs["no"] = 0, 0 },
		"empty label":   func(s *calibrate.Sample) { s.Label = "" },
		"label missing": func(s *calibrate.Sample) { s.Label = "maybe" },
	} {
		bad := ok()
		bad.Probs = map[string]float64{"yes": 0.6, "no": 0.4}
		mut(&bad)
		if _, err := calibrate.FitTemperature([]calibrate.Sample{ok(), bad}); err == nil {
			t.Errorf("temperature %s: want error", name)
		}
		if name == "label missing" {
			continue // platt: any non-positive label is y=0 by contract
		}
		neg := ok()
		neg.Label = "no"
		if _, err := calibrate.FitPlatt([]calibrate.Sample{ok(), neg, bad}, "yes"); err == nil {
			t.Errorf("platt %s: want error", name)
		}
	}
}
