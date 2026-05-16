package mdfence_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/conformance/verifynoleak/mdfence"
)

func TestExtract_NoFences_ReturnsEmpty(t *testing.T) {
	out := mdfence.Extract([]byte("just prose, no fences here\nat all\n"))
	assert.Empty(t, out)
}

func TestExtract_SingleYAMLFence(t *testing.T) {
	src := "intro\n" +
		"```yaml\n" +
		"scenario_id: x\n" +
		"```\n" +
		"outro\n"
	out := mdfence.Extract([]byte(src))
	require.Len(t, out, 1)
	assert.Equal(t, 2, out[0].StartLine, "opening fence is on line 2")
	assert.Equal(t, 4, out[0].EndLine, "closing fence is on line 4")
	assert.Equal(t, "scenario_id: x\n", string(out[0].Content))
}

func TestExtract_YmlAlias(t *testing.T) {
	src := "```yml\nkey: value\n```\n"
	out := mdfence.Extract([]byte(src))
	require.Len(t, out, 1)
	assert.Equal(t, "key: value\n", string(out[0].Content))
}

func TestExtract_CaseInsensitiveTag(t *testing.T) {
	for _, tag := range []string{"YAML", "Yaml", "yAmL", "YML"} {
		src := "```" + tag + "\nkey: v\n```\n"
		out := mdfence.Extract([]byte(src))
		require.Len(t, out, 1, "tag %q", tag)
	}
}

func TestExtract_IgnoresNonYAMLFences(t *testing.T) {
	src := "```go\npackage x\n```\n" +
		"```python\nx = 1\n```\n" +
		"```\nplain block\n```\n"
	out := mdfence.Extract([]byte(src))
	assert.Empty(t, out, "non-YAML languages must not be extracted")
}

func TestExtract_MultipleBlocksAreIndependent(t *testing.T) {
	src := "```yaml\nfirst: doc\n```\n" +
		"between prose\n" +
		"```yaml\nsecond: doc\n```\n"
	out := mdfence.Extract([]byte(src))
	require.Len(t, out, 2)
	assert.Equal(t, "first: doc\n", string(out[0].Content))
	assert.Equal(t, "second: doc\n", string(out[1].Content))
}

func TestExtract_NestedFenceInsideNonYAML_SkipsInner(t *testing.T) {
	// A go code block that contains the literal characters
	// ```yaml — those must NOT be extracted as a YAML block.
	src := "```go\n" +
		"// example: ```yaml is a fence opener\n" +
		"// scenario_id: x\n" +
		"```\n"
	out := mdfence.Extract([]byte(src))
	assert.Empty(t, out, "inner ```yaml inside an outer ```go block must be ignored")
}

func TestExtract_FourBacktickFenceWithThreeBacktickInner(t *testing.T) {
	// CommonMark allows fences of 3+ backticks; closer must match
	// or exceed. A 4-backtick yaml fence can contain literal ``` as
	// content.
	src := "````yaml\n" +
		"# code sample inside scenario\n" +
		"# ```\n" +
		"scenario_id: nested-ok\n" +
		"````\n"
	out := mdfence.Extract([]byte(src))
	require.Len(t, out, 1)
	assert.Contains(t, string(out[0].Content), "scenario_id: nested-ok")
}

func TestExtract_UnclosedFence_EmitsToEOF(t *testing.T) {
	src := "intro\n```yaml\nscenario_id: dangling\nmore: stuff\n"
	out := mdfence.Extract([]byte(src))
	require.Len(t, out, 1)
	assert.Equal(t, 2, out[0].StartLine)
	assert.Equal(t, 4, out[0].EndLine, "endLine should be the last line of source for unclosed fences")
	assert.Contains(t, string(out[0].Content), "scenario_id: dangling")
}

func TestExtract_FenceWithInfoString(t *testing.T) {
	// CommonMark allows ```yaml title="example" or similar metadata
	// after the language tag. Only the first token decides.
	src := "```yaml title=\"example\" id=block-1\n" +
		"scenario_id: x\n" +
		"```\n"
	out := mdfence.Extract([]byte(src))
	require.Len(t, out, 1)
}

func TestExtract_LeadingWhitespaceOnFence(t *testing.T) {
	src := "  ```yaml\n  scenario_id: indented\n  ```\n"
	out := mdfence.Extract([]byte(src))
	require.Len(t, out, 1)
	// Content carries the original lines verbatim; indentation is
	// the caller's problem to interpret (yaml.v3 handles it fine).
	assert.Contains(t, string(out[0].Content), "scenario_id: indented")
}

func TestExtract_LineNumbersAreOneBased(t *testing.T) {
	// Line 1: prose. Line 2: opener. Line 3: content. Line 4: close.
	src := "prose\n```yaml\nk: v\n```\n"
	out := mdfence.Extract([]byte(src))
	require.Len(t, out, 1)
	assert.Equal(t, 2, out[0].StartLine)
	assert.Equal(t, 4, out[0].EndLine)
}

func TestExtract_LargeBlock(t *testing.T) {
	// Stress test the bufio Scanner buffer expansion.
	var b strings.Builder
	b.WriteString("```yaml\n")
	for i := 0; i < 10_000; i++ {
		b.WriteString("entry_")
		b.WriteString(strings.Repeat("x", 20))
		b.WriteString(": value\n")
	}
	b.WriteString("```\n")
	out := mdfence.Extract([]byte(b.String()))
	require.Len(t, out, 1)
	assert.Greater(t, len(out[0].Content), 100_000)
}

func TestExtract_EmptyYAMLBlock(t *testing.T) {
	src := "```yaml\n```\n"
	out := mdfence.Extract([]byte(src))
	require.Len(t, out, 1)
	assert.Equal(t, "", string(out[0].Content), "empty block content is the empty string, not nil")
}

func TestExtract_ContentIsClone_NotAliased(t *testing.T) {
	// Mutating returned Content must not be observable in subsequent
	// calls. Guards against accidental shared-slice bugs.
	src := []byte("```yaml\nkey: v\n```\n")
	out := mdfence.Extract(src)
	require.Len(t, out, 1)
	out[0].Content[0] = 'X'
	out2 := mdfence.Extract(src)
	require.Len(t, out2, 1)
	assert.Equal(t, byte('k'), out2[0].Content[0], "subsequent Extract must see pristine bytes")
}
