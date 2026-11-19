package llmjudge_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/llmjudge"
	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

var qGround = judge.Question{Name: "mug", Kind: judge.KindGrounding, Instructions: "the mug in `photo`"}

func photo() llm.ContentPart {
	return llm.ContentPart{
		Type: llm.PartTypeImage, MimeType: "image/png",
		Source:   llm.InlineSource([]byte("png"), "image/png"),
		Metadata: map[string]any{judge.AssetNameKey: "photo"},
	}
}

func groundCase(q judge.Question) judge.Case {
	return judge.Case{Questions: []judge.Question{q}, Assets: []llm.ContentPart{photo()}}
}

func boxAns(top, bottom, left, right, conf float64, why string) map[string]any {
	return map[string]any{
		"box":        map[string]any{"top": top, "bottom": bottom, "left": left, "right": right},
		"confidence": conf, "why": why,
	}
}

func TestGroundingVerbalized(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{"mug": boxAns(0.1, 0.6, 0.2, 0.7, 0.8, "on the left")})}}
	c := groundCase(qGround)
	v, err := run(t, f, c)
	require.NoError(t, err)

	a := v.Answers["mug"]
	require.NotNil(t, a.Box)
	assert.Equal(t, judge.Box{Top: 0.1, Bottom: 0.6, Left: 0.2, Right: 0.7}, *a.Box)
	assert.Equal(t, 0.8, a.Confidence)
	assert.Equal(t, "on the left", a.Why)
	assert.Equal(t, "verbalized", a.Source)
	assert.Empty(t, a.Value)
	assert.NoError(t, judge.ValidateVerdict(c, v))

	// the image rides as a part, named so the instructions can point at it
	user := f.reqs[0].Messages[1]
	var texts []string
	var imgs int
	for _, p := range user.Parts {
		switch p.Type {
		case llm.PartTypeImage:
			imgs++
		case llm.PartTypeText:
			texts = append(texts, p.Text)
		}
	}
	assert.Equal(t, 1, imgs)
	assert.Contains(t, texts, "Image `photo`:")
	assert.Contains(t, user.Content, "the mug in `photo`")
	assert.Contains(t, user.Content, "fractions")
}

func TestGroundingSchema(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{"mug": boxAns(0, 1, 0, 1, 0.5, "")})}}
	_, err := run(t, f, groundCase(qGround))
	require.NoError(t, err)
	var s struct {
		Properties map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(f.reqs[0].ResponseFormat.Schema, &s))
	m := s.Properties["mug"]
	assert.ElementsMatch(t, []string{"box", "confidence", "why"}, m.Required)
	assert.NotContains(t, m.Properties, "abstain", "no abstain unless allowed")
	assert.Contains(t, string(m.Properties["box"]), `"top"`)
	assert.Contains(t, string(m.Properties["box"]), `"maximum":1`)
}

func TestGroundingAbstain(t *testing.T) {
	q := qGround
	q.Abstain = true
	r := boxAns(0, 0, 0, 0, 0.9, "no mug")
	r["abstain"] = true
	f := &fake{replies: []string{reply(t, map[string]any{"mug": r})}}
	c := groundCase(q)
	v, err := run(t, f, c)
	require.NoError(t, err)

	a := v.Answers["mug"]
	assert.True(t, a.Abstained)
	assert.Equal(t, judge.LabelAbstain, a.Value)
	assert.Nil(t, a.Box)
	assert.NoError(t, judge.ValidateVerdict(c, v))

	var s struct {
		Properties map[string]struct {
			Required []string `json:"required"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(f.reqs[0].ResponseFormat.Schema, &s))
	assert.Contains(t, s.Properties["mug"].Required, "abstain")
}

func TestGroundingAbstainFalseKeepsBox(t *testing.T) {
	q := qGround
	q.Abstain = true
	r := boxAns(0.1, 0.5, 0.1, 0.5, 0.7, "")
	r["abstain"] = false
	f := &fake{replies: []string{reply(t, map[string]any{"mug": r})}}
	v, err := run(t, f, groundCase(q))
	require.NoError(t, err)
	require.NotNil(t, v.Answers["mug"].Box)
	assert.False(t, v.Answers["mug"].Abstained)
}

func TestGroundingInvalidBoxFails(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{"mug": boxAns(0.6, 0.1, 0.2, 0.7, 0.8, "")})}}
	_, err := run(t, f, groundCase(qGround))
	assert.ErrorIs(t, err, judge.ErrInvalid)
}

func TestGroundingMixedWithOtherKinds(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{
		"mug": boxAns(0.1, 0.6, 0.2, 0.7, 0.8, ""),
		"ok":  ans("yes", 0.9, ""),
	})}}
	c := groundCase(qGround)
	c.Questions = append(c.Questions, qYesNo)
	v, err := run(t, f, c)
	require.NoError(t, err)
	assert.Len(t, f.reqs, 1)
	assert.Equal(t, "yes", v.Answers["ok"].Value)
	assert.NotNil(t, v.Answers["mug"].Box)
}

func TestGroundingNeedsImage(t *testing.T) {
	f := &fake{}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qGround}})
	assert.ErrorIs(t, err, judge.ErrInvalid)
	assert.Empty(t, f.reqs)
}

func TestGroundingRefusedInVotesAndLogprobs(t *testing.T) {
	for name, m := range map[string]llmjudge.ModeKind{
		"votes":    llmjudge.Votes(3),
		"logprobs": llmjudge.Logprobs,
	} {
		t.Run(name, func(t *testing.T) {
			f := &fake{}
			_, err := run(t, f, groundCase(qGround), llmjudge.Mode(m))
			var ce *llmerrors.ErrCapabilityNotSupported
			require.ErrorAs(t, err, &ce)
			assert.Equal(t, "llmjudge", ce.Provider)
			assert.Empty(t, f.reqs)
		})
	}
}

func TestGroundingVotesOneIsVerbalized(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{"mug": boxAns(0.1, 0.6, 0.2, 0.7, 0.8, "")})}}
	v, err := run(t, f, groundCase(qGround), llmjudge.Mode(llmjudge.Votes(1)))
	require.NoError(t, err)
	assert.Equal(t, "verbalized", v.Answers["mug"].Source)
}
