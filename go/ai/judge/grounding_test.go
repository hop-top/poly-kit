package judge_test

import (
	"math"
	"strings"
	"testing"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/llm"
)

func groundQ(abstain bool) judge.Question {
	return judge.Question{Name: "where", Kind: judge.KindGrounding, Abstain: abstain,
		Instructions: "the mug in `photo`"}
}

func pngAsset() llm.ContentPart {
	return llm.ContentPart{Type: llm.PartTypeImage, Source: llm.InlineSource([]byte("x"), "image/png")}
}

func TestValidateGroundingCase(t *testing.T) {
	tests := []struct {
		name string
		c    judge.Case
		want string
	}{
		{"valid", judge.Case{Questions: []judge.Question{groundQ(true)}, Assets: []llm.ContentPart{pngAsset()}}, ""},
		{"no asset", judge.Case{Questions: []judge.Question{groundQ(false)}}, "image asset"},
		{"non-image asset", judge.Case{Questions: []judge.Question{groundQ(false)},
			Assets: []llm.ContentPart{{Type: llm.PartTypeText, Text: "hi"}}}, "image asset"},
		{"no instructions", judge.Case{Questions: []judge.Question{{Name: "w", Kind: judge.KindGrounding}},
			Assets: []llm.ContentPart{pngAsset()}}, "instructions"},
		{"options", judge.Case{Questions: []judge.Question{{Name: "w", Kind: judge.KindGrounding, Instructions: "x",
			Options: []judge.Option{{Value: "a"}}}}, Assets: []llm.ContentPart{pngAsset()}}, "no options or anchors"},
		{"anchors", judge.Case{Questions: []judge.Question{{Name: "w", Kind: judge.KindGrounding, Instructions: "x",
			Anchors: []string{"a", "b"}}}, Assets: []llm.ContentPart{pngAsset()}}, "no options or anchors"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := judge.Validate(tc.c)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateGroundingAnswer(t *testing.T) {
	ok := &judge.Box{Top: 0.1, Bottom: 0.6, Left: 0.2, Right: 0.7}
	tests := []struct {
		name string
		q    judge.Question
		a    judge.Answer
		want string
	}{
		{"box", groundQ(false), judge.Answer{Box: ok, Confidence: 0.8}, ""},
		{"box confidence unknown", groundQ(false), judge.Answer{Box: ok}, ""},
		{"full image", groundQ(false), judge.Answer{Box: &judge.Box{Top: 0, Bottom: 1, Left: 0, Right: 1}}, ""},
		{"abstain", groundQ(true), judge.Answer{Value: "abstain", Abstained: true}, ""},
		{"abstain disallowed", groundQ(false), judge.Answer{Value: "abstain", Abstained: true}, "abstain not allowed"},
		{"abstain with box", groundQ(true), judge.Answer{Value: "abstain", Abstained: true, Box: ok}, "has a box"},
		{"abstain flag, no value", groundQ(true), judge.Answer{Abstained: true}, "value"},
		{"missing box", groundQ(true), judge.Answer{}, "no box"},
		{"value set", groundQ(false), judge.Answer{Value: "mug", Box: ok}, "want empty"},
		{"top >= bottom", groundQ(false), judge.Answer{Box: &judge.Box{Top: 0.6, Bottom: 0.1, Left: 0.2, Right: 0.7}}, "top<bottom"},
		{"left == right", groundQ(false), judge.Answer{Box: &judge.Box{Top: 0.1, Bottom: 0.6, Left: 0.5, Right: 0.5}}, "left<right"},
		{"negative", groundQ(false), judge.Answer{Box: &judge.Box{Top: -0.1, Bottom: 0.6, Left: 0.2, Right: 0.7}}, "within [0,1]"},
		{"over one", groundQ(false), judge.Answer{Box: &judge.Box{Top: 0.1, Bottom: 1.1, Left: 0.2, Right: 0.7}}, "within [0,1]"},
		{"nan", groundQ(false), judge.Answer{Box: &judge.Box{Top: math.NaN(), Bottom: 0.6, Left: 0.2, Right: 0.7}}, "within [0,1]"},
		{"confidence range", groundQ(false), judge.Answer{Box: ok, Confidence: 1.5}, "confidence"},
		{"probs", groundQ(false), judge.Answer{Box: ok, Probs: map[string]float64{"x": 1}}, "no probs"},
		{"box on choice", choiceQ(false), judge.Answer{Value: "a", Box: ok}, "box only for grounding"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := judge.ValidateAnswer(tc.q, tc.a)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestGroundingLabelsEmpty(t *testing.T) {
	if got := judge.Labels(groundQ(true)); len(got) != 0 {
		t.Fatalf("Labels = %v, want none (abstain included)", got)
	}
}

func TestBoxIoU(t *testing.T) {
	a := judge.Box{Top: 0, Bottom: 0.5, Left: 0, Right: 0.5}
	tests := []struct {
		name string
		b    judge.Box
		want float64
	}{
		{"same", a, 1},
		{"disjoint", judge.Box{Top: 0.6, Bottom: 1, Left: 0.6, Right: 1}, 0},
		{"touching", judge.Box{Top: 0, Bottom: 0.5, Left: 0.5, Right: 1}, 0},
		{"half", judge.Box{Top: 0, Bottom: 0.5, Left: 0.25, Right: 0.75}, 1.0 / 3},
		{"inside", judge.Box{Top: 0, Bottom: 0.25, Left: 0, Right: 0.5}, 0.5},
		{"degenerate", judge.Box{Top: 0.2, Bottom: 0.2, Left: 0.1, Right: 0.3}, 0},
	}
	for _, tc := range tests {
		if got := a.IoU(tc.b); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: IoU = %v, want %v", tc.name, got, tc.want)
		}
		if got := tc.b.IoU(a); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s reversed: IoU = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestImageNames(t *testing.T) {
	named := llm.ContentPart{Type: llm.PartTypeImage, Metadata: map[string]any{judge.AssetNameKey: "photo"}}
	txt := llm.ContentPart{Type: llm.PartTypeText, Text: "x"}
	tests := []struct {
		name string
		in   []llm.ContentPart
		want string
	}{
		{"none", nil, ""},
		{"single default", []llm.ContentPart{pngAsset()}, "image"},
		{"single named", []llm.ContentPart{named}, "photo"},
		{"several", []llm.ContentPart{pngAsset(), txt, named, pngAsset()}, "image1,photo,image3"},
	}
	for _, tc := range tests {
		if got := strings.Join(judge.ImageNames(tc.in), ","); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestValidateImageNames(t *testing.T) {
	named := llm.ContentPart{Type: llm.PartTypeImage, Metadata: map[string]any{judge.AssetNameKey: "photo"}}
	q := []judge.Question{groundQ(false)}
	err := judge.Validate(judge.Case{Questions: q, Assets: []llm.ContentPart{named, named}})
	if err == nil || !strings.Contains(err.Error(), "duplicate image asset name") {
		t.Fatalf("dup: %v", err)
	}
	err = judge.Validate(judge.Case{Questions: q, State: map[string]any{"photo": 1}, Assets: []llm.ContentPart{named}})
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("clash: %v", err)
	}
}
