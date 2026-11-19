package openai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/ai/llm/internal/transporttest"
)

const logprobsCompletion = `{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o",` +
	`"choices":[{"index":0,"message":{"role":"assistant","content":"yes"},"finish_reason":"stop",` +
	`"logprobs":{"content":[` +
	`{"token":"yes","logprob":-0.125,"bytes":[121,101,115],"top_logprobs":[` +
	`{"token":"yes","logprob":-0.125,"bytes":[121,101,115]},{"token":"no","logprob":-2.5,"bytes":[110,111]}]}],` +
	`"refusal":null}}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`

func logprobsReq(topK int) llm.Request {
	return llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "judge"}},
		Logprobs: &llm.LogprobsConfig{TopK: topK},
	}
}

func TestLogprobs_WireAndMapping(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", logprobsCompletion)
	p, err := llm.Resolve("openai://gpt-4o?api_key=k&base_url=" + fake.URL)
	require.NoError(t, err)
	defer p.Close()

	resp, err := p.(llm.Completer).Complete(context.Background(), logprobsReq(2))
	require.NoError(t, err)

	body := wireBody(t, fake)
	assert.JSONEq(t, `true`, string(body["logprobs"]))
	assert.JSONEq(t, `2`, string(body["top_logprobs"]))
	assert.Equal(t, []llm.TokenLogprob{{
		Token: "yes", Logprob: -0.125,
		Top: []llm.TopLogprob{{Token: "yes", Logprob: -0.125}, {Token: "no", Logprob: -2.5}},
	}}, resp.Logprobs)

	_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "judge"}},
	})
	require.NoError(t, err)
	assert.NotContains(t, string(fake.Body()), "logprobs")

	_, err = p.(llm.Completer).Complete(context.Background(), logprobsReq(0))
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(fake.Body(), &m))
	assert.Contains(t, m, "logprobs")
	assert.NotContains(t, m, "top_logprobs")
}

func TestLogprobs_ServerIgnoredIsCapabilityError(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", completionJSON("hi", "assistant", "stop"))
	p, err := llm.Resolve("openai://gpt-4o?api_key=k&base_url=" + fake.URL)
	require.NoError(t, err)
	defer p.Close()
	_, err = p.(llm.Completer).Complete(context.Background(), logprobsReq(1))
	var capErr *llmerrors.ErrCapabilityNotSupported
	require.True(t, errors.As(err, &capErr), "got %v", err)
	assert.Equal(t, "logprobs", capErr.Capability)
}

func TestLogprobs_StreamToolsAndBadTopKRefusedUnsent(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", logprobsCompletion)
	p, err := llm.Resolve("openai://gpt-4o?api_key=k&base_url=" + fake.URL)
	require.NoError(t, err)
	defer p.Close()

	var capErr *llmerrors.ErrCapabilityNotSupported
	_, err = p.(llm.Streamer).Stream(context.Background(), logprobsReq(1))
	require.True(t, errors.As(err, &capErr))
	_, err = p.(llm.ToolCaller).CallWithTools(context.Background(), logprobsReq(1), nil)
	require.True(t, errors.As(err, &capErr))
	_, err = p.(llm.ToolStreamer).StreamWithTools(context.Background(), logprobsReq(1), nil)
	require.True(t, errors.As(err, &capErr))
	_, err = p.(llm.Completer).Complete(context.Background(), logprobsReq(llm.MaxTopLogprobs+1))
	require.Error(t, err)
	assert.Nil(t, fake.Body())
}

func TestLogprobs_Advertised(t *testing.T) {
	p, err := llm.Resolve("openai://gpt-4o?api_key=k")
	require.NoError(t, err)
	defer p.Close()
	lc, ok := p.(llm.LogprobCompleter)
	require.True(t, ok)
	assert.True(t, lc.SupportsLogprobs())
}
