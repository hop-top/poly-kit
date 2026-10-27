package llm_test

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

var allToolNameRules = []llm.ToolNameRule{llm.AnthropicToolNames, llm.OpenAIToolNames, llm.GeminiToolNames}

// Whatever the input, a sanitized name is non-empty, valid for its rule
// and stable.
func TestToolNameSanitizeAlwaysValid(t *testing.T) {
	alphabet := []rune("aZ09_-.:/ é検🔧\t")
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		var b strings.Builder
		n := 1 + r.Intn(200)
		for j := 0; j < n; j++ {
			b.WriteRune(alphabet[r.Intn(len(alphabet))])
		}
		name := b.String()
		for _, rule := range allToolNameRules {
			got, err := rule.Sanitize(name)
			require.NoError(t, err)
			require.True(t, rule.Valid(got), "%s: %q -> %q", rule.Provider, name, got)
			again, _ := rule.Sanitize(name)
			require.Equal(t, got, again)
			if rule.Valid(name) {
				require.Equal(t, name, got)
			}
		}
	}
}

func TestToolNameInvalidUTF8(t *testing.T) {
	bad := "a\xffb"
	_, err := llm.AnthropicToolNames.Sanitize(bad)
	var te *llmerrors.ErrToolName
	require.ErrorAs(t, err, &te)
	assert.Equal(t, llmerrors.ToolNameInvalidUTF8, te.Reason)

	_, err = llm.NewToolNames(llm.AnthropicToolNames, llm.ToolNamesSanitize, []llm.ToolDef{{Name: bad}})
	require.ErrorAs(t, err, &te)
	assert.Equal(t, llmerrors.ToolNameInvalidUTF8, te.Reason)
}

func TestToolNameModeOf(t *testing.T) {
	for in, want := range map[string]llm.ToolNameMode{"sanitize": llm.ToolNamesSanitize, "strict": llm.ToolNamesStrict, " strict ": llm.ToolNamesStrict} {
		got, err := llm.ToolNameModeOf(map[string]string{"tool_names": in})
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	got, err := llm.ToolNameModeOf(nil)
	require.NoError(t, err)
	assert.Equal(t, llm.ToolNamesSanitize, got)
	for _, in := range []string{"", "loose", "STRICT"} {
		_, err := llm.ToolNameModeOf(map[string]string{"tool_names": in})
		assert.Error(t, err, in)
	}
}

func TestToolNamesNilAndCopies(t *testing.T) {
	var none *llm.ToolNames
	assert.Equal(t, "a.b", none.Wire("a.b"))
	assert.Equal(t, "a_b", none.Original("a_b"))

	names, err := llm.NewToolNames(llm.AnthropicToolNames, llm.ToolNamesSanitize, []llm.ToolDef{{Name: "a.b"}})
	require.NoError(t, err)
	defs := []llm.ToolDef{{Name: "a.b", Description: "d"}}
	out := names.Defs(defs)
	assert.Equal(t, "a_b", out[0].Name)
	assert.Equal(t, "d", out[0].Description)
	assert.Equal(t, "a.b", defs[0].Name)

	calls := []llm.ToolCall{{ID: "1", Name: "a_b"}}
	back := names.Calls(calls)
	assert.Equal(t, "a.b", back[0].Name)
	assert.Equal(t, "a_b", calls[0].Name)
}

func TestErrToolNameMessagesAndFallback(t *testing.T) {
	cases := map[string]string{
		llmerrors.ToolNameEmpty:       `tool name is empty (provider "anthropic")`,
		llmerrors.ToolNameInvalid:     `tool name "a.b" is not valid for provider "anthropic"`,
		llmerrors.ToolNameDuplicate:   `tool name "a.b" is defined twice (provider "anthropic")`,
		llmerrors.ToolNameCollision:   `tool names "a:b" and "a.b" map to the same provider name (provider "anthropic")`,
		llmerrors.ToolNameInvalidUTF8: `tool name "a.b" is not valid UTF-8 (provider "anthropic")`,
	}
	for reason, want := range cases {
		err := llmerrors.NewToolName("anthropic", reason, "a.b", "a:b")
		assert.Equal(t, want, err.Error())
		assert.False(t, llmerrors.IsFallbackable(err))
	}
}
