package judgetest_test

import (
	"context"
	"errors"
	"testing"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/judgetest"
)

func oneQ(name string) judge.Case {
	return judge.Case{Questions: []judge.Question{{Name: name, Kind: judge.KindYesNo}}}
}

func TestScriptedOrderAndLog(t *testing.T) {
	f := judgetest.Scripted(judge.Verdict{Model: "m1"}, judge.Verdict{Model: "m2"})
	ctx := context.Background()
	for _, want := range []string{"m1", "m2"} {
		v, err := f.Judge(ctx, oneQ("q-"+want))
		if err != nil || v.Model != want {
			t.Fatalf("got %q err %v, want %q", v.Model, err, want)
		}
	}
	if _, err := f.Judge(ctx, oneQ("x")); !errors.Is(err, judgetest.ErrScriptExhausted) {
		t.Fatalf("err = %v, want ErrScriptExhausted", err)
	}
	calls := f.Calls()
	if len(calls) != 3 || calls[0].Questions[0].Name != "q-m1" || calls[1].Questions[0].Name != "q-m2" {
		t.Fatalf("call log = %+v", calls)
	}
}

func TestScriptedError(t *testing.T) {
	boom := errors.New("boom")
	f := &judgetest.Fake{Script: []judgetest.Result{{Err: boom}}}
	if _, err := f.Judge(context.Background(), oneQ("q")); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestCancelledCtxNotLogged(t *testing.T) {
	f := judgetest.Scripted(judge.Verdict{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Judge(ctx, oneQ("q")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if len(f.Calls()) != 0 {
		t.Fatal("canceled call logged")
	}
	if _, err := f.Judge(context.Background(), oneQ("q")); err != nil {
		t.Fatalf("script entry consumed by canceled call: %v", err)
	}
}

func TestAnswerBuildersAreValid(t *testing.T) {
	cq := judge.Question{Name: "c", Kind: judge.KindChoice, Abstain: true,
		Options: []judge.Option{{Value: "a"}, {Value: "b"}}}
	sq := judge.Question{Name: "s", Kind: judge.KindScore, Anchors: []string{"l", "m", "h"}}
	yq := judge.Question{Name: "y", Kind: judge.KindYesNo}
	c := judge.Case{Questions: []judge.Question{cq, sq, yq}}
	v := judge.Verdict{Answers: map[string]judge.Answer{
		"c": judgetest.Choice(map[string]float64{"a": 0.7, "b": 0.2, "abstain": 0.1}, "a", "b", "abstain"),
		"s": judgetest.Score(map[string]float64{"0": 0.2, "1": 0.3, "2": 0.5}, 3),
		"y": judgetest.Choice(map[string]float64{"yes": 0.4, "no": 0.6}, "yes", "no"),
	}}
	if err := judge.ValidateVerdict(c, v); err != nil {
		t.Fatal(err)
	}
	if got := v.Answers["s"].Score; got < 0.65 || got > 0.651 {
		t.Fatalf("score = %v, want 0.65", got)
	}
	ab := judgetest.Abstain()
	if err := judge.ValidateAnswer(cq, ab); err != nil {
		t.Fatal(err)
	}
}

func TestGroundingBuildersAreValid(t *testing.T) {
	q := judge.Question{Name: "g", Kind: judge.KindGrounding, Abstain: true, Instructions: "the mug"}
	for name, a := range map[string]judge.Answer{
		"box":     judgetest.Grounding(judge.Box{Top: 0, Bottom: 1, Left: 0, Right: 0.5}, 0),
		"abstain": judgetest.GroundingAbstain(),
	} {
		if err := judge.ValidateAnswer(q, a); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
