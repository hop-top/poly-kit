package ollama_test

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

// daemonReply has the shape Ollama 0.40 answers /api/chat with when
// logprobs are asked (probed against a live daemon).
const daemonReply = `{"model":"m","message":{"role":"assistant","content":"yes"},"done":true,` +
	`"logprobs":[` +
	`{"token":"y","logprob":-0.5,"bytes":[121],"top_logprobs":[` +
	`{"token":"y","logprob":-0.5,"bytes":[121]},{"token":"n","logprob":-1.5,"bytes":[110]}]},` +
	`{"token":"es","logprob":-0.25,"bytes":[101,115],"top_logprobs":[` +
	`{"token":"es","logprob":-0.25,"bytes":[101,115]},{"token":"o","logprob":-2.5,"bytes":[111]}]}],` +
	`"eval_count":2,"prompt_eval_count":5}`

func resolveOllama(t *testing.T, base string) llm.Completer {
	t.Helper()
	p, err := llm.Resolve("ollama://m?base_url=" + base)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p.(llm.Completer)
}

func TestLogprobs_WireAndMapping(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", daemonReply)
	c := resolveOllama(t, fake.URL)

	req := structuredReq()
	req.Logprobs = &llm.LogprobsConfig{TopK: 2}
	resp, err := c.Complete(context.Background(), req)
	require.NoError(t, err)

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(fake.Body(), &body))
	assert.JSONEq(t, `true`, string(body["logprobs"]))
	assert.JSONEq(t, `2`, string(body["top_logprobs"]))

	assert.Equal(t, []llm.TokenLogprob{
		{Token: "y", Logprob: -0.5, Top: []llm.TopLogprob{{Token: "y", Logprob: -0.5}, {Token: "n", Logprob: -1.5}}},
		{Token: "es", Logprob: -0.25, Top: []llm.TopLogprob{{Token: "es", Logprob: -0.25}, {Token: "o", Logprob: -2.5}}},
	}, resp.Logprobs)
	assert.Equal(t, "yes", resp.Content)
}

func TestLogprobs_TopKZeroSendsNoTopLogprobs(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", daemonReply)
	c := resolveOllama(t, fake.URL)
	req := structuredReq()
	req.Logprobs = &llm.LogprobsConfig{}
	_, err := c.Complete(context.Background(), req)
	require.NoError(t, err)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(fake.Body(), &body))
	assert.Contains(t, body, "logprobs")
	assert.NotContains(t, body, "top_logprobs")
}

func TestLogprobs_NotAskedNotSent(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", chatReply("hi"))
	c := resolveOllama(t, fake.URL)
	resp, err := c.Complete(context.Background(), structuredReq())
	require.NoError(t, err)
	assert.Nil(t, resp.Logprobs)
	assert.NotContains(t, string(fake.Body()), "logprobs")
}

// An old daemon ignores the field: the reply has tokens, no logprobs.
func TestLogprobs_OldDaemonIsCapabilityError(t *testing.T) {
	for name, reply := range map[string]string{
		"absent": chatReply("hi"),
		"null":   `{"message":{"role":"assistant","content":"hi"},"done":true,"logprobs":null,"eval_count":3}`,
	} {
		t.Run(name, func(t *testing.T) {
			fake := transporttest.NewFake(t, "application/json", reply)
			c := resolveOllama(t, fake.URL)
			req := structuredReq()
			req.Logprobs = &llm.LogprobsConfig{TopK: 1}
			_, err := c.Complete(context.Background(), req)
			var capErr *llmerrors.ErrCapabilityNotSupported
			require.True(t, errors.As(err, &capErr), "got %v", err)
			assert.Equal(t, "logprobs", capErr.Capability)
			assert.False(t, llmerrors.IsFallbackable(err))
		})
	}
}

func TestLogprobs_EmptyReplyIsNotAnError(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json",
		`{"message":{"role":"assistant","content":""},"done":true,"logprobs":[],"eval_count":0}`)
	c := resolveOllama(t, fake.URL)
	req := structuredReq()
	req.Logprobs = &llm.LogprobsConfig{}
	resp, err := c.Complete(context.Background(), req)
	require.NoError(t, err)
	assert.Empty(t, resp.Logprobs)
}

func TestLogprobs_StreamAndBadTopKRefusedUnsent(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", daemonReply)
	c := resolveOllama(t, fake.URL)

	req := structuredReq()
	req.Logprobs = &llm.LogprobsConfig{TopK: 1}
	_, err := c.(llm.Streamer).Stream(context.Background(), req)
	var capErr *llmerrors.ErrCapabilityNotSupported
	require.True(t, errors.As(err, &capErr))

	for _, k := range []int{-1, llm.MaxTopLogprobs + 1} {
		req.Logprobs = &llm.LogprobsConfig{TopK: k}
		_, err = c.Complete(context.Background(), req)
		require.Error(t, err)
	}
	assert.Nil(t, fake.Body(), "nothing was sent")
}

func TestLogprobs_AdapterAdvertises(t *testing.T) {
	p, err := llm.Resolve("ollama://m")
	require.NoError(t, err)
	defer p.Close()
	lc, ok := p.(llm.LogprobCompleter)
	require.True(t, ok)
	assert.True(t, lc.SupportsLogprobs())
	assert.Contains(t, llm.NewClient(p).Capabilities(), "logprobs")
}
