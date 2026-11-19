package eval_test

import (
	"context"
	"strings"
	"testing"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/eval"
	"hop.top/kit/go/ai/judge/judgetest"
	"hop.top/kit/go/ai/llm"
)

var refBox = judge.Box{Top: 0, Bottom: 0.5, Left: 0, Right: 0.5}

func groundCases(n int, abstain bool) []judge.Case {
	cs := make([]judge.Case, n)
	for i := range cs {
		cs[i] = judge.Case{
			State: map[string]any{"i": i},
			Questions: []judge.Question{{
				Name: "q", Kind: judge.KindGrounding, Abstain: abstain, Instructions: "the mug in `image`",
			}},
			Assets: []llm.ContentPart{{Type: llm.PartTypeImage, Source: llm.InlineSource([]byte("x"), "image/png")}},
		}
	}
	return cs
}

func boxEntries(cs []judge.Case, refs ...*judge.Box) []eval.Entry {
	es := make([]eval.Entry, len(cs))
	for i, c := range cs {
		es[i] = eval.Entry{Case: c, Reference: &eval.Reference{Boxes: map[string]judge.Box{}}}
		if refs[i] != nil {
			es[i].Reference.Boxes["q"] = *refs[i]
		} else {
			es[i].Reference.Labels = map[string]string{"q": judge.LabelAbstain}
		}
	}
	return es
}

func TestGroundingIoUAgreement(t *testing.T) {
	cs := groundCases(4, true)
	r := &refBox
	cand := byIndex(
		judgetest.Grounding(refBox, 0.9),                                                // iou 1: match
		judgetest.Grounding(judge.Box{Top: 0, Bottom: 0.5, Left: 0.25, Right: 0.75}, 0), // iou 1/3: miss
		judgetest.Grounding(judge.Box{Top: 0, Bottom: 0.5, Left: 0.1, Right: 0.5}, 0.4), // iou 0.8: match
		judgetest.GroundingAbstain(),
	)
	rep, err := eval.Compare(context.Background(), nil, cand, boxEntries(cs, r, r, r, nil), eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	q := question(t, rep, "q")
	if q.N != 4 || q.Matches != 3 {
		t.Fatalf("N=%d matches=%d, want 4 and 3", q.N, q.Matches)
	}
	near(t, "agreement", q.Agreement, 0.75)
	if q.NIoU != 3 {
		t.Fatalf("NIoU = %d, want 3 (both answered)", q.NIoU)
	}
	near(t, "mean iou", q.MeanIoU, (1+1.0/3+0.8)/3)
	if q.RefAbstain != 1 || q.CandAbstain != 1 {
		t.Errorf("abstain ref/cand = %d/%d", q.RefAbstain, q.CandAbstain)
	}
}

func TestGroundingAbstainMatchesAbstain(t *testing.T) {
	cs := groundCases(2, true)
	cand := byIndex(judgetest.GroundingAbstain(), judgetest.Grounding(refBox, 0))
	rep, err := eval.Compare(context.Background(), nil, cand, boxEntries(cs, nil, nil), eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	q := question(t, rep, "q")
	if q.Matches != 1 || q.NIoU != 0 {
		t.Fatalf("matches=%d niou=%d, want 1 and 0", q.Matches, q.NIoU)
	}
}

func TestGroundingIoUThreshold(t *testing.T) {
	cs := groundCases(1, false)
	loose := judgetest.Grounding(judge.Box{Top: 0, Bottom: 0.5, Left: 0.25, Right: 0.75}, 0) // iou 1/3
	for _, tc := range []struct {
		iou  float64
		want int
	}{{0, 0}, {0.3, 1}, {0.5, 0}, {1.0 / 3, 1}} {
		rep, err := eval.Compare(context.Background(), nil, byIndex(loose), boxEntries(cs, &refBox), eval.Options{IoU: tc.iou})
		if err != nil {
			t.Fatal(err)
		}
		if got := question(t, rep, "q").Matches; got != tc.want {
			t.Errorf("IoU=%v matches=%d, want %d", tc.iou, got, tc.want)
		}
	}
	if _, err := eval.Compare(context.Background(), nil, byIndex(loose), boxEntries(cs, &refBox), eval.Options{IoU: 1.5}); err == nil {
		t.Error("IoU 1.5 accepted")
	}
}

func TestGroundingRecordedVerdictReference(t *testing.T) {
	cs := groundCases(1, false)
	v := judge.Verdict{Answers: map[string]judge.Answer{"q": judgetest.Grounding(refBox, 0.7)}}
	es := []eval.Entry{{Case: cs[0], Reference: &eval.Reference{Verdict: &v}}}
	rep, err := eval.Compare(context.Background(), nil, byIndex(judgetest.Grounding(refBox, 0.6)), es, eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	q := question(t, rep, "q")
	near(t, "agreement", q.Agreement, 1)
	near(t, "mean iou", q.MeanIoU, 1)
	if len(rep.Samples) != 0 {
		t.Errorf("grounding must add no calibration samples: %d", len(rep.Samples))
	}
	if q.NProbs != 0 || len(q.Confusion) != 0 {
		t.Errorf("no probs or confusion for boxes: %+v", q)
	}
}

func TestGroundingReferenceErrors(t *testing.T) {
	cs := groundCases(1, false)
	cand := byIndex(judgetest.Grounding(refBox, 0))
	bad := judge.Box{Top: 0.5, Bottom: 0.1, Left: 0, Right: 1}
	for name, ref := range map[string]*eval.Reference{
		"invalid box":               {Boxes: map[string]judge.Box{"q": bad}},
		"unknown question":          {Boxes: map[string]judge.Box{"zz": refBox}},
		"label string":              {Labels: map[string]string{"q": "mug"}},
		"abstain label not allowed": {Labels: map[string]string{"q": judge.LabelAbstain}},
	} {
		_, err := eval.Compare(context.Background(), nil, cand, []eval.Entry{{Case: cs[0], Reference: ref}}, eval.Options{})
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// boxes for a non-grounding question
	yc := ynCases(1, false)
	_, err := eval.Compare(context.Background(), nil, byIndex(yn(0.9)),
		[]eval.Entry{{Case: yc[0], Reference: &eval.Reference{Boxes: map[string]judge.Box{"q": refBox}}}}, eval.Options{})
	if err == nil {
		t.Error("box reference for a yes/no question accepted")
	}
}

func TestGroundingMarkdown(t *testing.T) {
	cs := groundCases(1, false)
	rep, err := eval.Compare(context.Background(), nil, byIndex(judgetest.Grounding(refBox, 0.5)), boxEntries(cs, &refBox), eval.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if md := rep.Markdown(); !strings.Contains(md, "iou") || !strings.Contains(md, "1.000") {
		t.Errorf("markdown lacks iou:\n%s", md)
	}
}
