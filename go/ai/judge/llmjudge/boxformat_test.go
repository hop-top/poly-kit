package llmjudge_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/llmjudge"
	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// sized is a plain image of w x h pixels, PNG or JPEG encoded.
func sized(t *testing.T, w, h int, jpg bool) llm.ContentPart {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	var buf bytes.Buffer
	mime := "image/png"
	if jpg {
		mime = "image/jpeg"
		require.NoError(t, jpeg.Encode(&buf, img, nil))
	} else {
		require.NoError(t, png.Encode(&buf, img))
	}
	return llm.ContentPart{
		Type: llm.PartTypeImage, MimeType: mime,
		Source:   llm.InlineSource(buf.Bytes(), mime),
		Metadata: map[string]any{judge.AssetNameKey: "photo"},
	}
}

func caseWith(q judge.Question, img llm.ContentPart) judge.Case {
	return judge.Case{Questions: []judge.Question{q}, Assets: []llm.ContentPart{img}}
}

func approxBox(t *testing.T, want, got judge.Box) {
	t.Helper()
	const eps = 1e-9
	assert.InDelta(t, want.Top, got.Top, eps, "top")
	assert.InDelta(t, want.Bottom, got.Bottom, eps, "bottom")
	assert.InDelta(t, want.Left, got.Left, eps, "left")
	assert.InDelta(t, want.Right, got.Right, eps, "right")
}

// The red rectangle of the local-model probes: pixels [400,100,560,220] of
// a 640x480 image = grid [625,208,875,458].
var probeBox = judge.Box{Top: 0.2083333333, Bottom: 0.4583333333, Left: 0.625, Right: 0.875}

func TestGrid1000ProbeReplies(t *testing.T) {
	tests := map[string]struct {
		reply string
		want  judge.Box
	}{
		"qwen3-vl 8b string-encoded array under a free key": {
			reply: `{"red_rectangle": "[625, 207, 875, 457]"}`,
			want:  judge.Box{Top: 0.207, Bottom: 0.457, Left: 0.625, Right: 0.875},
		},
		"qwen3-vl 4b bbox_2d": {
			reply: `{"bbox_2d": [620, 200, 870, 450]}`,
			want:  judge.Box{Top: 0.2, Bottom: 0.45, Left: 0.62, Right: 0.87},
		},
		"fenced array of objects, first wins": {
			reply: "```json\n[{\"bbox_2d\": [100, 200, 300, 400], \"label\": \"rect\"}, {\"bbox_2d\": [0, 0, 5, 5]}]\n```",
			want:  judge.Box{Top: 0.2, Bottom: 0.4, Left: 0.1, Right: 0.3},
		},
		"numbers as strings": {
			reply: `{"bbox_2d": ["100", "200", "300", "400"]}`,
			want:  judge.Box{Top: 0.2, Bottom: 0.4, Left: 0.1, Right: 0.3},
		},
		"bare array": {
			reply: `[100, 200, 300, 400]`,
			want:  judge.Box{Top: 0.2, Bottom: 0.4, Left: 0.1, Right: 0.3},
		},
		"prose around the json": {
			reply: "Sure, here it is: {\"bbox_2d\": [100, 200, 300, 400]} hope that helps",
			want:  judge.Box{Top: 0.2, Bottom: 0.4, Left: 0.1, Right: 0.3},
		},
		"nested under another key": {
			reply: `{"result": {"object": "rect", "coords": [100, 200, 300, 400]}}`,
			want:  judge.Box{Top: 0.2, Bottom: 0.4, Left: 0.1, Right: 0.3},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := &fake{replies: []string{tc.reply}}
			c := groundCase(qGround)
			v, err := run(t, f, c, llmjudge.BoxFormat(llmjudge.Grid1000))
			require.NoError(t, err)
			a := v.Answers["mug"]
			require.NotNil(t, a.Box)
			approxBox(t, tc.want, *a.Box)
			assert.Equal(t, "verbalized", a.Source)
			assert.NoError(t, judge.ValidateVerdict(c, v))
		})
	}
}

func TestGrid1000RequestIsFreeText(t *testing.T) {
	f := &fake{replies: []string{`{"bbox_2d": [100, 200, 300, 400]}`}}
	_, err := run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Grid1000), llmjudge.Temperature(0))
	require.NoError(t, err)
	require.Len(t, f.reqs, 1)
	req := f.reqs[0]
	assert.Nil(t, req.ResponseFormat, "no schema format: it breaks VLM grounding")
	require.NotNil(t, req.Temperature)
	user := req.Messages[1]
	assert.Contains(t, user.Content, "the mug in `photo`")
	assert.Contains(t, user.Content, `"bbox_2d": [x1, y1, x2, y2]`)
	assert.Contains(t, user.Content, "0 to 1000")
	assert.Contains(t, user.Content, "top-left")
	assert.Contains(t, user.Content, "confidence")
	var imgs int
	for _, p := range user.Parts {
		if p.Type == llm.PartTypeImage {
			imgs++
		}
	}
	assert.Equal(t, 1, imgs)
}

func TestGrid1000ConfidenceAndWhy(t *testing.T) {
	f := &fake{replies: []string{`{"bbox_2d": [100, 200, 300, 400], "confidence": 0.8, "why": "left side"}`}}
	v, err := run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Grid1000))
	require.NoError(t, err)
	a := v.Answers["mug"]
	assert.Equal(t, 0.8, a.Confidence)
	assert.Equal(t, "left side", a.Why)

	// out-of-range or missing confidence is unknown (0), not a failure
	f = &fake{replies: []string{`{"bbox_2d": [100, 200, 300, 400], "confidence": 7}`}}
	v, err = run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Grid1000))
	require.NoError(t, err)
	assert.Equal(t, 0.0, v.Answers["mug"].Confidence)
}

func TestPixelsDividesByImageSize(t *testing.T) {
	for _, jpg := range []bool{false, true} {
		f := &fake{replies: []string{`{"bbox_2d": [400, 100, 560, 220]}`}}
		c := caseWith(qGround, sized(t, 640, 480, jpg))
		v, err := run(t, f, c, llmjudge.BoxFormat(llmjudge.Pixels))
		require.NoError(t, err)
		require.NotNil(t, v.Answers["mug"].Box)
		approxBox(t, judge.Box{Top: 100.0 / 480, Bottom: 220.0 / 480, Left: 400.0 / 640, Right: 560.0 / 640}, *v.Answers["mug"].Box)
		assert.NoError(t, judge.ValidateVerdict(c, v))
		assert.Contains(t, f.reqs[0].Messages[1].Content, "pixels")
		assert.Nil(t, f.reqs[0].ResponseFormat)
	}
}

func TestPixelsQwen25Reply(t *testing.T) {
	// fenced array of objects; x2 runs past the width: clamped
	f := &fake{replies: []string{"```json\n[{\"bbox_2d\": [646,162,908,361], \"label\": \"red rectangle\"}]\n```"}}
	v, err := run(t, f, caseWith(qGround, sized(t, 640, 480, false)), llmjudge.BoxFormat(llmjudge.Pixels))
	// x1 = 646 > 640 clamps to 1 and x2 clamps to 1: zero width
	assert.ErrorIs(t, err, judge.ErrInvalid)
	_ = v

	f = &fake{replies: []string{"```json\n[{\"bbox_2d\": [646,162,908,361], \"label\": \"red rectangle\"}]\n```"}}
	v, err = run(t, f, caseWith(qGround, sized(t, 1280, 960, false)), llmjudge.BoxFormat(llmjudge.Pixels))
	require.NoError(t, err)
	approxBox(t, judge.Box{Top: 162.0 / 960, Bottom: 361.0 / 960, Left: 646.0 / 1280, Right: 908.0 / 1280}, *v.Answers["mug"].Box)
}

func TestPixelsNeedsDecodableImage(t *testing.T) {
	f := &fake{replies: []string{`{"bbox_2d": [1, 2, 3, 4]}`}}
	_, err := run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Pixels)) // photo() holds "png" bytes
	assert.ErrorIs(t, err, judge.ErrInvalid)
	assert.Empty(t, f.reqs, "fail before spending a call")
}

func TestPixelsRefusesSeveralImages(t *testing.T) {
	a, b := sized(t, 10, 10, false), sized(t, 10, 10, false)
	a.Metadata = map[string]any{judge.AssetNameKey: "a"}
	b.Metadata = map[string]any{judge.AssetNameKey: "b"}
	c := judge.Case{Questions: []judge.Question{qGround}, Assets: []llm.ContentPart{a, b}}
	f := &fake{}
	_, err := run(t, f, c, llmjudge.BoxFormat(llmjudge.Pixels))
	assert.ErrorIs(t, err, judge.ErrInvalid)
	assert.Empty(t, f.reqs)
}

func TestBoxClampAndReorder(t *testing.T) {
	// corners swapped, then partly outside the grid
	f := &fake{replies: []string{`{"bbox_2d": [900, 800, 100, 50]}`}}
	v, err := run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Grid1000))
	require.NoError(t, err)
	approxBox(t, judge.Box{Top: 0.05, Bottom: 0.8, Left: 0.1, Right: 0.9}, *v.Answers["mug"].Box)

	f = &fake{replies: []string{`{"bbox_2d": [-50, 100, 500, 1200]}`}}
	v, err = run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Grid1000))
	require.NoError(t, err)
	approxBox(t, judge.Box{Top: 0.1, Bottom: 1, Left: 0, Right: 0.5}, *v.Answers["mug"].Box)
}

func TestBoxDegenerateRejected(t *testing.T) {
	for name, reply := range map[string]string{
		"zero width":         `{"bbox_2d": [500, 100, 500, 400]}`,
		"zero height":        `{"bbox_2d": [100, 500, 400, 500]}`,
		"point":              `{"bbox_2d": [0, 0, 0, 0]}`,
		"whole image":        `{"bbox_2d": [0, 0, 1000, 1000]}`,
		"clamps to whole":    `{"bbox_2d": [-10, -10, 1500, 1500]}`,
		"wholly outside":     `{"bbox_2d": [1100, 1100, 1300, 1300]}`,
		"degenerate fenced":  "```json\n[{\"bbox_2d\": [0,0,1000,1000]}]\n```",
		"string whole image": `{"box": "[0, 0, 1000, 1000]"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := &fake{replies: []string{reply}}
			_, err := run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Grid1000))
			assert.ErrorIs(t, err, judge.ErrInvalid)
			assert.Len(t, f.reqs, 1, "a degenerate box is not repaired")
		})
	}
}

func TestBoxFormatRepair(t *testing.T) {
	f := &fake{replies: []string{
		"I cannot see any box here, sorry.",
		`{"bbox_2d": [100, 200, 300, 400]}`,
	}}
	v, err := run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Grid1000))
	require.NoError(t, err)
	require.Len(t, f.reqs, 2)
	msgs := f.reqs[1].Messages
	require.Len(t, msgs, 4)
	assert.Equal(t, "assistant", msgs[2].Role)
	assert.Equal(t, "I cannot see any box here, sorry.", msgs[2].Content)
	assert.Equal(t, "user", msgs[3].Role)
	assert.Contains(t, msgs[3].Content, "bbox_2d")
	require.NotNil(t, v.Answers["mug"].Box)
	assert.Equal(t, 30, v.Usage.TotalTokens, "usage sums across attempts")
}

func TestBoxFormatRepairExhausted(t *testing.T) {
	f := &fake{replies: []string{"nope", "still nope", "never"}}
	_, err := run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Grid1000))
	assert.ErrorIs(t, err, llm.ErrSchemaInvalid)
	assert.Len(t, f.reqs, 3, "default 2 repairs")

	f = &fake{replies: []string{"nope", "x"}}
	_, err = run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Grid1000), llmjudge.MaxRepairs(0))
	assert.ErrorIs(t, err, llm.ErrSchemaInvalid)
	assert.Len(t, f.reqs, 1)
}

func TestBoxFormatAbstain(t *testing.T) {
	q := qGround
	q.Abstain = true
	f := &fake{replies: []string{`{"abstain": true}`}}
	c := groundCase(q)
	v, err := run(t, f, c, llmjudge.BoxFormat(llmjudge.Grid1000))
	require.NoError(t, err)
	a := v.Answers["mug"]
	assert.True(t, a.Abstained)
	assert.Nil(t, a.Box)
	assert.NoError(t, judge.ValidateVerdict(c, v))
	assert.Contains(t, f.reqs[0].Messages[1].Content, "abstain")

	// a box with abstain false/absent is located
	f = &fake{replies: []string{`{"bbox_2d": [100, 200, 300, 400], "abstain": false}`}}
	v, err = run(t, f, c, llmjudge.BoxFormat(llmjudge.Grid1000))
	require.NoError(t, err)
	assert.NotNil(t, v.Answers["mug"].Box)

	// without Abstain on the question the prompt does not offer it
	f = &fake{replies: []string{`{"bbox_2d": [100, 200, 300, 400]}`}}
	_, err = run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Grid1000))
	require.NoError(t, err)
	assert.NotContains(t, f.reqs[0].Messages[1].Content, "abstain")
}

func TestBoxFormatSeveralQuestionsOneCallEach(t *testing.T) {
	q2 := judge.Question{Name: "cup", Kind: judge.KindGrounding, Instructions: "the cup in `photo`"}
	f := &fake{replies: []string{`{"bbox_2d": [100, 200, 300, 400]}`, `{"bbox_2d": [500, 500, 600, 700]}`}}
	c := groundCase(qGround)
	c.Questions = append(c.Questions, q2)
	v, err := run(t, f, c, llmjudge.BoxFormat(llmjudge.Grid1000))
	require.NoError(t, err)
	assert.Len(t, f.reqs, 2)
	assert.Contains(t, f.reqs[1].Messages[1].Content, "the cup in `photo`")
	assert.NotContains(t, f.reqs[1].Messages[1].Content, "the mug")
	approxBox(t, judge.Box{Top: 0.5, Bottom: 0.7, Left: 0.5, Right: 0.6}, *v.Answers["cup"].Box)
	assert.Equal(t, 30, v.Usage.TotalTokens)
}

func TestBoxFormatRefusesOtherKinds(t *testing.T) {
	c := groundCase(qGround)
	c.Questions = append(c.Questions, qYesNo)
	f := &fake{}
	_, err := run(t, f, c, llmjudge.BoxFormat(llmjudge.Grid1000))
	var ce *llmerrors.ErrCapabilityNotSupported
	require.ErrorAs(t, err, &ce)
	assert.Empty(t, f.reqs)
}

func TestBoxFormatFractionsIsDefault(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{"mug": boxAns(0.1, 0.6, 0.2, 0.7, 0.8, "x")})}}
	v, err := run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Fractions))
	require.NoError(t, err)
	assert.NotNil(t, f.reqs[0].ResponseFormat)
	assert.Equal(t, judge.Box{Top: 0.1, Bottom: 0.6, Left: 0.2, Right: 0.7}, *v.Answers["mug"].Box)
}

func TestBoxFormatIgnoredWithoutGrounding(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{"ok": ans("yes", 0.9, "")})}}
	v, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}}, llmjudge.BoxFormat(llmjudge.Grid1000))
	require.NoError(t, err)
	assert.Equal(t, "yes", v.Answers["ok"].Value)
	assert.NotNil(t, f.reqs[0].ResponseFormat)
}

func TestBoxFormatRefusedInVotes(t *testing.T) {
	f := &fake{}
	_, err := run(t, f, groundCase(qGround), llmjudge.BoxFormat(llmjudge.Grid1000), llmjudge.Mode(llmjudge.Votes(3)))
	var ce *llmerrors.ErrCapabilityNotSupported
	require.ErrorAs(t, err, &ce)
}

func TestBoxFormatFor(t *testing.T) {
	for model, want := range map[string]llmjudge.BoxConvention{
		"qwen3-vl:8b":           llmjudge.Grid1000,
		"Qwen3-VL:4b":           llmjudge.Grid1000,
		"qwen2-vl:7b":           llmjudge.Grid1000,
		"qwen2.5vl:3b":          llmjudge.Pixels,
		"qwen2.5-vl:7b":         llmjudge.Pixels,
		"library/qwen3-vl:8b":   llmjudge.Grid1000,
		"hf.co/x/Qwen2.5-VL-7B": llmjudge.Pixels,
		"gpt-4o":                llmjudge.Fractions,
		"":                      llmjudge.Fractions,
		"qwen3:8b":              llmjudge.Fractions,
	} {
		assert.Equal(t, want, llmjudge.BoxFormatFor(model), model)
	}
}
