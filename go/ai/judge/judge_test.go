package judge_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"hop.top/kit/go/ai/judge"
)

func choiceQ(abstain bool) judge.Question {
	return judge.Question{Name: "label", Kind: judge.KindChoice, Abstain: abstain,
		Options: []judge.Option{{Value: "a"}, {Value: "b"}, {Value: "c"}}}
}

func scoreQ(abstain bool) judge.Question {
	return judge.Question{Name: "imp", Kind: judge.KindScore, Abstain: abstain,
		Anchors: []string{"none", "low", "mid", "high", "critical"}}
}

func TestValidateCase(t *testing.T) {
	ynq := judge.Question{Name: "ok", Kind: judge.KindYesNo}
	tests := []struct {
		name string
		c    judge.Case
		want string // substring of error; "" = valid
	}{
		{"valid all kinds", judge.Case{Questions: []judge.Question{choiceQ(true), scoreQ(false), ynq}}, ""},
		{"no questions", judge.Case{}, "no questions"},
		{"empty name", judge.Case{Questions: []judge.Question{{Kind: judge.KindYesNo}}}, "empty name"},
		{"duplicate name", judge.Case{Questions: []judge.Question{ynq, ynq}}, "duplicate name"},
		{"unknown kind", judge.Case{Questions: []judge.Question{{Name: "x", Kind: "box"}}}, "unknown kind"},
		{"choice one option", judge.Case{Questions: []judge.Question{{Name: "x", Kind: judge.KindChoice, Options: []judge.Option{{Value: "a"}}}}}, ">= 2 options"},
		{"choice dup option", judge.Case{Questions: []judge.Question{{Name: "x", Kind: judge.KindChoice, Options: []judge.Option{{Value: "a"}, {Value: "a"}}}}}, "duplicate option"},
		{"choice empty option", judge.Case{Questions: []judge.Question{{Name: "x", Kind: judge.KindChoice, Options: []judge.Option{{Value: "a"}, {Value: " "}}}}}, "empty option"},
		{"choice reserved abstain", judge.Case{Questions: []judge.Question{{Name: "x", Kind: judge.KindChoice, Options: []judge.Option{{Value: "a"}, {Value: "abstain"}}}}}, "reserved"},
		{"choice with anchors", judge.Case{Questions: []judge.Question{{Name: "x", Kind: judge.KindChoice, Anchors: []string{"p"}, Options: []judge.Option{{Value: "a"}, {Value: "b"}}}}}, "no anchors"},
		{"score one anchor", judge.Case{Questions: []judge.Question{{Name: "x", Kind: judge.KindScore, Anchors: []string{"p"}}}}, ">= 2 anchors"},
		{"score with options", judge.Case{Questions: []judge.Question{{Name: "x", Kind: judge.KindScore, Anchors: []string{"p", "q"}, Options: []judge.Option{{Value: "a"}}}}}, "no options"},
		{"yesno with options", judge.Case{Questions: []judge.Question{{Name: "x", Kind: judge.KindYesNo, Options: []judge.Option{{Value: "a"}}}}}, "no options or anchors"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := judge.Validate(tc.c)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
			if !errors.Is(err, judge.ErrInvalid) {
				t.Fatalf("err %v does not wrap ErrInvalid", err)
			}
		})
	}
}

func TestLabels(t *testing.T) {
	got := strings.Join(judge.Labels(choiceQ(true)), ",")
	if got != "a,b,c,abstain" {
		t.Fatalf("choice labels = %s", got)
	}
	if got := strings.Join(judge.Labels(judge.Question{Kind: judge.KindYesNo}), ","); got != "yes,no" {
		t.Fatalf("yesno labels = %s", got)
	}
	if got := strings.Join(judge.Labels(scoreQ(false)), ","); got != "0,1,2,3,4" {
		t.Fatalf("score labels = %s", got)
	}
}

func TestValidateAnswer(t *testing.T) {
	good := judge.Answer{Value: "a", Confidence: 0.6, Probs: map[string]float64{"a": 0.6, "b": 0.3, "c": 0.1}}
	tests := []struct {
		name string
		q    judge.Question
		a    judge.Answer
		want string
	}{
		{"valid", choiceQ(false), good, ""},
		{"valid no probs", choiceQ(false), judge.Answer{Value: "b", Confidence: 0.7}, ""},
		{"probs sum low", choiceQ(false), judge.Answer{Value: "a", Confidence: 0.6, Probs: map[string]float64{"a": 0.6, "b": 0.3, "c": 0.05}}, "probs sum"},
		{"probs sum high", choiceQ(false), judge.Answer{Value: "a", Confidence: 0.6, Probs: map[string]float64{"a": 0.6, "b": 0.3, "c": 0.2}}, "probs sum"},
		{"probs within epsilon", choiceQ(false), judge.Answer{Value: "a", Confidence: 0.6, Probs: map[string]float64{"a": 0.6, "b": 0.3, "c": 0.1 + 1e-9}}, ""},
		{"probs bad key", choiceQ(false), judge.Answer{Value: "a", Confidence: 0.6, Probs: map[string]float64{"a": 0.6, "z": 0.4}}, "not a valid label"},
		{"prob negative", choiceQ(false), judge.Answer{Value: "a", Confidence: 1.2, Probs: map[string]float64{"a": 1.2, "b": -0.2}}, "outside [0,1]"},
		{"value not label", choiceQ(false), judge.Answer{Value: "z", Confidence: 0.5}, "not a valid label"},
		{"confidence != P(value)", choiceQ(false), judge.Answer{Value: "a", Confidence: 0.9, Probs: good.Probs}, "confidence"},
		{"confidence range", choiceQ(false), judge.Answer{Value: "a", Confidence: 1.5}, "outside [0,1]"},
		{"abstain disallowed", choiceQ(false), judge.Answer{Value: "abstain", Abstained: true, Confidence: 1}, "abstain not allowed"},
		{"abstain allowed", choiceQ(true), judge.Answer{Value: "abstain", Abstained: true, Confidence: 1, Probs: map[string]float64{"abstain": 1}}, ""},
		{"abstained flag without value", choiceQ(true), judge.Answer{Value: "a", Abstained: true, Confidence: 0.5}, "Abstained=true"},
		{"abstain value without flag", choiceQ(true), judge.Answer{Value: "abstain", Confidence: 1}, "Abstained=false"},
		{"score ev ok", scoreQ(false), judge.Answer{Value: "2", Confidence: 0.4, Score: 0.5,
			Probs: map[string]float64{"0": 0.1, "1": 0.2, "2": 0.4, "3": 0.2, "4": 0.1}}, ""},
		{"score ev wrong", scoreQ(false), judge.Answer{Value: "2", Confidence: 0.4, Score: 0.9,
			Probs: map[string]float64{"0": 0.1, "1": 0.2, "2": 0.4, "3": 0.2, "4": 0.1}}, "expected value"},
		{"score out of range", scoreQ(false), judge.Answer{Value: "2", Confidence: 0.4, Score: 2}, "outside [0,1]"},
		{"yesno ok", judge.Question{Name: "q", Kind: judge.KindYesNo}, judge.Answer{Value: "yes", Confidence: 0.8, Probs: map[string]float64{"yes": 0.8, "no": 0.2}}, ""},
		{"yesno maybe", judge.Question{Name: "q", Kind: judge.KindYesNo}, judge.Answer{Value: "maybe", Confidence: 0.8}, "not a valid label"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := judge.ValidateAnswer(tc.q, tc.a)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateVerdict(t *testing.T) {
	c := judge.Case{Questions: []judge.Question{choiceQ(false)}}
	ok := judge.Verdict{Answers: map[string]judge.Answer{"label": {Value: "a", Confidence: 1}}}
	if err := judge.ValidateVerdict(c, ok); err != nil {
		t.Fatal(err)
	}
	if err := judge.ValidateVerdict(c, judge.Verdict{}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing answer: %v", err)
	}
	extra := judge.Verdict{Answers: map[string]judge.Answer{"label": ok.Answers["label"], "zzz": {}}}
	if err := judge.ValidateVerdict(c, extra); err == nil || !strings.Contains(err.Error(), "no such question") {
		t.Fatalf("extra answer: %v", err)
	}
}

// Vectors are decide.score_value outputs (ANCHORS=5), computed by running
// the python function.
func TestScoreEVParityDecidePy(t *testing.T) {
	const n = 5
	probVecs := []struct {
		probs map[string]float64
		want  float64
	}{
		{map[string]float64{"0": .1, "1": .2, "2": .4, "3": .2, "4": .1}, 0.5},
		{map[string]float64{"0": 0, "1": 0, "2": 0, "3": 0, "4": 1}, 1.0},
		{map[string]float64{"0": 1, "1": 0, "2": 0, "3": 0, "4": 0}, 0.0},
		{map[string]float64{"0": .05, "1": .15, "2": .1, "3": .3, "4": .4}, 0.7125},
		{map[string]float64{"3": .5, "4": .5}, 0.875},
		{nil, 0.0},
	}
	for i, v := range probVecs {
		if got := judge.ScoreEV(v.probs, n); math.Abs(got-v.want) > 1e-12 {
			t.Errorf("prob vec %d: ScoreEV = %v, want %v", i, got, v.want)
		}
	}
	idxVecs := []struct{ idx, want float64 }{{3, .75}, {4, 1}, {9, 1}, {0, 0}, {1.5, .375}}
	for _, v := range idxVecs {
		if got := judge.ScoreFromIndex(v.idx, n); math.Abs(got-v.want) > 1e-12 {
			t.Errorf("ScoreFromIndex(%v) = %v, want %v", v.idx, got, v.want)
		}
	}
}

func TestScoreEVAbstainExcluded(t *testing.T) {
	// 0.5 abstain; remaining mass split evenly between anchors 3 and 4.
	p := map[string]float64{"3": .25, "4": .25, "abstain": .5}
	if got := judge.ScoreEV(p, 5); math.Abs(got-0.875) > 1e-12 {
		t.Fatalf("ScoreEV = %v, want 0.875", got)
	}
	if got := judge.ScoreEV(map[string]float64{"abstain": 1}, 5); got != 0 {
		t.Fatalf("all-abstain ScoreEV = %v, want 0", got)
	}
}
