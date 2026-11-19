package anthropic_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/ai/llm/internal/transporttest"
)

func TestLogprobs_NotSupported(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", string(anthropicResponse("x", "end_turn", 1, 1)))
	p, err := llm.Resolve("anthropic://claude-sonnet-4-5?api_key=k&base_url=" + fake.URL)
	require.NoError(t, err)
	defer p.Close()
	_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "judge"}},
		Logprobs: &llm.LogprobsConfig{},
	})
	var capErr *llmerrors.ErrCapabilityNotSupported
	require.True(t, errors.As(err, &capErr), "got %v", err)
	assert.Equal(t, "logprobs", capErr.Capability)
	assert.Nil(t, fake.Body())
	_, ok := p.(llm.LogprobCompleter)
	assert.False(t, ok)
}
