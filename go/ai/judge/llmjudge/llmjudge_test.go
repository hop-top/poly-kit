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

func ans(probs map[string]any, why string) map[string]any {
	return map[string]any{"probs": probs, "why": why}
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
		"route": ans(map[string]any{"a": 0.2, "b": 0.6, "c": 0.2}, "because"),
	})}}
	c := judge.Case{State: map[string]any{"k": "v"}, Questions: []judge.Question{qChoice}}
	v, err := run(t, f, c)
	require.NoError(t, err)

	a := v.Answers["route"]
	assert.Equal(t, "b", a.Value)
	assert.InDelta(t, 0.6, a.Confidence, 1e-9)
	assert.Equal(t, "because", a.Why)
	assert.Equal(t, "verbalized", a.Source)
	assert.NoError(t, judge.ValidateVerdict(c, v))
	assert.Equal(t, "llm", v.Backend)
	assert.Equal(t, 15, v.Usage.TotalTokens)

	// request carries schema constrained to the labels, and the state.
	require.Len(t, f.reqs, 1)
	req := f.reqs[0]
	require.NotNil(t, req.ResponseFormat)
	assert.Contains(t, string(req.ResponseFormat.Schema), `"route"`)
	var all strings.Builder
	for _, m := range req.Messages {
		all.WriteString(m.Content)
	}
	assert.Contains(t, all.String(), `"k"`)
	assert.Contains(t, all.String(), "first")
}

func TestChoiceNormalisesProbs(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{
		"route": ans(map[string]any{"a": 0.5, "b": 0.25, "c": 0.25, "x": 0}, ""),
	})}}
	// unknown label "x" violates the schema -> repaired; second reply absent -> err.
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qChoice}})
	require.Error(t, err)

	f = &fake{replies: []string{reply(t, map[string]any{
		"route": ans(map[string]any{"a": 0.9, "b": 0.9, "c": 0.2}, ""),
	})}}
	c := judge.Case{Questions: []judge.Question{qChoice}}
	v, err := run(t, f, c)
	require.NoError(t, err)
	a := v.Answers["route"]
	assert.InDelta(t, 0.45, a.Probs["a"], 1e-9)
	assert.Equal(t, "a", a.Value, "tie goes to earlier label")
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestScoreExpectedValue(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{
		"urgency": ans(map[string]any{"0": 0.0, "1": 0.5, "2": 0.5}, ""),
	})}}
	c := judge.Case{Questions: []judge.Question{qScore}}
	v, err := run(t, f, c)
	require.NoError(t, err)
	a := v.Answers["urgency"]
	assert.InDelta(t, 0.75, a.Score, 1e-9)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestYesNo(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{
		"ok": ans(map[string]any{"yes": 0.7, "no": 0.3}, ""),
	})}}
	c := judge.Case{Questions: []judge.Question{qYesNo}}
	v, err := run(t, f, c)
	require.NoError(t, err)
	assert.Equal(t, "yes", v.Answers["ok"].Value)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestAbstain(t *testing.T) {
	q := qYesNo
	q.Abstain = true
	f := &fake{replies: []string{reply(t, map[string]any{
		"ok": ans(map[string]any{"yes": 0.1, "no": 0.2, "abstain": 0.7}, "unsure"),
	})}}
	c := judge.Case{Questions: []judge.Question{q}}
	v, err := run(t, f, c)
	require.NoError(t, err)
	a := v.Answers["ok"]
	assert.True(t, a.Abstained)
	assert.Equal(t, "abstain", a.Value)
	assert.NoError(t, judge.ValidateVerdict(c, v))

	// score + abstain: score 0.
	qs := qScore
	qs.Abstain = true
	f = &fake{replies: []string{reply(t, map[string]any{
		"urgency": ans(map[string]any{"0": 0.1, "1": 0.1, "2": 0.1, "abstain": 0.7}, ""),
	})}}
	c = judge.Case{Questions: []judge.Question{qs}}
	v, err = run(t, f, c)
	require.NoError(t, err)
	assert.Zero(t, v.Answers["urgency"].Score)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestAbstainLabelOnlyWhenAllowed(t *testing.T) {
	bad := reply(t, map[string]any{
		"ok": ans(map[string]any{"yes": 0.1, "no": 0.2, "abstain": 0.7}, ""),
	})
	f := &fake{replies: []string{bad, bad, bad}}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}})
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrSchemaInvalid)
}

func TestMultipleQuestionsOneCall(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{
		"route":   ans(map[string]any{"a": 1, "b": 0, "c": 0}, ""),
		"urgency": ans(map[string]any{"0": 0, "1": 0, "2": 1}, ""),
		"ok":      ans(map[string]any{"yes": 0, "no": 1}, ""),
	})}}
	c := judge.Case{Questions: []judge.Question{qChoice, qScore, qYesNo}}
	v, err := run(t, f, c)
	require.NoError(t, err)
	assert.Len(t, f.reqs, 1)
	assert.Equal(t, 1.0, v.Answers["urgency"].Score)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestVotes(t *testing.T) {
	r := func(a, b float64) string {
		return reply(t, map[string]any{"ok": ans(map[string]any{"yes": a, "no": b}, "")})
	}
	f := &fake{replies: []string{r(0.9, 0.1), r(0.8, 0.2), r(0.3, 0.7), r(0.6, 0.4)}}
	c := judge.Case{Questions: []judge.Question{qYesNo}}
	v, err := run(t, f, c, llmjudge.WithVotes(4), llmjudge.WithTemperature(0.9))
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
	assert.Equal(t, 60, v.Usage.TotalTokens)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestVotesScore(t *testing.T) {
	r := func(i string) string {
		p := map[string]any{"0": 0, "1": 0, "2": 0}
		p[i] = 1
		return reply(t, map[string]any{"urgency": ans(p, "")})
	}
	f := &fake{replies: []string{r("0"), r("2"), r("2"), r("2")}}
	c := judge.Case{Questions: []judge.Question{qScore}}
	v, err := run(t, f, c, llmjudge.WithVotes(4))
	require.NoError(t, err)
	assert.InDelta(t, 0.75, v.Answers["urgency"].Score, 1e-9)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestVotesDefaultTemperature(t *testing.T) {
	r := reply(t, map[string]any{"ok": ans(map[string]any{"yes": 1, "no": 0}, "")})
	f := &fake{replies: []string{r, r, r}}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}}, llmjudge.WithVotes(3))
	require.NoError(t, err)
	require.NotNil(t, f.reqs[0].Temperature)
	assert.Greater(t, *f.reqs[0].Temperature, 0.0)

	// single sample leaves provider default.
	f = &fake{replies: []string{r}}
	_, err = run(t, f, judge.Case{Questions: []judge.Question{qYesNo}})
	require.NoError(t, err)
	assert.Nil(t, f.reqs[0].Temperature)
}

func TestInvalidOutputRepairs(t *testing.T) {
	good := reply(t, map[string]any{"ok": ans(map[string]any{"yes": 0.6, "no": 0.4}, "")})
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
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}}, llmjudge.WithMaxRepairs(1))
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrSchemaInvalid)
	assert.Len(t, f.reqs, 2)
}

func TestAllZeroProbsRejected(t *testing.T) {
	f := &fake{replies: []string{reply(t, map[string]any{
		"ok": ans(map[string]any{"yes": 0, "no": 0}, ""),
	})}}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}})
	require.Error(t, err)
	assert.ErrorIs(t, err, judge.ErrInvalid)
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
	f := &fake{}
	_, err := llmjudge.New(f).Judge(ctx, judge.Case{Questions: []judge.Question{qYesNo}})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestModelAndAssets(t *testing.T) {
	r := reply(t, map[string]any{"ok": ans(map[string]any{"yes": 1, "no": 0}, "")})
	f := &fake{replies: []string{r}}
	asset := llm.ContentPart{Type: llm.PartTypeImage, MimeType: "image/png"}
	c := judge.Case{Questions: []judge.Question{qYesNo}, Assets: []llm.ContentPart{asset}}
	v, err := run(t, f, c, llmjudge.WithModel("m1"))
	require.NoError(t, err)
	assert.Equal(t, "m1", f.reqs[0].Model)
	assert.Equal(t, "m1", v.Model)
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

func TestImplementsJudge(t *testing.T) {
	var _ judge.Judge = llmjudge.New(&fake{})
}

func TestVotesBelowOneIsOne(t *testing.T) {
	r := reply(t, map[string]any{"ok": ans(map[string]any{"yes": 1, "no": 0}, "")})
	f := &fake{replies: []string{r}}
	_, err := run(t, f, judge.Case{Questions: []judge.Question{qYesNo}}, llmjudge.WithVotes(0))
	require.NoError(t, err)
	assert.Len(t, f.reqs, 1)
}
