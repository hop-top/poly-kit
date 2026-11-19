package routellm_test

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
	_ "hop.top/kit/go/ai/llm/routellm"
)

// A routed model is not known ahead; a schema is refused, unsent.
func TestStructured_NotSupported(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", `{}`)
	p, err := llm.Resolve("routellm://m?base_url=" + fake.URL)
	require.NoError(t, err)
	defer p.Close()

	req := llm.Request{
		Messages:       []llm.Message{{Role: "user", Content: "judge"}},
		ResponseFormat: &llm.ResponseFormat{Schema: json.RawMessage(transporttest.VerdictSchema)},
	}
	_, err = p.(llm.Completer).Complete(context.Background(), req)
	var capErr *llmerrors.ErrCapabilityNotSupported
	require.True(t, errors.As(err, &capErr), "got %v", err)
	assert.Equal(t, "response_format", capErr.Capability)
	assert.False(t, llmerrors.IsFallbackable(err))

	_, err = p.(llm.Streamer).Stream(context.Background(), req)
	require.True(t, errors.As(err, &capErr))
	_, err = p.(llm.ToolCaller).CallWithTools(context.Background(), req, nil)
	require.True(t, errors.As(err, &capErr))
	assert.Nil(t, fake.Body(), "nothing was sent")
}
