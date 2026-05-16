package usp

import (
	"testing"

	"hop.top/kit/go/ai/toolspec"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectCrossToolWorkflows_PRMergeFlow(t *testing.T) {
	tm := TransitionMap{
		"gh pr merge": {"git pull": 3},
	}

	wfs := DetectCrossToolWorkflows(tm, nil)
	require.Len(t, wfs, 1)
	assert.Equal(t, "pr-merge-flow", wfs[0].Name)
	assert.Equal(t, []string{"gh pr merge", "git pull"}, wfs[0].Steps)
	assert.Equal(t, []string{"git pull"}, wfs[0].After["gh pr merge"])
}

func TestDetectCrossToolWorkflows_FeatureBranch(t *testing.T) {
	tm := TransitionMap{
		"git push": {"gh pr create": 5},
	}

	wfs := DetectCrossToolWorkflows(tm, nil)
	require.Len(t, wfs, 1)
	assert.Equal(t, "feature-branch", wfs[0].Name)
}

func TestDetectCrossToolWorkflows_ContainerPublish(t *testing.T) {
	tm := TransitionMap{
		"docker build": {"docker push": 4},
	}

	wfs := DetectCrossToolWorkflows(tm, nil)
	require.Len(t, wfs, 1)
	assert.Equal(t, "container-publish", wfs[0].Name)
}

func TestDetectCrossToolWorkflows_NoMatch(t *testing.T) {
	tm := TransitionMap{
		"npm install": {"npm test": 2},
	}

	wfs := DetectCrossToolWorkflows(tm, nil)
	assert.Empty(t, wfs)
}

func TestDetectCrossToolWorkflows_MultipleMatches(t *testing.T) {
	tm := TransitionMap{
		"gh pr merge":  {"git pull": 3},
		"git push":     {"gh pr create": 5},
		"docker build": {"docker push": 4},
	}

	wfs := DetectCrossToolWorkflows(tm, nil)
	assert.Len(t, wfs, 3)

	names := make(map[string]bool)
	for _, wf := range wfs {
		names[wf.Name] = true
	}
	assert.True(t, names["pr-merge-flow"])
	assert.True(t, names["feature-branch"])
	assert.True(t, names["container-publish"])
}

func TestDetectCrossToolWorkflows_CustomPatterns(t *testing.T) {
	tm := TransitionMap{
		"npm test": {"npm publish": 2},
	}

	custom := []CrossToolPattern{
		{Name: "publish-flow", Sequence: []string{"npm test", "npm publish"}},
	}

	wfs := DetectCrossToolWorkflows(tm, custom)
	require.Len(t, wfs, 1)
	assert.Equal(t, "publish-flow", wfs[0].Name)
}

func TestDetectAndMerge_AppendsWorkflows(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "git",
		Workflows: []toolspec.Workflow{
			{Name: "existing", Steps: []string{"git add", "git commit"}},
		},
	}

	tm := TransitionMap{
		"gh pr merge": {"git pull": 3},
	}

	result := DetectAndMerge(spec, tm, nil)
	assert.Len(t, result.Workflows, 2)
	assert.Equal(t, "existing", result.Workflows[0].Name)
	assert.Equal(t, "pr-merge-flow", result.Workflows[1].Name)
}

func TestDetectAndMerge_SkipsDuplicates(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "git",
		Workflows: []toolspec.Workflow{
			{Name: "pr-merge-flow", Steps: []string{"gh pr merge", "git pull"}},
		},
	}

	tm := TransitionMap{
		"gh pr merge": {"git pull": 3},
	}

	result := DetectAndMerge(spec, tm, nil)
	assert.Len(t, result.Workflows, 1) // no duplicate added
}

func TestDetectAndMerge_NilSpec(t *testing.T) {
	tm := TransitionMap{
		"docker build": {"docker push": 4},
	}

	result := DetectAndMerge(nil, tm, nil)
	require.NotNil(t, result)
	assert.Len(t, result.Workflows, 1)
}

func TestDetectAndMerge_NoMatches(t *testing.T) {
	spec := &toolspec.ToolSpec{Name: "git"}
	tm := TransitionMap{
		"npm install": {"npm test": 2},
	}

	result := DetectAndMerge(spec, tm, nil)
	assert.Empty(t, result.Workflows)
}

func TestDefaultPatterns_NotEmpty(t *testing.T) {
	patterns := DefaultPatterns()
	assert.NotEmpty(t, patterns)
	for _, p := range patterns {
		assert.NotEmpty(t, p.Name)
		assert.True(t, len(p.Sequence) >= 2,
			"pattern %s should have at least 2 steps", p.Name)
	}
}
