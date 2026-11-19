package llm_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
)

func TestCheckLogprobs(t *testing.T) {
	assert.NoError(t, llm.CheckLogprobs(nil))
	assert.NoError(t, llm.CheckLogprobs(&llm.LogprobsConfig{}))
	assert.NoError(t, llm.CheckLogprobs(&llm.LogprobsConfig{TopK: llm.MaxTopLogprobs}))
	assert.Error(t, llm.CheckLogprobs(&llm.LogprobsConfig{TopK: -1}))
	assert.Error(t, llm.CheckLogprobs(&llm.LogprobsConfig{TopK: llm.MaxTopLogprobs + 1}))
}

func TestRequestStarted_FlagsLogprobs(t *testing.T) {
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()
	got := capture(t, b, string(llm.TopicRequestStart))
	c := llm.NewClient(&fullProvider{}, llm.WithBus(b))

	_, err := c.Complete(context.Background(), llm.Request{Logprobs: &llm.LogprobsConfig{TopK: 3}})
	require.NoError(t, err)
	_, err = c.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)

	require.Len(t, got(), 2)
	raw, _ := json.Marshal(got()[0])
	assert.Contains(t, string(raw), `"logprobs":true`)
	raw, _ = json.Marshal(got()[1])
	assert.NotContains(t, string(raw), "logprobs")
}

// Tokens spell the reply a piece at a time, beyond a redactor's reach:
// the opt-in response content leaves them out, and a copy is apart.
func TestResponseContentDropsLogprobs(t *testing.T) {
	lp := []llm.TokenLogprob{{Token: "hun", Logprob: -1, Top: []llm.TopLogprob{{Token: "hun", Logprob: -1}}}}
	resp := llm.Response{Content: "ok", Logprobs: lp}

	var seen llm.Response
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()
	c := llm.NewClient(&fullProvider{resp: resp}, llm.WithBus(b),
		llm.WithResponseContent(llm.RedactResponse(redact.New())))
	end := capture(t, b, string(llm.TopicRequestEnd))
	_, err := c.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)
	require.Len(t, end(), 1)
	seen = *end()[0].(llm.RequestEndPayload).Response
	assert.Nil(t, seen.Logprobs)
	assert.Equal(t, lp, resp.Logprobs, "the caller's response is untouched")
}

func TestClonedResponseKeepsLogprobsApart(t *testing.T) {
	lp := []llm.TokenLogprob{{Token: "a", Logprob: -1, Top: []llm.TopLogprob{{Token: "a", Logprob: -1}}}}
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()
	c := llm.NewClient(&fullProvider{resp: llm.Response{Logprobs: lp}}, llm.WithBus(b),
		llm.WithResponseContent(func(r llm.Response) llm.Response {
			r.Logprobs[0].Token = "X"
			r.Logprobs[0].Top[0].Token = "X"
			return r
		}))
	_, err := c.Complete(context.Background(), llm.Request{})
	require.NoError(t, err)
	assert.Equal(t, "a", lp[0].Token)
	assert.Equal(t, "a", lp[0].Top[0].Token)
}
