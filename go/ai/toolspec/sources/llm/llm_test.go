package llm

import (
	"context"
	"errors"
	"testing"

	kitllm "hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/toolspec"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCompleter stubs kitllm.Completer for testing.
type mockCompleter struct {
	response string
	err      error
}

func (m *mockCompleter) Complete(_ context.Context, _ kitllm.Request) (kitllm.Response, error) {
	return kitllm.Response{Content: m.response}, m.err
}

func TestLLMSource_FillsErrorPatterns(t *testing.T) {
	mock := &mockCompleter{
		response: `{
  "error_patterns": [
    {"pattern": "command not found", "fix": "install the tool", "source": "llm"}
  ],
  "workflows": [
    {"name": "basic usage", "steps": ["run init", "run build"]}
  ]
}`,
	}

	src := NewLLMSource(Config{Client: mock, Enabled: true})
	spec, err := src.Resolve("mytool")
	require.NoError(t, err)
	require.NotNil(t, spec)

	assert.Equal(t, "mytool", spec.Name)

	require.Len(t, spec.ErrorPatterns, 1)
	assert.Equal(t, "command not found", spec.ErrorPatterns[0].Pattern)
	assert.Equal(t, "install the tool", spec.ErrorPatterns[0].Fix)
	assert.Equal(t, "llm", spec.ErrorPatterns[0].Source)

	require.Len(t, spec.Workflows, 1)
	assert.Equal(t, "basic usage", spec.Workflows[0].Name)
	assert.Equal(t, []string{"run init", "run build"}, spec.Workflows[0].Steps)
}

func TestLLMSource_SkipsPopulatedFields(t *testing.T) {
	// LLM source only fills ErrorPatterns and Workflows; Commands and Flags
	// are never populated, confirming partial-data behavior.
	mock := &mockCompleter{
		response: `{
  "error_patterns": [{"pattern": "err", "fix": "fix it", "source": "llm"}],
  "workflows": []
}`,
	}

	src := NewLLMSource(Config{Client: mock, Enabled: true})
	spec, err := src.Resolve("git")
	require.NoError(t, err)
	require.NotNil(t, spec)

	assert.Empty(t, spec.Commands, "LLM source must not populate Commands")
	assert.Empty(t, spec.Flags, "LLM source must not populate Flags")
	assert.Len(t, spec.ErrorPatterns, 1)
}

func TestLLMSource_DisabledReturnsNil(t *testing.T) {
	src := NewLLMSource(Config{
		Client:  &mockCompleter{response: "should not be called"},
		Enabled: false,
	})
	spec, err := src.Resolve("anything")
	assert.NoError(t, err)
	assert.Nil(t, spec)
}

func TestLLMSource_StripsMarkdownFences(t *testing.T) {
	mock := &mockCompleter{
		response: "```json\n{\"error_patterns\": [], \"workflows\": []}\n```",
	}

	src := NewLLMSource(Config{Client: mock, Enabled: true})
	spec, err := src.Resolve("tool")
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.Equal(t, "tool", spec.Name)
}

func TestLLMSource_CompleteError(t *testing.T) {
	mock := &mockCompleter{err: errors.New("api down")}

	src := NewLLMSource(Config{Client: mock, Enabled: true})
	spec, err := src.Resolve("tool")
	assert.Nil(t, spec)
	assert.ErrorContains(t, err, "api down")
}

func TestLLMSource_ImplementsSource(t *testing.T) {
	var _ toolspec.Source = (*LLMSource)(nil)
}
