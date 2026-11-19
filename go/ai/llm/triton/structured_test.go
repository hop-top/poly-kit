package triton_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	_ "hop.top/kit/go/ai/llm/triton"
)

// Triton scores; it makes no completions, so there is no schema to
// constrain, and CompleteStructured cannot be handed one.
func TestStructured_NotACompleter(t *testing.T) {
	p, err := llm.Resolve("triton://m?base_url=http://127.0.0.1:9")
	require.NoError(t, err)
	defer p.Close()
	_, ok := p.(llm.Completer)
	assert.False(t, ok)
	c := llm.NewClient(p)
	_, err = c.Complete(t.Context(), llm.Request{ResponseFormat: &llm.ResponseFormat{}})
	assert.Error(t, err)
}
