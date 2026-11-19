package jev_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/jev"
	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// testKey is a made-up credential; never a real one.
const testKey = "sk-or-v1-test-0000000000000000"

// seen is one request the fake received.
type seen struct {
	url     string
	headers http.Header
	body    map[string]any
	raw     string
}

// fakeTransport is a hermetic http.RoundTripper standing in for the
// service. Answers are in the shapes TypeSafe documents for POST
// /v1/systemone (the OpenRouter route adds usage.cost).
type fakeTransport struct {
	mu     sync.Mutex
	status int
	header http.Header
	body   string
	err    error
	reqs   []seen
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	s := seen{url: r.URL.String(), headers: r.Header.Clone(), raw: string(raw)}
	_ = json.Unmarshal(raw, &s.body)
	f.reqs = append(f.reqs, s)
	if f.err != nil {
		return nil, f.err
	}
	st := f.status
	if st == 0 {
		st = 200
	}
	h := f.header
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{
		StatusCode: st,
		Status:     http.StatusText(st),
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Request:    r,
	}, nil
}

func (f *fakeTransport) last(t *testing.T) seen {
	t.Helper()
	require.NotEmpty(t, f.reqs, "no request reached the transport")
	return f.reqs[len(f.reqs)-1]
}

func newJudge(t *testing.T, f *fakeTransport, opts ...jev.Option) judge.Judge {
	t.Helper()
	opts = append([]jev.Option{jev.WithAPIKey(testKey), jev.WithTransport(f)}, opts...)
	j, err := jev.New(opts...)
	require.NoError(t, err)
	return j
}

var (
	qChoice = judge.Question{
		Name: "tone", Kind: judge.KindChoice, Instructions: "What is the tone?",
		Options: []judge.Option{
			{Value: "angry", Description: "An upset or hostile message"},
			{Value: "calm", Description: "A neutral or polite message"},
			{Value: "excited"},
		},
	}
	qScore = judge.Question{
		Name: "urgency", Kind: judge.KindScore, Instructions: "How urgent?",
		Anchors: []string{"Can wait", "Needs attention this week", "Needs attention today"},
	}
	qYesNo = judge.Question{Name: "billing", Kind: judge.KindYesNo, Instructions: "Is this about billing?"}

	state = map[string]any{"message": "I was charged twice for my subscription."}
)

const allKindsResponse = `{
  "model": "typesafe/jev-1.14",
  "answers": {
    "tone": {"type": "choice", "choice": "calm", "confidence": 0.7,
             "probabilities": {"angry": 0.15, "calm": 0.8, "excited": 0.05}},
    "urgency": {"type": "score", "score": 1.7, "confidence": 0.55,
                "legend": {"0": "Can wait", "1": "Needs attention this week", "2": "Needs attention today"},
                "probabilities": {"0": 0.1, "1": 0.1, "2": 0.8}},
    "billing": {"type": "noul", "noul": 0.98}
  },
  "usage": {"input_tokens": 120, "output_tokens": 12, "cost": 0.000042}
}`

func allKindsCase() judge.Case {
	return judge.Case{State: state, Questions: []judge.Question{qChoice, qScore, qYesNo}}
}

func TestEachKind(t *testing.T) {
	f := &fakeTransport{body: allKindsResponse}
	c := allKindsCase()
	v, err := newJudge(t, f).Judge(context.Background(), c)
	require.NoError(t, err)
	require.NoError(t, judge.ValidateVerdict(c, v))

	assert.Equal(t, "jev", v.Backend)
	assert.Equal(t, "typesafe/jev-1.14", v.Model)

	tone := v.Answers["tone"]
	assert.Equal(t, "calm", tone.Value)
	assert.Equal(t, "native", tone.Source)
	assert.InDelta(t, 0.8, tone.Confidence, 1e-9, "confidence is Probs[Value], not the service's concentration figure")
	assert.InDelta(t, 0.15, tone.Probs["angry"], 1e-9)

	urg := v.Answers["urgency"]
	assert.Equal(t, "2", urg.Value, "score Value is the argmax anchor index")
	assert.InDelta(t, 0.85, urg.Score, 1e-9, "ScoreEV: (0.1*1 + 0.8*2)/2")
	assert.InDelta(t, 0.8, urg.Probs["2"], 1e-9)
	assert.InDelta(t, 0.8, urg.Confidence, 1e-9)

	yn := v.Answers["billing"]
	assert.Equal(t, judge.LabelYes, yn.Value)
	assert.False(t, yn.Abstained)
	assert.InDelta(t, 0.98, yn.Probs[judge.LabelYes], 1e-9)
	assert.InDelta(t, 0.02, yn.Probs[judge.LabelNo], 1e-9)
	assert.InDelta(t, 0.98, yn.Confidence, 1e-9)
	assert.Equal(t, "native", yn.Source)
}

func TestYesNoBelowHalfIsNo(t *testing.T) {
	f := &fakeTransport{body: `{"model":"m","answers":{"billing":{"type":"noul","noul":0.25}},"usage":{"input_tokens":1,"output_tokens":1}}`}
	c := judge.Case{State: state, Questions: []judge.Question{qYesNo}}
	v, err := newJudge(t, f).Judge(context.Background(), c)
	require.NoError(t, err)
	require.NoError(t, judge.ValidateVerdict(c, v))
	a := v.Answers["billing"]
	assert.Equal(t, judge.LabelNo, a.Value)
	assert.InDelta(t, 0.75, a.Confidence, 1e-9)
}

func TestRoundedProbsAreRenormalised(t *testing.T) {
	// The service rounds; 0.33+0.33+0.33 = 0.99 would fail ValidateAnswer.
	f := &fakeTransport{body: `{"model":"m","answers":{"tone":{"type":"choice","choice":"angry","confidence":0.4,
	  "probabilities":{"angry":0.34,"calm":0.33,"excited":0.32}}},"usage":{"input_tokens":1,"output_tokens":1}}`}
	c := judge.Case{State: state, Questions: []judge.Question{qChoice}}
	v, err := newJudge(t, f).Judge(context.Background(), c)
	require.NoError(t, err)
	require.NoError(t, judge.ValidateVerdict(c, v))
}

func TestChoiceWithoutProbabilitiesKeepsReportedChoice(t *testing.T) {
	// inbox's recorded shape: probabilities may come back empty.
	f := &fakeTransport{body: `{"model":"m","answers":{"tone":{"type":"choice","choice":"excited","confidence":0.95,"probabilities":{}}},
	  "usage":{"input_tokens":1,"output_tokens":1}}`}
	c := judge.Case{State: state, Questions: []judge.Question{qChoice}}
	v, err := newJudge(t, f).Judge(context.Background(), c)
	require.NoError(t, err)
	require.NoError(t, judge.ValidateVerdict(c, v))
	a := v.Answers["tone"]
	assert.Equal(t, "excited", a.Value)
	assert.Empty(t, a.Probs)
	assert.InDelta(t, 0.95, a.Confidence, 1e-9)
}

func TestScoreWithoutProbabilitiesFallsBackToScore(t *testing.T) {
	f := &fakeTransport{body: `{"model":"m","answers":{"urgency":{"type":"score","score":1.0,"confidence":0.5,"legend":{},"probabilities":{}}},
	  "usage":{"input_tokens":1,"output_tokens":1}}`}
	c := judge.Case{State: state, Questions: []judge.Question{qScore}}
	v, err := newJudge(t, f).Judge(context.Background(), c)
	require.NoError(t, err)
	require.NoError(t, judge.ValidateVerdict(c, v))
	a := v.Answers["urgency"]
	assert.Equal(t, "1", a.Value)
	assert.InDelta(t, 0.5, a.Score, 1e-9, "index 1 of 3 anchors")
}

func TestRequestShape(t *testing.T) {
	f := &fakeTransport{body: allKindsResponse}
	_, err := newJudge(t, f).Judge(context.Background(), allKindsCase())
	require.NoError(t, err)

	r := f.last(t)
	assert.Equal(t, "https://openrouter.ai/api/v1/systemone", r.url)
	assert.Equal(t, "Bearer "+testKey, r.headers.Get("Authorization"))
	assert.Equal(t, "application/json", r.headers.Get("Content-Type"))
	assert.Empty(t, r.headers.Get("TypeSafe-Danger-Zone-Images"), "images header only with images")

	assert.Equal(t, "typesafe/jev-1.14", r.body["model"])
	assert.Equal(t, map[string]any{"message": "I was charged twice for my subscription."}, r.body["state"])
	assert.NotContains(t, r.body, "assets")

	qs := r.body["questions"].(map[string]any)
	assert.Equal(t, map[string]any{
		"type":         "choice",
		"instructions": "What is the tone?",
		"criteria": map[string]any{
			"angry":   "An upset or hostile message",
			"calm":    "A neutral or polite message",
			"excited": "excited", // no description: the value stands in, as inbox sends it
		},
	}, qs["tone"])
	assert.Equal(t, map[string]any{
		"type":         "score",
		"instructions": "How urgent?",
		"criteria":     []any{"Can wait", "Needs attention this week", "Needs attention today"},
	}, qs["urgency"])
	assert.Equal(t, map[string]any{
		"type":         "noul",
		"instructions": "Is this about billing?",
	}, qs["billing"])
}

func TestOptions(t *testing.T) {
	f := &fakeTransport{body: allKindsResponse}
	j, err := jev.New(jev.WithAPIKey(testKey), jev.WithTransport(f), jev.WithModel("typesafe/jev-9"))
	require.NoError(t, err)
	_, err = j.Judge(context.Background(), allKindsCase())
	require.NoError(t, err)
	assert.Equal(t, "typesafe/jev-9", f.last(t).body["model"])
}

func TestCostAndUsage(t *testing.T) {
	f := &fakeTransport{body: allKindsResponse}
	v, err := newJudge(t, f).Judge(context.Background(), allKindsCase())
	require.NoError(t, err)
	assert.InDelta(t, 0.000042, v.Cost.USD, 1e-12)
	assert.Equal(t, int64(132), v.Cost.Tokens)
	assert.Equal(t, llm.Usage{PromptTokens: 120, CompletionTokens: 12, TotalTokens: 132}, v.Usage)
}

func TestNoCostIsZero(t *testing.T) {
	f := &fakeTransport{body: `{"model":"m","answers":{"billing":{"type":"noul","noul":0.5}},"usage":{"input_tokens":3,"output_tokens":1}}`}
	v, err := newJudge(t, f).Judge(context.Background(),
		judge.Case{State: state, Questions: []judge.Question{qYesNo}})
	require.NoError(t, err)
	assert.Zero(t, v.Cost.USD)
	assert.Equal(t, int64(4), v.Cost.Tokens)
}

var pngBytes = []byte("\x89PNG\r\n\x1a\nfake")

func image(mime, name string) llm.ContentPart {
	p := llm.ContentPart{Type: llm.PartTypeImage, Source: llm.InlineSource(pngBytes, mime)}
	if name != "" {
		p.Metadata = map[string]any{judge.AssetNameKey: name}
	}
	return p
}

var qBox = judge.Question{Name: "mug", Kind: judge.KindGrounding, Instructions: "the mug in `photo`"}

func groundCase(q judge.Question, assets ...llm.ContentPart) judge.Case {
	return judge.Case{Questions: []judge.Question{q}, Assets: assets}
}

func TestGroundingRequestShape(t *testing.T) {
	f := &fakeTransport{body: `{"answers":{"mug":{"type":"box","box":{"top":0.1,"bottom":0.6,"left":0.2,"right":0.7},"confidence":0.9}}}`}
	_, err := newJudge(t, f).Judge(context.Background(), groundCase(qBox, image("image/png", "photo")))
	require.NoError(t, err)

	r := f.last(t)
	assert.Equal(t, "true", r.headers.Get("TypeSafe-Danger-Zone-Images"))
	assert.Equal(t, map[string]any{"photo": "asset-photo"}, r.body["state"])
	assert.Equal(t, map[string]any{
		"asset-photo": map[string]any{
			"type": "image", "format": "png",
			"data_base64": base64.StdEncoding.EncodeToString(pngBytes),
		},
	}, r.body["assets"])
	assert.Equal(t, map[string]any{
		"mug": map[string]any{"type": "box", "instructions": "the mug in `photo`"},
	}, r.body["questions"], "box sends no criteria")
}

func TestImageDefaultNameAndJPEG(t *testing.T) {
	f := &fakeTransport{body: `{"answers":{"mug":{"type":"box","box":{"top":0,"bottom":1,"left":0,"right":1}}}}`}
	_, err := newJudge(t, f).Judge(context.Background(), groundCase(qBox, image("image/jpeg", "")))
	require.NoError(t, err)
	r := f.last(t)
	assert.Equal(t, map[string]any{"image": "asset-image"}, r.body["state"])
	assert.Equal(t, "jpg", r.body["assets"].(map[string]any)["asset-image"].(map[string]any)["format"])
}

func TestStateKeptAlongsideImage(t *testing.T) {
	f := &fakeTransport{body: allKindsResponse}
	c := allKindsCase()
	c.Assets = []llm.ContentPart{image("image/png", "")}
	_, err := newJudge(t, f).Judge(context.Background(), c)
	require.NoError(t, err)
	r := f.last(t)
	assert.Equal(t, "true", r.headers.Get("TypeSafe-Danger-Zone-Images"), "any question may carry an image")
	st := r.body["state"].(map[string]any)
	assert.Equal(t, "asset-image", st["image"])
	assert.Equal(t, "I was charged twice for my subscription.", st["message"])
}

func TestGroundingAnswers(t *testing.T) {
	tests := []struct {
		name string
		q    judge.Question
		body string
		want judge.Answer
		err  string
	}{
		{"box", qBox,
			`{"answers":{"mug":{"type":"box","box":{"top":0.1,"bottom":0.6,"left":0.2,"right":0.7},"confidence":0.9}}}`,
			judge.Answer{Box: &judge.Box{Top: 0.1, Bottom: 0.6, Left: 0.2, Right: 0.7}, Confidence: 0.9, Source: jev.SourceNative}, ""},
		{"null confidence", qBox,
			`{"answers":{"mug":{"type":"box","box":{"top":0.1,"bottom":0.6,"left":0.2,"right":0.7},"confidence":null}}}`,
			judge.Answer{Box: &judge.Box{Top: 0.1, Bottom: 0.6, Left: 0.2, Right: 0.7}, Source: jev.SourceNative}, ""},
		{"no confidence", qBox,
			`{"answers":{"mug":{"type":"box","box":{"top":0.1,"bottom":0.6,"left":0.2,"right":0.7}}}}`,
			judge.Answer{Box: &judge.Box{Top: 0.1, Bottom: 0.6, Left: 0.2, Right: 0.7}, Source: jev.SourceNative}, ""},
		{"abstain on null box", withAbstain(qBox),
			`{"answers":{"mug":{"type":"box","box":null,"confidence":0.4}}}`,
			judge.Answer{Value: judge.LabelAbstain, Abstained: true, Confidence: 0.4, Source: jev.SourceNative}, ""},
		{"abstain on missing box", withAbstain(qBox),
			`{"answers":{"mug":{"type":"box"}}}`,
			judge.Answer{Value: judge.LabelAbstain, Abstained: true, Source: jev.SourceNative}, ""},
		{"null box, abstain not allowed", qBox,
			`{"answers":{"mug":{"type":"box","box":null}}}`, judge.Answer{}, "no box"},
		{"partial box", qBox,
			`{"answers":{"mug":{"type":"box","box":{"top":0.1,"bottom":0.6,"left":0.2}}}}`, judge.Answer{}, "box"},
		{"inverted", qBox,
			`{"answers":{"mug":{"type":"box","box":{"top":0.6,"bottom":0.1,"left":0.2,"right":0.7}}}}`, judge.Answer{}, "top<bottom"},
		{"out of range", qBox,
			`{"answers":{"mug":{"type":"box","box":{"top":0.1,"bottom":1.6,"left":0.2,"right":0.7}}}}`, judge.Answer{}, "within [0,1]"},
		{"bad confidence", qBox,
			`{"answers":{"mug":{"type":"box","box":{"top":0.1,"bottom":0.6,"left":0.2,"right":0.7},"confidence":2}}}`, judge.Answer{}, "confidence"},
		{"wrong type", qBox,
			`{"answers":{"mug":{"type":"choice","choice":"a"}}}`, judge.Answer{}, "type"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeTransport{body: tc.body}
			v, err := newJudge(t, f).Judge(context.Background(), groundCase(tc.q, image("image/png", "photo")))
			if tc.err != "" {
				require.ErrorIs(t, err, jev.ErrMalformed)
				assert.Contains(t, err.Error(), tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, v.Answers["mug"])
		})
	}
}

func withAbstain(q judge.Question) judge.Question { q.Abstain = true; return q }

func TestGroundingNeedsImage(t *testing.T) {
	f := &fakeTransport{}
	_, err := newJudge(t, f).Judge(context.Background(), groundCase(qBox))
	require.ErrorIs(t, err, judge.ErrInvalid)
	assert.Empty(t, f.reqs)
}

func TestUnsendableAssetsRefused(t *testing.T) {
	for name, part := range map[string]llm.ContentPart{
		"webp":    image("image/webp", "photo"),
		"no mime": {Type: llm.PartTypeImage, Source: llm.InlineSource(pngBytes, "")},
		"audio":   {Type: llm.PartTypeAudio, Source: llm.InlineSource(pngBytes, "audio/wav")},
		"text":    {Type: llm.PartTypeText, Text: "note"},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeTransport{body: allKindsResponse}
			c := allKindsCase()
			c.Assets = []llm.ContentPart{part}
			_, err := newJudge(t, f).Judge(context.Background(), c)
			var cap *llmerrors.ErrCapabilityNotSupported
			require.ErrorAs(t, err, &cap)
			assert.Equal(t, "jev", cap.Provider)
			assert.Empty(t, f.reqs, "nothing sent for a refused case")
		})
	}
}

func TestUnknownKindRefused(t *testing.T) {
	f := &fakeTransport{body: allKindsResponse}
	c := judge.Case{State: state, Questions: []judge.Question{qYesNo, {Name: "x", Kind: judge.Kind("region")}}}
	_, err := newJudge(t, f).Judge(context.Background(), c)
	var cap *llmerrors.ErrCapabilityNotSupported
	require.ErrorAs(t, err, &cap)
	assert.Empty(t, f.reqs)
}

func TestInvalidCase(t *testing.T) {
	f := &fakeTransport{}
	_, err := newJudge(t, f).Judge(context.Background(), judge.Case{})
	require.ErrorIs(t, err, judge.ErrInvalid)

	tooMany := judge.Question{Name: "s", Kind: judge.KindScore, Anchors: make([]string, 11)}
	for i := range tooMany.Anchors {
		tooMany.Anchors[i] = "level"
	}
	_, err = newJudge(t, f).Judge(context.Background(), judge.Case{State: state, Questions: []judge.Question{tooMany}})
	require.ErrorIs(t, err, judge.ErrInvalid, "the service takes at most 10 score levels")
	assert.Empty(t, f.reqs)
}

func TestKeyNotSentToNonDefaultEndpoint(t *testing.T) {
	f := &fakeTransport{body: allKindsResponse}
	_, err := jev.New(jev.WithAPIKey(testKey), jev.WithTransport(f),
		jev.WithURL("https://proxy.example.test/api/v1/systemone"))
	require.ErrorIs(t, err, llm.ErrKeyEndpoint)
	assert.NotContains(t, err.Error(), testKey)
	assert.Empty(t, f.reqs)

	// Plain http to a remote host is refused as well.
	_, err = jev.New(jev.WithAPIKey(testKey), jev.WithTransport(f),
		jev.WithURL("http://openrouter.ai/api/v1/systemone"))
	require.ErrorIs(t, err, llm.ErrKeyEndpoint)
}

func TestLoopbackEndpointAllowed(t *testing.T) {
	f := &fakeTransport{body: allKindsResponse}
	j, err := jev.New(jev.WithAPIKey(testKey), jev.WithTransport(f),
		jev.WithURL("http://127.0.0.1:9/api/v1/systemone"))
	require.NoError(t, err)
	_, err = j.Judge(context.Background(), allKindsCase())
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:9/api/v1/systemone", f.last(t).url)
}

func TestKeyFromEnvironment(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", testKey)
	f := &fakeTransport{body: allKindsResponse}
	j, err := jev.New(jev.WithTransport(f))
	require.NoError(t, err)
	_, err = j.Judge(context.Background(), allKindsCase())
	require.NoError(t, err)
	assert.Equal(t, "Bearer "+testKey, f.last(t).headers.Get("Authorization"))
}

func TestMissingKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("LLM_API_KEY", "")
	_, err := jev.New(jev.WithTransport(&fakeTransport{}))
	require.Error(t, err)
	assert.ErrorIs(t, err, llm.ErrMissingKey)
}

func TestErrorStatuses(t *testing.T) {
	c := judge.Case{State: state, Questions: []judge.Question{qYesNo}}
	tests := []struct {
		name   string
		status int
		header http.Header
		check  func(t *testing.T, err error)
	}{
		{"401", 401, nil, func(t *testing.T, err error) {
			var e *llmerrors.ErrAuth
			require.ErrorAs(t, err, &e)
			assert.Equal(t, "jev", e.Provider)
			assert.False(t, llmerrors.IsFallbackable(err))
		}},
		{"403", 403, nil, func(t *testing.T, err error) {
			var e *llmerrors.ErrPermissionDenied
			require.ErrorAs(t, err, &e)
		}},
		{"404", 404, nil, func(t *testing.T, err error) {
			var e *llmerrors.ErrModel
			require.ErrorAs(t, err, &e)
			assert.Equal(t, "typesafe/jev-1.14", e.Model)
		}},
		{"429", 429, http.Header{"Retry-After": {"7"}}, func(t *testing.T, err error) {
			var e *llmerrors.ErrRateLimit
			require.ErrorAs(t, err, &e)
			assert.Equal(t, 7*time.Second, e.RetryAfter)
			assert.True(t, llmerrors.IsFallbackable(err))
		}},
		{"429 no header", 429, nil, func(t *testing.T, err error) {
			var e *llmerrors.ErrRateLimit
			require.ErrorAs(t, err, &e)
			assert.Zero(t, e.RetryAfter)
		}},
		{"500", 500, nil, func(t *testing.T, err error) {
			var e *llmerrors.HTTPStatusError
			require.ErrorAs(t, err, &e)
			assert.Equal(t, 500, e.StatusCode)
			assert.True(t, llmerrors.IsFallbackable(err))
		}},
		{"503", 503, nil, func(t *testing.T, err error) {
			assert.True(t, llmerrors.IsFallbackable(err))
		}},
		{"422 carries detail, no key", 422, nil, func(t *testing.T, err error) {
			assert.Contains(t, err.Error(), "422")
			assert.Contains(t, err.Error(), "criteria")
			assert.NotContains(t, err.Error(), testKey)
		}},
		{"302 is not followed", 302, http.Header{"Location": {"https://evil.example.test/"}}, func(t *testing.T, err error) {
			require.Error(t, err)
			assert.Contains(t, err.Error(), "302")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeTransport{status: tt.status, header: tt.header,
				body: `{"detail":[{"loc":["body","questions","billing","criteria"],"msg":"bad","type":"x"}]}`}
			_, err := newJudge(t, f).Judge(context.Background(), c)
			require.Error(t, err)
			tt.check(t, err)
			assert.Len(t, f.reqs, 1, "no retry inside the backend")
		})
	}
}

func TestTransportError(t *testing.T) {
	f := &fakeTransport{err: errors.New("connection reset")}
	_, err := newJudge(t, f).Judge(context.Background(),
		judge.Case{State: state, Questions: []judge.Question{qYesNo}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection reset")
}

func TestContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeTransport{err: context.Canceled}
	_, err := newJudge(t, f).Judge(ctx, judge.Case{State: state, Questions: []judge.Question{qYesNo}})
	var e *llmerrors.ErrContext
	require.ErrorAs(t, err, &e)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestMalformedResponses(t *testing.T) {
	c := judge.Case{State: state, Questions: []judge.Question{qChoice}}
	tests := map[string]string{
		"not json":         `<html>oops</html>`,
		"no answers":       `{"model":"m","usage":{}}`,
		"missing answer":   `{"model":"m","answers":{},"usage":{}}`,
		"extra answer":     `{"model":"m","answers":{"tone":{"type":"choice","choice":"calm","confidence":1,"probabilities":{"calm":1}},"zzz":{"type":"noul","noul":0.5}},"usage":{}}`,
		"wrong type":       `{"model":"m","answers":{"tone":{"type":"noul","noul":0.5}},"usage":{}}`,
		"unknown choice":   `{"model":"m","answers":{"tone":{"type":"choice","choice":"furious","confidence":1,"probabilities":{}}},"usage":{}}`,
		"unknown prob key": `{"model":"m","answers":{"tone":{"type":"choice","choice":"calm","confidence":1,"probabilities":{"calm":0.5,"furious":0.5}}},"usage":{}}`,
		"negative prob":    `{"model":"m","answers":{"tone":{"type":"choice","choice":"calm","confidence":1,"probabilities":{"calm":1.5,"angry":-0.5}}},"usage":{}}`,
		"zero-mass probs":  `{"model":"m","answers":{"tone":{"type":"choice","choice":"calm","confidence":1,"probabilities":{"calm":0,"angry":0}}},"usage":{}}`,
		"negative cost":    `{"model":"m","answers":{"tone":{"type":"choice","choice":"calm","confidence":1,"probabilities":{"calm":1}}},"usage":{"cost":-1}}`,
		"trailing garbage": `{"model":"m","answers":{"tone":{"type":"choice","choice":"calm","confidence":1,"probabilities":{"calm":1}}},"usage":{}} {}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			f := &fakeTransport{body: body}
			_, err := newJudge(t, f).Judge(context.Background(), c)
			require.ErrorIs(t, err, jev.ErrMalformed)
			assert.NotContains(t, err.Error(), testKey)
		})
	}

	t.Run("noul out of range", func(t *testing.T) {
		f := &fakeTransport{body: `{"model":"m","answers":{"billing":{"type":"noul","noul":1.5}},"usage":{}}`}
		_, err := newJudge(t, f).Judge(context.Background(),
			judge.Case{State: state, Questions: []judge.Question{qYesNo}})
		require.ErrorIs(t, err, jev.ErrMalformed)
	})
	t.Run("noul missing", func(t *testing.T) {
		f := &fakeTransport{body: `{"model":"m","answers":{"billing":{"type":"noul"}},"usage":{}}`}
		_, err := newJudge(t, f).Judge(context.Background(),
			judge.Case{State: state, Questions: []judge.Question{qYesNo}})
		require.ErrorIs(t, err, jev.ErrMalformed)
	})
	t.Run("score key out of range", func(t *testing.T) {
		f := &fakeTransport{body: `{"model":"m","answers":{"urgency":{"type":"score","score":1,"confidence":1,"legend":{},"probabilities":{"0":0.5,"7":0.5}}},"usage":{}}`}
		_, err := newJudge(t, f).Judge(context.Background(),
			judge.Case{State: state, Questions: []judge.Question{qScore}})
		require.ErrorIs(t, err, jev.ErrMalformed)
	})
}

func TestOversizedResponse(t *testing.T) {
	f := &fakeTransport{body: `{"model":"m","pad":"` + strings.Repeat("x", 5<<20) + `"}`}
	_, err := newJudge(t, f).Judge(context.Background(),
		judge.Case{State: state, Questions: []judge.Question{qYesNo}})
	var e *llmerrors.ErrResponseTooLarge
	require.ErrorAs(t, err, &e)
}

func TestImplementsJudge(t *testing.T) {
	f := &fakeTransport{body: allKindsResponse}
	j := newJudge(t, f)
	vs, err := judge.Batch(context.Background(), j, []judge.Case{allKindsCase(), allKindsCase()}, 2)
	require.NoError(t, err)
	assert.Len(t, vs, 2)
}
