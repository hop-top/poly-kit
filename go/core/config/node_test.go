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
	n := &yaml.Node{Kind: yaml.ScalarNode, Value: "hello"}
	assert.Equal(t, "hello", nodeToValue(n))
}

func TestNodeToValue_Sequence(t *testing.T) {
	n := &yaml.Node{
		Kind: yaml.SequenceNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "a"},
			{Kind: yaml.ScalarNode, Value: "b"},
		},
	}
	assert.Equal(t, []string{"a", "b"}, nodeToValue(n))
}

func TestNodeToValue_Mapping(t *testing.T) {
	n := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "k"},
			{Kind: yaml.ScalarNode, Value: "v"},
		},
	}
	got := nodeToValue(n).(map[string]any)
	assert.Equal(t, "v", got["k"])
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
