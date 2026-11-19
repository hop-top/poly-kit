package calibrate_test

import (
	"context"
	"testing"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/calibrate"
	"hop.top/kit/go/ai/judge/judgetest"
	"hop.top/kit/go/ai/llm"
)

// Grounding answers carry a Box and no Probs: calibration skips them, even
// when params name the question.
func TestGroundingSkipped(t *testing.T) {
	q := judge.Question{Name: "g", Kind: judge.KindGrounding, Instructions: "the mug in `image`"}
	c := judge.Case{
		Questions: []judge.Question{q},
		Assets:    []llm.ContentPart{{Type: llm.PartTypeImage, Source: llm.InlineSource([]byte("x"), "image/png")}},
	}
	want := judgetest.Grounding(judge.Box{Top: 0.1, Bottom: 0.6, Left: 0.2, Right: 0.7}, 0.8)
	p := calibrate.Params{Questions: map[string]calibrate.QuestionParams{
		"g": {Temperature: &calibrate.Temperature{T: 2}},
	}}

	got, err := p.Apply(q, want)
	if err != nil {
		t.Fatal(err)
	}
	if got.Box != want.Box || got.Confidence != want.Confidence || got.Value != "" {
		t.Fatalf("Apply changed a grounding answer: %+v", got)
	}

	inner := judgetest.Fixed(judge.Verdict{Answers: map[string]judge.Answer{"g": want}})
	v, err := calibrate.Calibrated(inner, p).Judge(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if a := v.Answers["g"]; a.Box != want.Box || a.Confidence != want.Confidence {
		t.Fatalf("Calibrated changed a grounding answer: %+v", a)
	}
}
