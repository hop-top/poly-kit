package usp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSpec_GitWorkflow(t *testing.T) {
	tm := TransitionMap{
		"git add":    {"git commit": 10, "git status": 2},
		"git commit": {"git push": 8},
		"git push":   {"gh pr": 3},
	}
	spec := BuildSpec("git", tm)
	require.NotNil(t, spec)
	assert.Equal(t, "git", spec.Name)

	// Should have a same-tool workflow.
	require.GreaterOrEqual(t, len(spec.Workflows), 1)
	wf := spec.Workflows[0]
	assert.Equal(t, "git", wf.Name)

	// After map should have entries for add, commit, push.
	assert.Contains(t, wf.After, "add")
	assert.Contains(t, wf.After, "commit")
	assert.Contains(t, wf.After, "push")

	// "add" should rank "git commit" first (count 10 > 2).
	require.NotEmpty(t, wf.After["add"])
	assert.Equal(t, "git commit", wf.After["add"][0])

	// Steps should be the top path: add -> commit -> push.
	assert.Equal(t, []string{"git add", "git commit", "git push"}, wf.Steps)
}

func TestBuildSpec_CrossTool(t *testing.T) {
	tm := TransitionMap{
		"git commit": {"git push": 5, "gh pr": 3},
		"git push":   {"gh pr": 7},
	}
	spec := BuildSpec("git", tm)
	require.NotNil(t, spec)

	// Find the cross-tool workflow.
	var cross *int
	for i, wf := range spec.Workflows {
		if wf.Name == "git-cross-tool" {
			cross = &i
		}
	}
	require.NotNil(t, cross, "expected cross-tool workflow")
	cwf := spec.Workflows[*cross]
	assert.Contains(t, cwf.After, "commit")
	assert.Contains(t, cwf.After, "push")
	assert.Equal(t, "gh pr", cwf.After["push"][0])
}

func TestBuildSpec_RankingCapped(t *testing.T) {
	targets := map[string]int{
		"git a": 6, "git b": 5, "git c": 4,
		"git d": 3, "git e": 2, "git f": 1,
	}
	tm := TransitionMap{
		"git start": targets,
	}
	spec := BuildSpec("git", tm)
	require.NotNil(t, spec)
	require.GreaterOrEqual(t, len(spec.Workflows), 1)
	after := spec.Workflows[0].After["start"]
	assert.LessOrEqual(t, len(after), 5, "should cap at 5 suggestions")
}

func TestBuildSpec_EmptyTransitions(t *testing.T) {
	tm := TransitionMap{}
	spec := BuildSpec("git", tm)
	assert.Equal(t, "git", spec.Name)
	assert.Empty(t, spec.Workflows)
}

func TestBuildSpec_NoMatchingTool(t *testing.T) {
	tm := TransitionMap{
		"docker build": {"docker push": 3},
	}
	spec := BuildSpec("git", tm)
	assert.Equal(t, "git", spec.Name)
	assert.Empty(t, spec.Workflows)
}
