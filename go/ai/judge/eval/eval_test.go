package eval_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/calibrate"
	"hop.top/kit/go/ai/judge/eval"
	"hop.top/kit/go/ai/judge/judgetest"
)

const tol = 1e-9

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func ynQuestion(abstain bool) judge.Question {
	return judge.Question{Name: "q", Kind: judge.KindYesNo, Abstain: abstain}
}

func ynCases(n int, abstain bool) []judge.Case {
	cs := make([]judge.Case, n)
	for i := range cs {
		cs[i] = judge.Case{
			State:     map[string]any{"i": i},
			Questions: []judge.Question{ynQuestion(abstain)},
		}
	}
	return cs
}

// byIndex answers question "q" of case i with as[i]; safe under parallelism.
func byIndex(as ...judge.Answer) *judgetest.Fake {
	return &judgetest.Fake{Fn: func(_ context.Context, c judge.Case) (judge.Verdict, error) {
		i := c.State["i"].(int)
		return judge.Verdict{Answers: map[string]judge.Answer{"q": as[i]}}, nil
	}}
}

// yn is a yes/no answer with P(yes)=py.
func yn(py float64) judge.Answer {
	return judgetest.Choice(map[string]float64{"yes": py, "no": 1 - py}, "yes", "no")
}

// ynAbs is a yes/no/abstain answer with the given probs.
func ynAbs(py, pn, pa float64) judge.Answer {
	return judgetest.Choice(map[string]float64{"yes": py, "no": pn, "abstain": pa}, "yes", "no", "abstain")
}

func entries(cs []judge.Case, labels ...string) []eval.Entry {
	es := make([]eval.Entry, len(cs))
	for i, c := range cs {
		es[i] = eval.Entry{Case: c}
		if labels[i] != "" {
			es[i].Reference = &eval.Reference{Labels: map[string]string{"q": labels[i]}}
		}
	}
	return es
}

func question(t *testing.T, r eval.Report, name string) eval.QuestionReport {
	t.Helper()
	for _, q := range r.Questions {
		if q.Name == name {
			return q
		}
	}
	t.Fatalf("report has no question %q: %+v", name, r.Questions)
	return eval.QuestionReport{}
}

func TestPerfectAgreement(t *testing.T) {
	cs := ynCases(3, false)
	ans := []judge.Answer{yn(1), yn(0), yn(1)}
	rep, err := eval.Compare(context.Background(), byIndex(ans...), byIndex(ans...),
		entries(cs, "", "", ""), eval.Options{Parallel: 2})
	if err != nil {
		t.Fatal(err)
	}
	if rep.N != 3 {
		t.Errorf("N = %d, want 3", rep.N)
	}
	q := question(t, rep, "q")
	near(t, "agreement", q.Agreement, 1)
	near(t, "brier", q.Brier, 0)
	near(t, "ece", q.ECE, 0)
	if q.N != 3 || q.Matches != 3 {
		t.Errorf("N/Matches = %d/%d, want 3/3", q.N, q.Matches)
	}
	if q.Confusion["yes"]["yes"] != 2 || q.Confusion["no"]["no"] != 1 {
		t.Errorf("confusion = %v", q.Confusion)
	}
}

func TestTotalDisagreement(t *testing.T) {
	cs := ynCases(2, false)
	rep, err := eval.Compare(context.Background(), nil, byIndex(yn(0), yn(0)),
		entries(cs, "yes", "yes"), eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	q := question(t, rep, "q")
	near(t, "agreement", q.Agreement, 0)
	near(t, "brier", q.Brier, 2) // (0-1)^2 + (1-0)^2, certain and wrong
	near(t, "ece", q.ECE, 1)     // confidence 1, accuracy 0
	if q.Confusion["yes"]["no"] != 2 {
		t.Errorf("confusion = %v", q.Confusion)
	}
}

// Hand-computed: labels yes,yes,no,no; cand P(yes) .95 .65 .75 .15.
//
//	brier = (.005 + .245 + 1.125 + .045) / 4 = .355
//	ece10 = (.05 + .35 + .75 + .15) / 4 = .325  (one case per bin)
//	ece2  = |.8 - .75| = .05                    (all four in the top bin)
func TestKnownBrierECE(t *testing.T) {
	cs := ynCases(4, false)
	cand := byIndex(yn(.95), yn(.65), yn(.75), yn(.15))
	es := entries(cs, "yes", "yes", "no", "no")

	rep, err := eval.Compare(context.Background(), nil, cand, es, eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	q := question(t, rep, "q")
	near(t, "agreement", q.Agreement, 0.75)
	near(t, "brier", q.Brier, 0.355)
	near(t, "ece", q.ECE, 0.325)
	if len(q.Bins) != 10 {
		t.Errorf("bins = %d, want default 10", len(q.Bins))
	}
	if q.NProbs != 4 {
		t.Errorf("NProbs = %d, want 4", q.NProbs)
	}

	rep, err = eval.Compare(context.Background(), nil, cand, es, eval.Options{Bins: 2})
	if err != nil {
		t.Fatal(err)
	}
	q = question(t, rep, "q")
	near(t, "ece2", q.ECE, 0.05)
	if len(q.Bins) != 2 || q.Bins[1].N != 4 {
		t.Errorf("bins = %+v", q.Bins)
	}
	near(t, "bin conf", q.Bins[1].Confidence, 0.8)
	near(t, "bin acc", q.Bins[1].Accuracy, 0.75)
}

func TestConfidenceOneLandsInTopBin(t *testing.T) {
	cs := ynCases(1, false)
	rep, err := eval.Compare(context.Background(), nil, byIndex(yn(1)), entries(cs, "yes"), eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	q := question(t, rep, "q")
	if q.Bins[9].N != 1 {
		t.Errorf("bins = %+v", q.Bins)
	}
}

func TestThresholdCoverage(t *testing.T) {
	cs := ynCases(4, false)
	cand := byIndex(yn(.95), yn(.65), yn(.75), yn(.15)) // conf .95 .65 .75 .85
	rep, err := eval.Compare(context.Background(), nil, cand,
		entries(cs, "yes", "yes", "no", "no"),
		eval.Options{Thresholds: []float64{0.7, 0.9}})
	if err != nil {
		t.Fatal(err)
	}
	q := question(t, rep, "q")
	if len(q.Thresholds) != 2 {
		t.Fatalf("thresholds = %+v", q.Thresholds)
	}
	a, b := q.Thresholds[0], q.Thresholds[1]
	if a.T != 0.7 || a.Covered != 3 {
		t.Errorf("t=.7: %+v", a)
	}
	near(t, "cov .7", a.Coverage, 0.75)
	near(t, "agree .7", a.Agreement, 2.0/3) // .95 right, .75 wrong, .85 right
	if b.Covered != 1 {
		t.Errorf("t=.9: %+v", b)
	}
	near(t, "cov .9", b.Coverage, 0.25)
	near(t, "agree .9", b.Agreement, 1)
}

func TestDefaultThresholds(t *testing.T) {
	rep, err := eval.Compare(context.Background(), nil, byIndex(yn(1)),
		entries(ynCases(1, false), "yes"), eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(question(t, rep, "q").Thresholds); n != len(eval.DefaultThresholds) || n == 0 {
		t.Errorf("thresholds = %d, want %d", n, len(eval.DefaultThresholds))
	}
}

func TestAbstainEitherSide(t *testing.T) {
	cs := ynCases(4, true)
	cand := byIndex(
		judgetest.Abstain(), // ref abstains, cand abstains: match
		yn(.9),              // ref abstains, cand answers: miss
		judgetest.Abstain(), // ref answers, cand abstains: miss
		yn(.9),              // both yes: match
	)
	rep, err := eval.Compare(context.Background(), nil, cand,
		entries(cs, "abstain", "abstain", "yes", "yes"),
		eval.Options{Thresholds: []float64{0.5}})
	if err != nil {
		t.Fatal(err)
	}
	q := question(t, rep, "q")
	near(t, "agreement", q.Agreement, 0.5)
	if q.RefAbstain != 2 || q.CandAbstain != 2 {
		t.Errorf("abstain ref/cand = %d/%d, want 2/2", q.RefAbstain, q.CandAbstain)
	}
	if q.NAnswered != 1 {
		t.Errorf("NAnswered = %d, want 1 (both answered)", q.NAnswered)
	}
	near(t, "agreement answered", q.AgreementAnswered, 1)
	// abstained cand answers are never covered
	th := q.Thresholds[0]
	if th.Covered != 2 {
		t.Errorf("covered = %d, want 2", th.Covered)
	}
	near(t, "cov", th.Coverage, 0.5)
	near(t, "agree", th.Agreement, 0.5)
	if q.Confusion["abstain"]["abstain"] != 1 || q.Confusion["abstain"]["yes"] != 1 || q.Confusion["yes"]["abstain"] != 1 {
		t.Errorf("confusion = %v", q.Confusion)
	}
}

func TestAbstainMassCountsInBrier(t *testing.T) {
	cs := ynCases(1, true)
	// ref label abstain; cand puts .5 on it: (0-0)^2 + (0-0)^2... labels
	// yes=.25,no=.25,abstain=.5 vs one-hot abstain: .0625+.0625+.25 = .375
	rep, err := eval.Compare(context.Background(), nil, byIndex(ynAbs(.25, .25, .5)),
		entries(cs, "abstain"), eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "brier", question(t, rep, "q").Brier, 0.375)
}

func scoreCase(i int) judge.Case {
	return judge.Case{
		State: map[string]any{"i": i},
		Questions: []judge.Question{{
			Name: "s", Kind: judge.KindScore, Abstain: true,
			Anchors: []string{"bad", "ok", "good"},
		}},
	}
}

func scoreFake(as ...judge.Answer) *judgetest.Fake {
	return &judgetest.Fake{Fn: func(_ context.Context, c judge.Case) (judge.Verdict, error) {
		return judge.Verdict{Answers: map[string]judge.Answer{"s": as[c.State["i"].(int)]}}, nil
	}}
}

func TestScoreMAE(t *testing.T) {
	cs := []judge.Case{scoreCase(0), scoreCase(1), scoreCase(2)}
	ref := scoreFake(
		judgetest.Score(map[string]float64{"0": .5, "2": .5}, 3), // score .5
		judgetest.Score(map[string]float64{"2": 1}, 3),           // score 1
		judgetest.Abstain(), // excluded
	)
	cand := scoreFake(
		judgetest.Score(map[string]float64{"1": 1}, 3), // .5 -> err 0
		judgetest.Score(map[string]float64{"0": 1}, 3), // 0  -> err 1
		judgetest.Score(map[string]float64{"1": 1}, 3), // ref abstained: excluded
	)
	es := make([]eval.Entry, len(cs))
	for i, c := range cs {
		es[i] = eval.Entry{Case: c}
	}
	rep, err := eval.Compare(context.Background(), ref, cand, es, eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	q := question(t, rep, "s")
	if q.NScore != 2 {
		t.Errorf("NScore = %d, want 2", q.NScore)
	}
	near(t, "mae", q.ScoreMAE, 0.5)
}

func TestScoreMAEFromLabel(t *testing.T) {
	// human label "2" of 3 anchors scales to 1.0; cand score .5 -> err .5
	cs := []judge.Case{scoreCase(0)}
	es := []eval.Entry{{Case: cs[0], Reference: &eval.Reference{Labels: map[string]string{"s": "2"}}}}
	rep, err := eval.Compare(context.Background(), nil,
		scoreFake(judgetest.Score(map[string]float64{"1": 1}, 3)), es, eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	q := question(t, rep, "s")
	near(t, "mae", q.ScoreMAE, 0.5)
	near(t, "agreement", q.Agreement, 0) // "1" != "2"
}

func TestNoScoreForNonScoreKind(t *testing.T) {
	rep, err := eval.Compare(context.Background(), nil, byIndex(yn(1)),
		entries(ynCases(1, false), "yes"), eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if q := question(t, rep, "q"); q.NScore != 0 || q.ScoreMAE != 0 {
		t.Errorf("score stats on yes/no: %+v", q)
	}
}

func TestEmptySet(t *testing.T) {
	rep, err := eval.Compare(context.Background(), byIndex(), byIndex(), nil, eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.N != 0 || len(rep.Questions) != 0 {
		t.Errorf("report = %+v", rep)
	}
	if _, err := json.Marshal(rep); err != nil {
		t.Errorf("marshal empty report: %v", err)
	}
	ok, reasons := rep.Pass(eval.Thresholds{MinAgreement: 0.9})
	if ok || len(reasons) == 0 {
		t.Errorf("empty report passed gate: %v %v", ok, reasons)
	}
}

func TestMismatchedQuestions(t *testing.T) {
	cs := ynCases(1, false)
	t.Run("ref verdict lacks question", func(t *testing.T) {
		es := []eval.Entry{{Case: cs[0], Reference: &eval.Reference{
			Verdict: &judge.Verdict{Answers: map[string]judge.Answer{}},
		}}}
		_, err := eval.Compare(context.Background(), nil, byIndex(yn(1)), es, eval.Options{})
		if !errors.Is(err, eval.ErrMismatch) {
			t.Errorf("err = %v, want ErrMismatch", err)
		}
	})
	t.Run("ref label for unknown question", func(t *testing.T) {
		es := []eval.Entry{{Case: cs[0], Reference: &eval.Reference{
			Labels: map[string]string{"q": "yes", "zzz": "yes"},
		}}}
		_, err := eval.Compare(context.Background(), nil, byIndex(yn(1)), es, eval.Options{})
		if !errors.Is(err, eval.ErrMismatch) {
			t.Errorf("err = %v, want ErrMismatch", err)
		}
	})
	t.Run("ref verdict answers extra question", func(t *testing.T) {
		es := []eval.Entry{{Case: cs[0], Reference: &eval.Reference{
			Verdict: &judge.Verdict{Answers: map[string]judge.Answer{"q": yn(1), "other": yn(1)}},
		}}}
		_, err := eval.Compare(context.Background(), nil, byIndex(yn(1)), es, eval.Options{})
		if !errors.Is(err, eval.ErrMismatch) {
			t.Errorf("err = %v, want ErrMismatch", err)
		}
	})
	t.Run("label not valid for question", func(t *testing.T) {
		_, err := eval.Compare(context.Background(), nil, byIndex(yn(1)), entries(cs, "maybe"), eval.Options{})
		if !errors.Is(err, eval.ErrMismatch) {
			t.Errorf("err = %v, want ErrMismatch", err)
		}
	})
	t.Run("cand answers wrong question", func(t *testing.T) {
		bad := &judgetest.Fake{Fn: func(context.Context, judge.Case) (judge.Verdict, error) {
			return judge.Verdict{Answers: map[string]judge.Answer{"nope": yn(1)}}, nil
		}}
		_, err := eval.Compare(context.Background(), nil, bad, entries(cs, "yes"), eval.Options{})
		if !errors.Is(err, eval.ErrMismatch) {
			t.Errorf("err = %v, want ErrMismatch", err)
		}
	})
	t.Run("same name different kind across cases", func(t *testing.T) {
		a := judge.Case{State: map[string]any{"i": 0}, Questions: []judge.Question{ynQuestion(false)}}
		b := judge.Case{State: map[string]any{"i": 1}, Questions: []judge.Question{{
			Name: "q", Kind: judge.KindScore, Anchors: []string{"x", "y"},
		}}}
		es := []eval.Entry{
			{Case: a, Reference: &eval.Reference{Labels: map[string]string{"q": "yes"}}},
			{Case: b, Reference: &eval.Reference{Labels: map[string]string{"q": "1"}}},
		}
		cand := &judgetest.Fake{Fn: func(_ context.Context, c judge.Case) (judge.Verdict, error) {
			if c.State["i"].(int) == 0 {
				return judge.Verdict{Answers: map[string]judge.Answer{"q": yn(1)}}, nil
			}
			return judge.Verdict{Answers: map[string]judge.Answer{"q": judgetest.Score(map[string]float64{"1": 1}, 2)}}, nil
		}}
		_, err := eval.Compare(context.Background(), nil, cand, es, eval.Options{})
		if !errors.Is(err, eval.ErrMismatch) {
			t.Errorf("err = %v, want ErrMismatch", err)
		}
	})
}

func TestNoReference(t *testing.T) {
	_, err := eval.Compare(context.Background(), nil, byIndex(yn(1)),
		entries(ynCases(1, false), ""), eval.Options{})
	if !errors.Is(err, eval.ErrNoReference) {
		t.Errorf("err = %v, want ErrNoReference", err)
	}
}

func TestRecordedReferenceBeatsRefJudge(t *testing.T) {
	cs := ynCases(2, false)
	es := entries(cs, "yes", "") // second case has no recorded reference
	ref := byIndex(yn(1), yn(0)) // would say yes, no
	rep, err := eval.Compare(context.Background(), ref, byIndex(yn(1), yn(0)), es, eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ref.Calls()); n != 1 {
		t.Errorf("ref judge calls = %d, want 1 (only the unrecorded case)", n)
	}
	near(t, "agreement", question(t, rep, "q").Agreement, 1)
}

func TestRecordedVerdictReference(t *testing.T) {
	cs := ynCases(1, false)
	v := judge.Verdict{Answers: map[string]judge.Answer{"q": yn(.8)}}
	es := []eval.Entry{{Case: cs[0], Reference: &eval.Reference{Verdict: &v}}}
	rep, err := eval.Compare(context.Background(), nil, byIndex(yn(.9)), es, eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "agreement", question(t, rep, "q").Agreement, 1)
}

func TestLabelsOverrideRecordedVerdict(t *testing.T) {
	cs := ynCases(1, false)
	v := judge.Verdict{Answers: map[string]judge.Answer{"q": yn(1)}} // recorded: yes
	es := []eval.Entry{{Case: cs[0], Reference: &eval.Reference{
		Verdict: &v, Labels: map[string]string{"q": "no"}, // human says no
	}}}
	rep, err := eval.Compare(context.Background(), nil, byIndex(yn(0)), es, eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "agreement", question(t, rep, "q").Agreement, 1)
}

func TestCandUsesBatchJudge(t *testing.T) {
	cs := ynCases(3, false)
	bf := judgetest.NewBatch(byIndex(yn(1), yn(1), yn(1)))
	_, err := eval.Compare(context.Background(), nil, bf, entries(cs, "yes", "yes", "yes"), eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := bf.Batches(); len(got) != 1 || got[0] != 3 {
		t.Errorf("batches = %v, want [3]", got)
	}
}

func TestCandErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	bad := &judgetest.Fake{Fn: func(context.Context, judge.Case) (judge.Verdict, error) {
		return judge.Verdict{}, boom
	}}
	_, err := eval.Compare(context.Background(), nil, bad, entries(ynCases(2, false), "yes", "yes"), eval.Options{})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestCandNilIsError(t *testing.T) {
	if _, err := eval.Compare(context.Background(), nil, nil, entries(ynCases(1, false), "yes"), eval.Options{}); err == nil {
		t.Error("nil candidate: want error")
	}
}

func TestNoProbsSkipsCalibrationMetrics(t *testing.T) {
	cs := ynCases(2, false)
	noProbs := judge.Answer{Value: "yes", Confidence: 0.9, Source: "verbalized"}
	rep, err := eval.Compare(context.Background(), nil, byIndex(noProbs, noProbs),
		entries(cs, "yes", "no"), eval.Options{Thresholds: []float64{0.5}})
	if err != nil {
		t.Fatal(err)
	}
	q := question(t, rep, "q")
	if q.NProbs != 0 || q.Brier != 0 || q.ECE != 0 {
		t.Errorf("calibration metrics without probs: %+v", q)
	}
	if len(rep.Samples) != 0 {
		t.Errorf("samples = %d, want 0", len(rep.Samples))
	}
	near(t, "agreement", q.Agreement, 0.5)
	if q.Thresholds[0].Covered != 2 {
		t.Errorf("confidence still gates coverage: %+v", q.Thresholds[0])
	}
}

func TestSamplesFeedCalibrateFit(t *testing.T) {
	cs := ynCases(4, false)
	rep, err := eval.Compare(context.Background(), nil,
		byIndex(yn(.95), yn(.65), yn(.75), yn(.15)),
		entries(cs, "yes", "yes", "no", "no"), eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Samples) != 4 {
		t.Fatalf("samples = %d, want 4", len(rep.Samples))
	}
	s := rep.Samples[0]
	if s.Question != "q" || s.Label != "yes" || math.Abs(s.Probs["yes"]-.95) > tol {
		t.Errorf("sample[0] = %+v", s)
	}
	params, err := calibrate.Fit(rep.Samples, calibrate.FitOptions{})
	if err != nil {
		t.Fatalf("calibrate.Fit: %v", err)
	}
	if _, ok := params.Questions["q"]; !ok {
		t.Errorf("params = %+v", params)
	}
}

func TestSamplesWithConfidentWrongFeedCalibrateFit(t *testing.T) {
	cs := ynCases(4, false)
	rep, err := eval.Compare(context.Background(), nil,
		byIndex(yn(.95), yn(.7), yn(0), yn(.2)), // case 2: sure "no"; reference says "yes"
		entries(cs, "yes", "yes", "yes", "no"), eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Samples[2].Probs["yes"]; got != 0 {
		t.Fatalf("setup: P(true label) = %v, want 0", got)
	}
	for _, m := range []calibrate.Method{calibrate.MethodTemperature, calibrate.MethodPlatt} {
		params, err := calibrate.Fit(rep.Samples, calibrate.FitOptions{Method: m})
		if err != nil {
			t.Fatalf("%s: calibrate.Fit with confident-wrong sample: %v", m, err)
		}
		if _, ok := params.Questions["q"]; !ok {
			t.Errorf("%s: params = %+v", m, params)
		}
	}
}

func TestParallelDeterministic(t *testing.T) {
	n := 40
	cs := ynCases(n, false)
	as := make([]judge.Answer, n)
	labels := make([]string, n)
	for i := range as {
		as[i], labels[i] = yn(.9), "yes"
		if i%4 == 0 {
			as[i] = yn(.1)
		}
	}
	rep, err := eval.Compare(context.Background(), nil, byIndex(as...), entries(cs, labels...), eval.Options{Parallel: 8})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "agreement", question(t, rep, "q").Agreement, 0.75)
	for i, s := range rep.Samples { // sample order follows case order
		if want := as[i].Probs["yes"]; math.Abs(s.Probs["yes"]-want) > tol {
			t.Fatalf("sample %d out of order", i)
		}
	}
}

func TestQuestionOrderIsFirstAppearance(t *testing.T) {
	c := judge.Case{State: map[string]any{"i": 0}, Questions: []judge.Question{
		{Name: "zeta", Kind: judge.KindYesNo}, {Name: "alpha", Kind: judge.KindYesNo},
	}}
	cand := &judgetest.Fake{Fn: func(context.Context, judge.Case) (judge.Verdict, error) {
		return judge.Verdict{Answers: map[string]judge.Answer{"zeta": yn(1), "alpha": yn(1)}}, nil
	}}
	es := []eval.Entry{{Case: c, Reference: &eval.Reference{Labels: map[string]string{"zeta": "yes", "alpha": "yes"}}}}
	rep, err := eval.Compare(context.Background(), nil, cand, es, eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Questions) != 2 || rep.Questions[0].Name != "zeta" {
		t.Errorf("order = %+v", rep.Questions)
	}
}

func TestPassReasons(t *testing.T) {
	cs := ynCases(4, false)
	rep, err := eval.Compare(context.Background(), nil,
		byIndex(yn(.95), yn(.65), yn(.75), yn(.15)),
		entries(cs, "yes", "yes", "no", "no"),
		eval.Options{Thresholds: []float64{0.7, 0.9}})
	if err != nil {
		t.Fatal(err)
	}
	// at conf>=.7: coverage .75, agreement .667; ece .325; brier .355; n 4.
	t.Run("all pass", func(t *testing.T) {
		ok, reasons := rep.Pass(eval.Thresholds{
			AtConfidence: 0.7, MinAgreement: 0.6, MinCoverage: 0.7,
			MaxECE: 0.4, MaxBrier: 0.4, MinN: 4,
		})
		if !ok || len(reasons) != 0 {
			t.Errorf("ok=%v reasons=%v", ok, reasons)
		}
	})
	t.Run("zero thresholds pass", func(t *testing.T) {
		if ok, reasons := rep.Pass(eval.Thresholds{}); !ok || len(reasons) != 0 {
			t.Errorf("ok=%v reasons=%v", ok, reasons)
		}
	})
	t.Run("each failure reasoned", func(t *testing.T) {
		ok, reasons := rep.Pass(eval.Thresholds{
			AtConfidence: 0.7, MinAgreement: 0.9, MinCoverage: 0.9,
			MaxECE: 0.1, MaxBrier: 0.1, MinN: 10,
		})
		if ok {
			t.Fatal("passed")
		}
		if len(reasons) != 5 {
			t.Fatalf("reasons = %q, want 5", reasons)
		}
		joined := strings.Join(reasons, "\n")
		for _, want := range []string{"agreement", "coverage", "ece", "brier", "n "} {
			if !strings.Contains(strings.ToLower(joined), want) {
				t.Errorf("reasons lack %q:\n%s", want, joined)
			}
		}
		for _, r := range reasons {
			if !strings.HasPrefix(r, "q: ") {
				t.Errorf("reason %q lacks question prefix", r)
			}
		}
	})
	t.Run("agreement without confidence uses overall", func(t *testing.T) {
		ok, reasons := rep.Pass(eval.Thresholds{MinAgreement: 0.8}) // overall .75
		if ok || len(reasons) != 1 {
			t.Errorf("ok=%v reasons=%v", ok, reasons)
		}
	})
	t.Run("confidence not computed", func(t *testing.T) {
		ok, reasons := rep.Pass(eval.Thresholds{AtConfidence: 0.55, MinAgreement: 0.1})
		if ok || len(reasons) != 1 || !strings.Contains(reasons[0], "0.55") {
			t.Errorf("ok=%v reasons=%v", ok, reasons)
		}
	})
	t.Run("ece needs probs", func(t *testing.T) {
		noProbs := judge.Answer{Value: "yes", Confidence: 0.9}
		r, err := eval.Compare(context.Background(), nil, byIndex(noProbs),
			entries(ynCases(1, false), "yes"), eval.Options{})
		if err != nil {
			t.Fatal(err)
		}
		ok, reasons := r.Pass(eval.Thresholds{MaxECE: 0.1})
		if ok || len(reasons) != 1 || !strings.Contains(reasons[0], "probabilities") {
			t.Errorf("ok=%v reasons=%v", ok, reasons)
		}
	})
	t.Run("no covered cases", func(t *testing.T) {
		r, err := eval.Compare(context.Background(), nil, byIndex(yn(.6)),
			entries(ynCases(1, false), "yes"), eval.Options{Thresholds: []float64{0.9}})
		if err != nil {
			t.Fatal(err)
		}
		ok, reasons := r.Pass(eval.Thresholds{AtConfidence: 0.9, MinAgreement: 0.5})
		if ok || len(reasons) != 1 || !strings.Contains(reasons[0], "no covered") {
			t.Errorf("ok=%v reasons=%v", ok, reasons)
		}
	})
	t.Run("coverage at zero confidence is answered share", func(t *testing.T) {
		r, err := eval.Compare(context.Background(), nil, byIndex(judgetest.Abstain(), yn(1)),
			entries(ynCases(2, true), "yes", "yes"), eval.Options{})
		if err != nil {
			t.Fatal(err)
		}
		ok, reasons := r.Pass(eval.Thresholds{MinCoverage: 0.75})
		if ok || len(reasons) != 1 {
			t.Errorf("ok=%v reasons=%v", ok, reasons)
		}
	})
}

func TestMarkdownAndJSON(t *testing.T) {
	cs := ynCases(4, false)
	rep, err := eval.Compare(context.Background(), nil,
		byIndex(yn(.95), yn(.65), yn(.75), yn(.15)),
		entries(cs, "yes", "yes", "no", "no"),
		eval.Options{Thresholds: []float64{0.7}})
	if err != nil {
		t.Fatal(err)
	}
	md := rep.Markdown()
	for _, want := range []string{"| question |", "| q ", "0.750", "0.355", "0.325", "0.70"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if rep.String() != md {
		t.Error("String != Markdown")
	}
	b, err := rep.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var back eval.Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.N != 4 || len(back.Questions) != 1 {
		t.Fatalf("back = %+v", back)
	}
	near(t, "brier", back.Questions[0].Brier, 0.355)
	if len(back.Samples) != 4 {
		t.Errorf("samples lost in JSON: %d", len(back.Samples))
	}
}
