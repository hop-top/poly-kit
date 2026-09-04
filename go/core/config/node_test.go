package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func mustParse(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(src), &doc))
	return &doc
}

func TestWalkPath_Scalar(t *testing.T) {
	doc := mustParse(t, "a:\n  b: val")
	n := walkPath(doc, "a.b")
	require.NotNil(t, n)
	assert.Equal(t, "val", n.Value)
}

func TestWalkPath_Missing(t *testing.T) {
	doc := mustParse(t, "a:\n  b: val")
	assert.Nil(t, walkPath(doc, "a.c"))
}

func TestWalkPath_TopLevel(t *testing.T) {
	doc := mustParse(t, "name: hello")
	n := walkPath(doc, "name")
	require.NotNil(t, n)
	assert.Equal(t, "hello", n.Value)
}

func TestWalkOrCreate_Creates(t *testing.T) {
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	parent, leaf := walkOrCreate(doc, "a.b.c")
	require.NotNil(t, parent)
	assert.Equal(t, "c", leaf)
	assert.Equal(t, yaml.MappingNode, parent.Kind)

	// Verify intermediate "a" was created.
	root := doc.Content[0]
	require.Len(t, root.Content, 2) // key "a", value mapping
	assert.Equal(t, "a", root.Content[0].Value)
}

func TestWalkOrCreate_ScalarToMapping(t *testing.T) {
	doc := mustParse(t, "a: 1")
	parent, leaf := walkOrCreate(doc, "a.b")
	require.NotNil(t, parent)
	assert.Equal(t, "b", leaf)
	assert.Equal(t, yaml.MappingNode, parent.Kind)

	// The former scalar "a" should now be a mapping.
	root := doc.Content[0]
	require.GreaterOrEqual(t, len(root.Content), 2)
	assert.Equal(t, "a", root.Content[0].Value)
	assert.Equal(t, yaml.MappingNode, root.Content[1].Kind)
}

func TestNodeToValue_Scalar(t *testing.T) {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "hello"}
	assert.Equal(t, "hello", nodeToValue(n))
}

// scalarNode parses src as a single-document scalar so the node carries
// the tag yaml.v3 resolves, which is what nodeToValue keys off.
func scalarNode(t *testing.T, src string) *yaml.Node {
	t.Helper()
	doc := mustParse(t, "v: "+src)
	n := walkPath(doc, "v")
	require.NotNil(t, n)
	return n
}

func TestNodeToValue_ScalarTags(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want any
	}{
		{"int", "8080", 8080},
		{"negative int", "-3", -3},
		{"float", "0.9", 0.9},
		{"bool true", "true", true},
		{"bool false", "false", false},
		{"null", "null", nil},
		{"empty is null", "", nil},
		{"bare string", "hello", "hello"},
		{"quoted number stays string", `"8080"`, "8080"},
		// YAML 1.1 booleans are plain strings under the 1.2 core
		// schema that yaml.v3 implements.
		{"yes stays string", "yes", "yes"},
		{"on stays string", "on", "on"},
		{"off stays string", "off", "off"},
		{"no stays string", "no", "no"},
		// Spellings yaml.v3 resolves to !!int / !!float are converted.
		{"hex int", "0x1F", 31},
		{"octal int", "0o17", 15},
		{"underscored int", "1_000", 1000},
		{"float exponent", "1e3", 1000.0},
		// Dates resolve to !!timestamp; kept as strings deliberately.
		{"timestamp stays string", "2024-01-01", "2024-01-01"},
		{"datetime stays string", "2024-01-01T10:20:30Z", "2024-01-01T10:20:30Z"},
		{"explicit timestamp stays string", "!!timestamp 2024-01-01", "2024-01-01"},
		// !!binary would decode to the underlying bytes; the raw base64
		// source text is what the file holds and what callers expect.
		{"binary stays raw base64", "!!binary aGk=", "aGk="},
		{"invalid binary stays raw", `!!binary "not!base64"`, "not!base64"},
		// Unknown tags carry no meaning here, so the text passes through.
		{"custom tag stays string", "!mytype foo", "foo"},
		{"custom uri tag stays string", "!<tag:example.com,2024:x> foo", "foo"},
		// An int past MaxInt64 decodes to uint64, which is outside the
		// documented return set; the literal digits are returned instead.
		{"int overflow stays string", "9223372036854775808", "9223372036854775808"},
		{"max int64 still converts", "9223372036854775807", 9223372036854775807},
		// Explicitly tagged but unconvertible: raw text, not dropped.
		{"bad explicit int stays string", "!!int notanum", "notanum"},
		{"bad explicit bool stays string", "!!bool notabool", "notabool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nodeToValue(scalarNode(t, tt.src)))
		})
	}
}

func TestNodeToValue_Sequence(t *testing.T) {
	doc := mustParse(t, "v: [a, b]")
	n := walkPath(doc, "v")
	require.NotNil(t, n)
	assert.Equal(t, []any{"a", "b"}, nodeToValue(n))
}

func TestNodeToValue_SequenceMixedTypes(t *testing.T) {
	doc := mustParse(t, `v: [1, 2.5, three, true, null, "4"]`)
	n := walkPath(doc, "v")
	require.NotNil(t, n)
	assert.Equal(t, []any{1, 2.5, "three", true, nil, "4"}, nodeToValue(n))
}

func TestNodeToValue_SequenceNonPrimitiveTagsStayStrings(t *testing.T) {
	doc := mustParse(t, `v: [2024-01-01, !!binary aGk=, 9223372036854775808]`)
	n := walkPath(doc, "v")
	require.NotNil(t, n)
	assert.Equal(t, []any{"2024-01-01", "aGk=", "9223372036854775808"}, nodeToValue(n))
}

func TestNodeToValue_Mapping(t *testing.T) {
	n := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "k"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "v"},
		},
	}
	got := nodeToValue(n).(map[string]any)
	assert.Equal(t, "v", got["k"])
}

func TestNodeToValue_NestedMappingPreservesTypes(t *testing.T) {
	doc := mustParse(t, "a:\n  b:\n    port: 8080\n    ratio: 0.5\n    on: true\n")
	n := walkPath(doc, "a")
	require.NotNil(t, n)

	got, ok := nodeToValue(n).(map[string]any)
	require.True(t, ok)
	inner, ok := got["b"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 8080, inner["port"])
	assert.Equal(t, 0.5, inner["ratio"])
	assert.Equal(t, true, inner["on"])
}

func TestCollectLeaves(t *testing.T) {
	doc := mustParse(t, "a:\n  b: 1\n  c: 2\nd: 3")
	leaves := collectLeaves(doc, "")
	keys := make([]string, len(leaves))
	for i, l := range leaves {
		keys[i] = l.Key
	}
	assert.ElementsMatch(t, []string{"a.b", "a.c", "d"}, keys)
}

func TestNodeCache_Caches(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("x: 1"), 0o644))

	c := newNodeCache()
	n1, err := c.get(p)
	require.NoError(t, err)
	n2, err := c.get(p)
	require.NoError(t, err)
	assert.True(t, n1 == n2, "expected same pointer on second get")
}

func TestNodeCache_Invalidate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("x: 1"), 0o644))

	c := newNodeCache()
	n1, err := c.get(p)
	require.NoError(t, err)

	c.invalidate(p)

	n2, err := c.get(p)
	require.NoError(t, err)
	assert.False(t, n1 == n2, "expected fresh pointer after invalidate")
}
