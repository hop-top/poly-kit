package parser_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/conformance/story/parser"
	"hop.top/kit/go/conformance/story/schema"
)

const minimalStory = `schema_version: "1"
story_id: spaced.launch.dry-run
title: Preview a launch
binary: spaced
intent: This is a story about previewing a spaced launch end to end for confidence.
steps:
  - id: preview
    invoke: ["spaced", "launch", "--dry-run"]
    capture: [exit_code, stdout]
`

func TestParseBytesMinimalStory(t *testing.T) {
	ps, err := parser.ParseBytes([]byte(minimalStory), "<test>")
	require.NoError(t, err)
	require.NotNil(t, ps)
	require.NotNil(t, ps.Story)
	assert.Equal(t, schema.SchemaVersionV1, ps.Story.SchemaVersion)
	assert.Equal(t, "spaced.launch.dry-run", ps.Story.StoryID)
	assert.Equal(t, "spaced", ps.Story.Binary)
	require.Len(t, ps.Story.Steps, 1)
	assert.Equal(t, "preview", ps.Story.Steps[0].ID)
	assert.Equal(t, []string{"spaced", "launch", "--dry-run"}, ps.Story.Steps[0].Invoke)
	assert.Equal(t, []string{"exit_code", "stdout"}, ps.Story.Steps[0].Capture)
}

func TestParseBytesUnknownTopLevelKeyRejected(t *testing.T) {
	// scenario_id at root: structurally a scenario, not a story.
	src := minimalStory + "scenario_id: oops\n"
	_, err := parser.ParseBytes([]byte(src), "<test>")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "field scenario_id not found") || strings.Contains(err.Error(), `field "scenario_id"`),
		"expected unknown-key error mentioning scenario_id; got %v", err)
}

func TestParseBytesUnknownStepKeyRejected(t *testing.T) {
	src := `schema_version: "1"
story_id: x.y.z
title: t
binary: spaced
intent: A reasonably long intent that satisfies the 40-char minimum. ` + strings.Repeat("x", 10) + `
steps:
  - id: preview
    invoke: ["spaced"]
    expected: oops
`
	_, err := parser.ParseBytes([]byte(src), "<test>")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected")
}

func TestParseBytesAssertionsAtRootRejected(t *testing.T) {
	src := minimalStory + `assertions:
  - kind: exit_code_equals
    value: 0
`
	_, err := parser.ParseBytes([]byte(src), "<test>")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "assertions")
}

func TestParseBytesJudgeBlockRejected(t *testing.T) {
	src := minimalStory + `judge:
  prompt: rate this
  required_score: 0.8
`
	_, err := parser.ParseBytes([]byte(src), "<test>")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "judge")
}

func TestParseBytesCassetteMustContainAsTopLevelKeyRejected(t *testing.T) {
	src := minimalStory + `cassette_must_contain: ["x"]
`
	_, err := parser.ParseBytes([]byte(src), "<test>")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cassette_must_contain")
}

func TestParseBytesMalformedYAML(t *testing.T) {
	_, err := parser.ParseBytes([]byte("schema_version: \"1\"\n  bad_indent: yes\n"), "<test>")
	require.Error(t, err)
}

func TestParseBytesEmpty(t *testing.T) {
	// Empty input is an EOF for yaml.v3's KnownFields-strict decoder —
	// no document, no struct. The parser surfaces this as a parse
	// error; the CLI leaf renders it as a parse-failed finding so
	// adopters get a precise complaint rather than a silent skip.
	_, err := parser.ParseBytes([]byte(""), "<test>")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse story")
}

func TestParseFileMissing(t *testing.T) {
	_, err := parser.ParseFile("/no/such/story.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read story")
}
