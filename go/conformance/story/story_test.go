package story_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/conformance/story"
	"hop.top/kit/go/conformance/story/schema"
)

const sampleStory = `schema_version: "1"
story_id: spaced.launch.dry-run-walkthrough
title: Preview a launch
binary: spaced
intent: An operator wants to preview a spaced launch before they commit it, end to end.
steps:
  - id: preview
    invoke: ["spaced", "launch", "--dry-run"]
    capture: [exit_code, stdout]
`

func writeStory(t *testing.T, dir, name, src string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(src), 0644))
	return p
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	writeStory(t, dir, "a.yaml", sampleStory)
	writeStory(t, dir, "b.yml", sampleStory)
	writeStory(t, dir, "ignore.txt", "not a story")
	// Hidden directory should be pruned.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".hidden"), 0755))
	writeStory(t, filepath.Join(dir, ".hidden"), "hidden.yaml", sampleStory)

	paths, err := story.Discover(dir)
	require.NoError(t, err)
	assert.Len(t, paths, 2)
	for _, p := range paths {
		assert.NotContains(t, p, ".hidden")
	}
}

func TestIndexUniqueIDs(t *testing.T) {
	dir := t.TempDir()
	writeStory(t, dir, "a.yaml", sampleStory)
	idx, err := story.Index(dir)
	require.NoError(t, err)
	assert.Len(t, idx, 1)
	_, ok := idx["spaced.launch.dry-run-walkthrough"]
	assert.True(t, ok)
}

func TestIndexDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	writeStory(t, dir, "a.yaml", sampleStory)
	writeStory(t, dir, "b.yaml", sampleStory)
	idx, err := story.Index(dir)
	require.NoError(t, err)
	// Index keeps first; second is silently dropped. Adopters who
	// want to detect dupes use the validator, not the index.
	assert.Len(t, idx, 1)
}

func TestContentSHA256Stable(t *testing.T) {
	dir := t.TempDir()
	p := writeStory(t, dir, "a.yaml", sampleStory)
	s1, err := story.ReadStory(p)
	require.NoError(t, err)
	s2, err := story.ReadStory(p)
	require.NoError(t, err)
	h1, err := story.ContentSHA256(s1)
	require.NoError(t, err)
	h2, err := story.ContentSHA256(s2)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "same content should hash to the same digest")
	assert.Len(t, h1, 64, "sha256 hex is 64 chars")
}

func TestContentSHA256Differs(t *testing.T) {
	s1 := &schema.Story{SchemaVersion: "1", StoryID: "a.b.c", Title: "t", Binary: "x", Intent: "i", Steps: []schema.Step{{ID: "s1", Invoke: []string{"x"}}}}
	s2 := &schema.Story{SchemaVersion: "1", StoryID: "a.b.d", Title: "t", Binary: "x", Intent: "i", Steps: []schema.Step{{ID: "s1", Invoke: []string{"x"}}}}
	h1, err := story.ContentSHA256(s1)
	require.NoError(t, err)
	h2, err := story.ContentSHA256(s2)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2)
}

func TestContentSHA256NilGuard(t *testing.T) {
	_, err := story.ContentSHA256(nil)
	require.Error(t, err)
}
