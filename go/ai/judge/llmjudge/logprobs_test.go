package llmjudge_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/calibrate"
	"hop.top/kit/go/ai/judge/llmjudge"
	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// alt is one top_logprobs entry; p is a probability, stored as its log.
type alt struct {
	tok string
	p   float64
}

// lpFake is a hermetic Completer returning one scripted Response per call.
type lpFake struct {
	mu   sync.Mutex
	resp []llm.Response
	reqs []llm.Request
}

func (f *lpFake) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	if len(f.reqs) > len(f.resp) {
		return llm.Response{}, errors.New("fake: script exhausted")
	}
	return f.resp[len(f.reqs)-1], nil
}

// answerResp is a reply to {"answer": "<picked>"} tokenised the way a
// typical BPE does: the value token sits after `{"`, `answer`, `":`, ` "`.
// top lists the alternatives at the value token.
func answerResp(picked string, top ...alt) llm.Response {
	var ts []llm.TopLogprob
	for _, a := range top {
		ts = append(ts, llm.TopLogprob{Token: a.tok, Logprob: math.Log(a.p)})
	}
	return llm.Response{
		Content: fmt.Sprintf(`{"answer": %q}`, picked),
		Usage:   llm.Usage{PromptTokens: 10, CompletionTokens: 6, TotalTokens: 16},
		Logprobs: []llm.TokenLogprob{
			{Token: `{"`, Logprob: -0.6},
			{Token: `answer`, Logprob: -0.1},
			{Token: `":`, Logprob: -0.2},
			{Token: ` "`, Logprob: -0.001},
			{Token: picked, Logprob: -0.3, Top: ts},
			{Token: `"}`, Logprob: -0.3},
		},
	}
}

var (
	lpChoice = judge.Question{
		Name: "route", Kind: judge.KindChoice,
		Options: []judge.Option{
			{Value: "urgent", Description: "needs action today"},
			{Value: "later"},
			{Value: "drop"},
		},
	}
	lpScore = judge.Question{
		Name: "urgency", Kind: judge.KindScore,
		Anchors: []string{"low", "mid", "high"},
	}
	lpYesNo = judge.Question{Name: "ok", Kind: judge.KindYesNo, Instructions: "is it fine?"}
)

func runLP(t *testing.T, f *lpFake, c judge.Case, opts ...llmjudge.Option) (judge.Verdict, error) {
	t.Helper()
	opts = append([]llmjudge.Option{llmjudge.Mode(llmjudge.Logprobs)}, opts...)
	return llmjudge.New(f, opts...).Judge(context.Background(), c)
}

// norm normalises raw probabilities the way the mode should.
func norm(m map[string]float64) map[string]float64 {
	var s float64
	for _, v := range m {
		s += v
	}
	out := map[string]float64{}
	for k, v := range m {
		out[k] = v / s
	}
	return out
}

func TestLogprobsChoice(t *testing.T) {
	f := &lpFake{resp: []llm.Response{
		answerResp("B", alt{"B", 0.6}, alt{"A", 0.3}, alt{"C", 0.05}, alt{"apple", 0.04}),
	}}
	c := judge.Case{State: map[string]any{"k": "v"}, Questions: []judge.Question{lpChoice}}
	v, err := runLP(t, f, c)
	require.NoError(t, err)

	a := v.Answers["route"]
	assert.Equal(t, "later", a.Value)
	want := norm(map[string]float64{"urgent": 0.3, "later": 0.6, "drop": 0.05})
	require.Len(t, a.Probs, 3)
	for k, p := range want {
		assert.InDelta(t, p, a.Probs[k], 1e-9, k)
	}
	assert.InDelta(t, want["later"], a.Confidence, 1e-9)
	assert.Equal(t, "logprobs", a.Source)
	assert.Equal(t, llmjudge.SourceLogprobs, a.Source)
	assert.Empty(t, a.Why)
	assert.NoError(t, judge.ValidateVerdict(c, v))
	assert.Equal(t, 16, v.Usage.TotalTokens)
	assert.Equal(t, "llm", v.Backend)
}

func TestLogprobsRequestShape(t *testing.T) {
	f := &lpFake{resp: []llm.Response{
		answerResp("A", alt{"A", 0.9}, alt{"B", 0.1}),
		answerResp("Y", alt{"Y", 0.8}, alt{"N", 0.2}),
	}}
	c := judge.Case{Questions: []judge.Question{lpChoice, lpYesNo}}
	_, err := runLP(t, f, c, llmjudge.Model("m1"))
	require.NoError(t, err)

	// One call per question: token location stays unambiguous.
	require.Len(t, f.reqs, 2)
	r := f.reqs[0]
	assert.Equal(t, "m1", r.Model)
	require.NotNil(t, r.Logprobs)
	assert.Equal(t, llm.MaxTopLogprobs, r.Logprobs.TopK)
	require.NotNil(t, r.ResponseFormat)
	assert.JSONEq(t, `{"type":"object","properties":{"answer":{"type":"string","enum":["A","B","C"]}},
		"required":["answer"],"additionalProperties":false}`, string(r.ResponseFormat.Schema))
	assert.True(t, r.ResponseFormat.Strict)

	user := r.Messages[len(r.Messages)-1].Content
	assert.Contains(t, user, "A: urgent")
	assert.Contains(t, user, "needs action today")
	assert.Contains(t, user, "B: later")
	assert.NotContains(t, user, "ok", "only the asked question is sent")

	var sch struct {
		Properties struct {
			Answer struct{ Enum []string }
		}
	}
	require.NoError(t, json.Unmarshal(f.reqs[1].ResponseFormat.Schema, &sch))
	assert.Equal(t, []string{"Y", "N"}, sch.Properties.Answer.Enum)
}

func TestLogprobsYesNo(t *testing.T) {
	f := &lpFake{resp: []llm.Response{answerResp("Y", alt{"Y", 0.7}, alt{"N", 0.2})}}
	c := judge.Case{Questions: []judge.Question{lpYesNo}}
	v, err := runLP(t, f, c)
	require.NoError(t, err)

	a := v.Answers["ok"]
	assert.Equal(t, judge.LabelYes, a.Value)
	assert.InDelta(t, 0.7/0.9, a.Probs[judge.LabelYes], 1e-9)
	assert.InDelta(t, 0.2/0.9, a.Probs[judge.LabelNo], 1e-9)
	assert.InDelta(t, a.Probs[judge.LabelYes], a.Confidence, 1e-12)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestLogprobsScore(t *testing.T) {
	f := &lpFake{resp: []llm.Response{
		answerResp("C", alt{"C", 0.5}, alt{"B", 0.3}, alt{"A", 0.2}),
	}}
	c := judge.Case{Questions: []judge.Question{lpScore}}
	v, err := runLP(t, f, c)
	require.NoError(t, err)

	a := v.Answers["urgency"]
	assert.Equal(t, "2", a.Value)
	assert.InDelta(t, 0.5, a.Probs["2"], 1e-9)
	assert.InDelta(t, 0.3, a.Probs["1"], 1e-9)
	assert.InDelta(t, judge.ScoreEV(a.Probs, 3), a.Score, 1e-12)
	assert.InDelta(t, (0.3*1+0.5*2)/2, a.Score, 1e-9)
	assert.NoError(t, judge.ValidateVerdict(c, v))

	user := f.reqs[0].Messages[len(f.reqs[0].Messages)-1].Content
	assert.Contains(t, user, "A: low")
	assert.Contains(t, user, "C: high")
}

func TestLogprobsAbstain(t *testing.T) {
	q := judge.Question{Name: "ok", Kind: judge.KindYesNo, Abstain: true}
	f := &lpFake{resp: []llm.Response{
		answerResp("X", alt{"X", 0.6}, alt{"Y", 0.3}, alt{"N", 0.1}),
	}}
	c := judge.Case{Questions: []judge.Question{q}}
	v, err := runLP(t, f, c)
	require.NoError(t, err)

	a := v.Answers["ok"]
	assert.Equal(t, judge.LabelAbstain, a.Value)
	assert.True(t, a.Abstained)
	assert.InDelta(t, 0.6, a.Probs[judge.LabelAbstain], 1e-9)
	assert.NoError(t, judge.ValidateVerdict(c, v))

	var sch struct {
		Properties struct {
			Answer struct{ Enum []string }
		}
	}
	require.NoError(t, json.Unmarshal(f.reqs[0].ResponseFormat.Schema, &sch))
	assert.Equal(t, []string{"Y", "N", "X"}, sch.Properties.Answer.Enum)

	// score + abstain: abstained score is 0.
	qs := lpScore
	qs.Abstain = true
	f = &lpFake{resp: []llm.Response{answerResp("X", alt{"X", 0.9}, alt{"C", 0.1})}}
	c = judge.Case{Questions: []judge.Question{qs}}
	v, err = runLP(t, f, c)
	require.NoError(t, err)
	assert.True(t, v.Answers["urgency"].Abstained)
	assert.Zero(t, v.Answers["urgency"].Score)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestLogprobsMissingKeysFloor(t *testing.T) {
	// A and C absent from top-k: they get the documented floor, not 0 and
	// not an error; the distribution still sums to 1.
	f := &lpFake{resp: []llm.Response{answerResp("B", alt{"B", 0.9}, alt{"apple", 0.05})}}
	c := judge.Case{Questions: []judge.Question{lpChoice}}
	v, err := runLP(t, f, c)
	require.NoError(t, err)

	a := v.Answers["route"]
	assert.Equal(t, "later", a.Value)
	for _, l := range []string{"urgent", "drop"} {
		p, ok := a.Probs[l]
		require.True(t, ok, l)
		assert.Greater(t, p, 0.0)
		assert.Less(t, p, 1e-5)
	}
	assert.InDelta(t, 1, a.Probs["urgent"]+a.Probs["later"]+a.Probs["drop"], 1e-9)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestLogprobsSampledTokenOutsideTop(t *testing.T) {
	// The sampled token is not always in Top; it still counts.
	r := answerResp("B", alt{"A", 0.2})
	v, err := runLP(t, &lpFake{resp: []llm.Response{r}}, judge.Case{Questions: []judge.Question{lpChoice}})
	require.NoError(t, err)
	assert.Equal(t, "later", v.Answers["route"].Value)
	assert.InDelta(t, math.Exp(-0.3)/(math.Exp(-0.3)+0.2), v.Answers["route"].Probs["later"], 1e-3)
}

func TestLogprobsTokenVariantsAndPunctuation(t *testing.T) {
	// Quotes and spaces glued to the key; the same key under two token
	// spellings adds up.
	r := llm.Response{
		Content: `{"answer":"B"}`,
		Logprobs: []llm.TokenLogprob{
			{Token: `{"answer":"`, Logprob: -0.1},
			{Token: `B"}`, Logprob: math.Log(0.4), Top: []llm.TopLogprob{
				{Token: `B"}`, Logprob: math.Log(0.4)},
				{Token: ` B`, Logprob: math.Log(0.2)},
				{Token: `A"}`, Logprob: math.Log(0.3)},
				{Token: `"C`, Logprob: math.Log(0.1)},
			}},
		},
	}
	v, err := runLP(t, &lpFake{resp: []llm.Response{r}}, judge.Case{Questions: []judge.Question{lpChoice}})
	require.NoError(t, err)
	a := v.Answers["route"]
	assert.InDelta(t, 0.6, a.Probs["later"], 1e-9)
	assert.InDelta(t, 0.3, a.Probs["urgent"], 1e-9)
	assert.InDelta(t, 0.1, a.Probs["drop"], 1e-9)
}

func TestLogprobsCapExceeded(t *testing.T) {
	var opts []judge.Option
	for i := 0; i < llm.MaxTopLogprobs; i++ {
		opts = append(opts, judge.Option{Value: fmt.Sprintf("o%d", i)})
	}
	f := &lpFake{}

	// MaxTopLogprobs labels fit.
	q := judge.Question{Name: "q", Kind: judge.KindChoice, Options: opts}
	f.resp = []llm.Response{answerResp("A", alt{"A", 0.5}, alt{"B", 0.5})}
	_, err := runLP(t, f, judge.Case{Questions: []judge.Question{q}})
	require.NoError(t, err)

	// One more (abstain) does not: refused, never truncated, no call.
	q.Abstain = true
	f = &lpFake{}
	_, err = runLP(t, f, judge.Case{Questions: []judge.Question{q}})
	require.Error(t, err)
	var ce *llmerrors.ErrCapabilityNotSupported
	assert.True(t, errors.As(err, &ce), "got %v", err)
	assert.Contains(t, err.Error(), "20")
	assert.Empty(t, f.reqs)
}

func TestLogprobsAnswerTokenNotFound(t *testing.T) {
	// Content passes the schema; the tokens do not line up with it.
	cases := map[string]llm.Response{
		"value token is not a key": {
			Content: `{"answer": "B"}`,
			Logprobs: []llm.TokenLogprob{
				{Token: `{"answer": "`, Logprob: -0.1},
				{Token: `apple`, Logprob: -0.1},
				{Token: `"}`, Logprob: -0.1},
			},
		},
		"no answer field in tokens": {
			Content: `{"answer": "B"}`,
			Logprobs: []llm.TokenLogprob{
				{Token: `{"other": "`, Logprob: -0.1},
				{Token: `B`, Logprob: -0.1},
				{Token: `"}`, Logprob: -0.1},
			},
		},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := runLP(t, &lpFake{resp: []llm.Response{r}}, judge.Case{Questions: []judge.Question{lpChoice}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "answer token")
			assert.Contains(t, err.Error(), "repair")
		})
	}
}

func TestLogprobsNoRepair(t *testing.T) {
	// Out-of-schema output is not re-asked in this mode.
	f := &lpFake{resp: []llm.Response{
		{Content: `{"answer": "Z"}`, Logprobs: []llm.TokenLogprob{{Token: "Z"}}},
		answerResp("A", alt{"A", 1}),
	}}
	_, err := runLP(t, f, judge.Case{Questions: []judge.Question{lpChoice}})
	require.Error(t, err)
	assert.True(t, errors.Is(err, llm.ErrSchemaInvalid))
	assert.Len(t, f.reqs, 1)
}

func TestLogprobsUnsupportedReply(t *testing.T) {
	// A backend that ignored the request: no logprobs in the reply.
	f := &lpFake{resp: []llm.Response{{Content: `{"answer": "A"}`}}}
	_, err := runLP(t, f, judge.Case{Questions: []judge.Question{lpChoice}})
	var ce *llmerrors.ErrCapabilityNotSupported
	assert.True(t, errors.As(err, &ce), "got %v", err)
}

func TestLogprobsInvalidCase(t *testing.T) {
	_, err := runLP(t, &lpFake{}, judge.Case{})
	assert.True(t, errors.Is(err, judge.ErrInvalid))
}

func TestLogprobsCalibratedRoundTrip(t *testing.T) {
	f := &lpFake{resp: []llm.Response{
		answerResp("A", alt{"A", 0.6}, alt{"B", 0.3}, alt{"C", 0.1}),
		answerResp("A", alt{"A", 0.6}, alt{"B", 0.3}, alt{"C", 0.1}),
	}}
	c := judge.Case{Questions: []judge.Question{lpChoice}}
	raw := llmjudge.New(f, llmjudge.Mode(llmjudge.Logprobs))
	base, err := raw.Judge(context.Background(), c)
	require.NoError(t, err)

	cal := calibrate.Calibrated(raw, calibrate.Params{Questions: map[string]calibrate.QuestionParams{
		"route": {Temperature: &calibrate.Temperature{T: 3}},
	}})
	v, err := cal.Judge(context.Background(), c)
	require.NoError(t, err)

	a, b := v.Answers["route"], base.Answers["route"]
	assert.Equal(t, b.Value, a.Value)
	assert.Less(t, a.Confidence, b.Confidence, "T>1 softens")
	assert.InDelta(t, a.Probs[a.Value], a.Confidence, 1e-12)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestLogprobsPriceAndTemperature(t *testing.T) {
	f := &lpFake{resp: []llm.Response{answerResp("Y", alt{"Y", 1})}}
	temp := 0.0
	_, err := runLP(t, f, judge.Case{Questions: []judge.Question{lpYesNo}},
		llmjudge.Temperature(temp), llmjudge.Price(llm.Price{Input: 1, Output: 1}))
	require.NoError(t, err)
	require.NotNil(t, f.reqs[0].Temperature)
	assert.Zero(t, *f.reqs[0].Temperature)
	assert.False(t, strings.Contains(f.reqs[0].Messages[0].Content, "confidence"),
		"logprobs prompt does not ask for a stated confidence")
}

func TestLogprobsThinkingTokensPrecedeReply(t *testing.T) {
	// A reasoning model's logprobs cover its thinking too, which may quote
	// the JSON; the reply's own answer token is the one read.
	r := answerResp("B", alt{"B", 0.8}, alt{"A", 0.2})
	think := []llm.TokenLogprob{
		{Token: `Output: {"answer": "`, Logprob: -0.1},
		{Token: `A`, Logprob: -0.1, Top: []llm.TopLogprob{{Token: "A", Logprob: math.Log(0.99)}}},
		{Token: `"}</think>`, Logprob: -0.1},
	}
	r.Logprobs = append(think, r.Logprobs...)
	v, err := runLP(t, &lpFake{resp: []llm.Response{r}}, judge.Case{Questions: []judge.Question{lpChoice}})
	require.NoError(t, err)
	assert.Equal(t, "later", v.Answers["route"].Value)
}
