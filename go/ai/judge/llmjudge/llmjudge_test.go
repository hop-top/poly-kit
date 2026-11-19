package llmjudge_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/llmjudge"
	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/ai/prompt"
)

// fake is a hermetic llm.Completer: replies in order, records requests.
type fake struct {
	mu      sync.Mutex
	replies []string
	err     error
	reqs    []llm.Request
}

func (f *fake) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return llm.Response{}, f.err
	}
	if len(f.reqs) > len(f.replies) {
		return llm.Response{}, errors.New("fake: script exhausted")
	}
	return llm.Response{
		Content: f.replies[len(f.reqs)-1],
		Usage:   llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}, nil
}

func reply(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return string(b)
}

func ans(value string, conf float64, why string) map[string]any {
	return map[string]any{"value": value, "confidence": conf, "why": why}
}

var (
	qChoice = judge.Question{
		Name: "route", Kind: judge.KindChoice,
		Options: []judge.Option{{Value: "a", Description: "first"}, {Value: "b"}, {Value: "c"}},
	}
	qScore = judge.Question{
		Name: "urgency", Kind: judge.KindScore,
		Anchors: []string{"low", "mid", "high"},
	}
	qYesNo = judge.Question{Name: "ok", Kind: judge.KindYesNo, Instructions: "is it fine?"}
)

func run(t *testing.T, f *fake, c judge.Case, opts ...llmjudge.Option) (judge.Verdict, error) {
	t.Helper()
	return llmjudge.New(f, opts...).Judge(context.Background(), c)
}

func TestChoice(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{
		"route": ans("b", 0.6, "because"),
	})}}
	c := judge.Case{State: map[string]any{"k": "v"}, Questions: []judge.Question{qChoice}}
	v, err := run(t, f, c)
	require.NoError(t, err)

	a := v.Answers["route"]
	assert.Equal(t, "b", a.Value)
	assert.InDelta(t, 0.6, a.Confidence, 1e-9)
	assert.Empty(t, a.Probs)
	assert.Equal(t, "because", a.Why)
	assert.Equal(t, "verbalized", a.Source)
	assert.NoError(t, judge.ValidateVerdict(c, v))
	assert.Equal(t, "llm", v.Backend)
	assert.Equal(t, 15, v.Usage.TotalTokens)

	// request carries schema constrained to the labels, and the state.
	require.Len(t, f.reqs, 1)
	req := f.reqs[0]
	require.NotNil(t, req.ResponseFormat)
	assert.Contains(t, string(req.ResponseFormat.Schema), `"enum":["a","b","c"]`)
	var all strings.Builder
	for _, m := range req.Messages {
		all.WriteString(m.Content)
	}
	assert.Contains(t, all.String(), `"k"`)
	assert.Contains(t, all.String(), "first")
}

func TestValueOutsideLabelsRepairs(t *testing.T) {
	bad := reply(t, map[string]any{"route": ans("zzz", 0.5, "")})
	f := &fake{replies: []string{bad, bad, bad}}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qChoice}})
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrSchemaInvalid)
}

func TestConfidenceOutOfRange(t *testing.T) {
	bad := reply(t, map[string]any{"ok": ans("yes", 1.5, "")})
	f := &fake{replies: []string{bad, bad, bad}}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}})
	assert.ErrorIs(t, err, llm.ErrSchemaInvalid)
}

func TestScoreFromIndex(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{"urgency": ans("1", 0.8, "")})}}
	c := judge.Case{Questions: []judge.Question{qScore}}
	v, err := run(t, f, c)
	require.NoError(t, err)
	assert.InDelta(t, 0.5, v.Answers["urgency"].Score, 1e-9)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestYesNo(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{"ok": ans("yes", 0.7, "")})}}
	c := judge.Case{Questions: []judge.Question{qYesNo}}
	v, err := run(t, f, c)
	require.NoError(t, err)
	assert.Equal(t, "yes", v.Answers["ok"].Value)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestAbstain(t *testing.T) {
	q := qYesNo
	q.Abstain = true
	f := &fake{replies: []string{reply(t, map[string]any{"ok": ans("abstain", 0.7, "unsure")})}}
	c := judge.Case{Questions: []judge.Question{q}}
	v, err := run(t, f, c)
	require.NoError(t, err)
	a := v.Answers["ok"]
	assert.True(t, a.Abstained)
	assert.NoError(t, judge.ValidateVerdict(c, v))

	qs := qScore
	qs.Abstain = true
	f = &fake{replies: []string{reply(t, map[string]any{"urgency": ans("abstain", 0.7, "")})}}
	c = judge.Case{Questions: []judge.Question{qs}}
	v, err = run(t, f, c)
	require.NoError(t, err)
	assert.Zero(t, v.Answers["urgency"].Score)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestAbstainLabelOnlyWhenAllowed(t *testing.T) {
	bad := reply(t, map[string]any{"ok": ans("abstain", 0.7, "")})
	f := &fake{replies: []string{bad, bad, bad}}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}})
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrSchemaInvalid)
}

func TestMultipleQuestionsOneCall(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{
		"route":   ans("a", 1, ""),
		"urgency": ans("2", 1, ""),
		"ok":      ans("no", 1, ""),
	})}}
	c := judge.Case{Questions: []judge.Question{qChoice, qScore, qYesNo}}
	v, err := run(t, f, c)
	require.NoError(t, err)
	assert.Len(t, f.reqs, 1)
	assert.Equal(t, 1.0, v.Answers["urgency"].Score)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestVotes(t *testing.T) {
	r := func(v string) string { return reply(t, map[string]any{"ok": ans(v, 0.9, "why-"+v)}) }
	f := &fake{replies: []string{r("yes"), r("yes"), r("no"), r("yes")}}
	c := judge.Case{Questions: []judge.Question{qYesNo}}
	v, err := run(t, f, c, llmjudge.Mode(llmjudge.Votes(4)), llmjudge.Temperature(0.9))
	require.NoError(t, err)
	assert.Len(t, f.reqs, 4)
	for _, req := range f.reqs {
		require.NotNil(t, req.Temperature)
		assert.Equal(t, 0.9, *req.Temperature)
	}
	a := v.Answers["ok"]
	assert.Equal(t, "yes", a.Value)
	assert.InDelta(t, 0.75, a.Probs["yes"], 1e-9)
	assert.InDelta(t, 0.75, a.Confidence, 1e-9)
	assert.Equal(t, "votes", a.Source)
	assert.Equal(t, "why-yes", a.Why)
	assert.Equal(t, 60, v.Usage.TotalTokens)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestVotesScore(t *testing.T) {
	r := func(i string) string { return reply(t, map[string]any{"urgency": ans(i, 0.5, "")}) }
	f := &fake{replies: []string{r("0"), r("2"), r("2"), r("2")}}
	c := judge.Case{Questions: []judge.Question{qScore}}
	v, err := run(t, f, c, llmjudge.Mode(llmjudge.Votes(4)))
	require.NoError(t, err)
	assert.InDelta(t, 0.75, v.Answers["urgency"].Score, 1e-9)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestVoteTieGoesToEarlierLabel(t *testing.T) {
	r := func(v string) string { return reply(t, map[string]any{"ok": ans(v, 0.9, "")}) }
	f := &fake{replies: []string{r("no"), r("yes")}}
	v, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}}, llmjudge.Mode(llmjudge.Votes(2)))
	require.NoError(t, err)
	assert.Equal(t, "yes", v.Answers["ok"].Value)
}

func TestVotesFailureFailsCase(t *testing.T) {
	r := reply(t, map[string]any{"ok": ans("yes", 0.9, "")})
	f := &fake{replies: []string{r}} // second sample: fake exhausted
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}}, llmjudge.Mode(llmjudge.Votes(2)))
	require.Error(t, err)
}

func TestVotesDefaultTemperature(t *testing.T) {
	r := reply(t, map[string]any{"ok": ans("yes", 1, "")})
	f := &fake{replies: []string{r, r, r}}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}}, llmjudge.Mode(llmjudge.Votes(3)))
	require.NoError(t, err)
	require.NotNil(t, f.reqs[0].Temperature)
	assert.Greater(t, *f.reqs[0].Temperature, 0.0)

	f = &fake{replies: []string{r}}
	_, err = run(t, f, judge.Case{Questions: []judge.Question{qYesNo}})
	require.NoError(t, err)
	assert.Nil(t, f.reqs[0].Temperature)
}

func TestInvalidOutputRepairs(t *testing.T) {
	good := reply(t, map[string]any{"ok": ans("yes", 0.6, "")})
	f := &fake{replies: []string{`not json`, good}}
	c := judge.Case{Questions: []judge.Question{qYesNo}}
	v, err := run(t, f, c)
	require.NoError(t, err)
	assert.Len(t, f.reqs, 2)
	assert.Equal(t, "yes", v.Answers["ok"].Value)
	assert.Equal(t, 30, v.Usage.TotalTokens, "repair usage counted")
}

func TestInvalidOutputErrors(t *testing.T) {
	f := &fake{replies: []string{`x`, `y`, `z`, `w`}}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}}, llmjudge.MaxRepairs(1))
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrSchemaInvalid)
	assert.Len(t, f.reqs, 2)
}

func TestUnsupportedKind(t *testing.T) {
	f := &fake{}
	q := judge.Question{Name: "n", Kind: "noul"}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{q}})
	require.Error(t, err)
	var ce *llmerrors.ErrCapabilityNotSupported
	assert.ErrorAs(t, err, &ce)
	assert.Empty(t, f.reqs)
}

func TestInvalidCase(t *testing.T) {
	f := &fake{}
	_, err := run(t, f, judge.Case{})
	assert.ErrorIs(t, err, judge.ErrInvalid)
	assert.Empty(t, f.reqs)
}

func TestClientError(t *testing.T) {
	boom := errors.New("boom")
	f := &fake{err: boom}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}})
	assert.ErrorIs(t, err, boom)
}

func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := llmjudge.New(&fake{}).Judge(ctx, judge.Case{Questions: []judge.Question{qYesNo}})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestModelAssetsPrice(t *testing.T) {
	r := reply(t, map[string]any{"ok": ans("yes", 1, "")})
	f := &fake{replies: []string{r}}
	asset := llm.ContentPart{Type: llm.PartTypeImage, MimeType: "image/png"}
	c := judge.Case{Questions: []judge.Question{qYesNo}, Assets: []llm.ContentPart{asset}}
	v, err := run(t, f, c, llmjudge.Model("m1"), llmjudge.Price(llm.Price{Input: 2e6, Output: 4e6}))
	require.NoError(t, err)
	assert.Equal(t, "m1", f.reqs[0].Model)
	assert.Equal(t, "m1", v.Model)
	assert.InDelta(t, 10*2+5*4, v.Cost.USD, 1e-9)
	assert.Equal(t, int64(15), v.Cost.Tokens)
	var found bool
	for _, m := range f.reqs[0].Messages {
		for _, p := range m.Parts {
			if p.Type == llm.PartTypeImage {
				found = true
			}
		}
	}
	assert.True(t, found, "asset forwarded as message part")
}

func TestNoPriceZeroUSD(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{"ok": ans("yes", 1, "")})}}
	v, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}})
	require.NoError(t, err)
	assert.Zero(t, v.Cost.USD)
}

func TestPatternSystemPrompt(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{"ok": ans("yes", 1, "")})}}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}},
		llmjudge.Pattern(&prompt.Pattern{Name: "p", System: "CUSTOM SYSTEM"}))
	require.NoError(t, err)
	require.Equal(t, "system", f.reqs[0].Messages[0].Role)
	assert.Equal(t, "CUSTOM SYSTEM", f.reqs[0].Messages[0].Content)
	assert.NotNil(t, f.reqs[0].ResponseFormat)
}

func TestVotesBelowOneIsOne(t *testing.T) {
	r := reply(t, map[string]any{"ok": ans("yes", 1, "")})
	f := &fake{replies: []string{r}}
	v, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}}, llmjudge.Mode(llmjudge.Votes(0)))
	require.NoError(t, err)
	assert.Len(t, f.reqs, 1)
	assert.Equal(t, "verbalized", v.Answers["ok"].Source)
}
