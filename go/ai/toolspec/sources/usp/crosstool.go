package usp

import "hop.top/kit/go/ai/toolspec"

// CrossToolPattern defines a named cross-tool workflow pattern.
type CrossToolPattern struct {
	Name     string   // e.g. "pr-merge-flow"
	Sequence []string // ordered command keys, e.g. ["gh pr merge", "git pull"]
}

// DefaultPatterns returns the built-in cross-tool patterns.
func DefaultPatterns() []CrossToolPattern {
	return []CrossToolPattern{
		{
			Name:     "pr-merge-flow",
			Sequence: []string{"gh pr merge", "git pull"},
		},
		{
			Name:     "feature-branch",
			Sequence: []string{"git push", "gh pr create"},
		},
		{
			Name:     "container-publish",
			Sequence: []string{"docker build", "docker push"},
		},
		{
			Name:     "test-then-commit",
			Sequence: []string{"go test", "git commit"},
		},
		{
			Name:     "branch-and-push",
			Sequence: []string{"git checkout", "git push"},
		},
	}
}

// DetectCrossToolWorkflows scans a TransitionMap for known cross-tool
// patterns and returns matching workflows.
func DetectCrossToolWorkflows(
	tm TransitionMap, patterns []CrossToolPattern,
) []toolspec.Workflow {
	if patterns == nil {
		patterns = DefaultPatterns()
	}

	var workflows []toolspec.Workflow
	for _, p := range patterns {
		if matchesPattern(tm, p.Sequence) {
			wf := toolspec.Workflow{
				Name:  p.Name,
				Steps: make([]string, len(p.Sequence)),
				After: make(map[string][]string),
			}
			copy(wf.Steps, p.Sequence)
			for i := 0; i < len(p.Sequence)-1; i++ {
				wf.After[p.Sequence[i]] = []string{p.Sequence[i+1]}
			}
			workflows = append(workflows, wf)
		}
	}
	return workflows
}

// matchesPattern checks that every consecutive pair in the sequence
// exists as a transition in the map.
func matchesPattern(tm TransitionMap, seq []string) bool {
	for i := 0; i < len(seq)-1; i++ {
		src, dst := seq[i], seq[i+1]
		dsts, ok := tm[src]
		if !ok {
			return false
		}
		if _, ok := dsts[dst]; !ok {
			return false
		}
	}
	return true
}

// DetectAndMerge detects cross-tool workflows and appends them to
// an existing ToolSpec. Duplicate workflow names are skipped.
func DetectAndMerge(
	spec *toolspec.ToolSpec,
	tm TransitionMap,
	patterns []CrossToolPattern,
) *toolspec.ToolSpec {
	if spec == nil {
		spec = &toolspec.ToolSpec{}
	}

	detected := DetectCrossToolWorkflows(tm, patterns)
	if len(detected) == 0 {
		return spec
	}

	existing := make(map[string]bool, len(spec.Workflows))
	for _, wf := range spec.Workflows {
		existing[wf.Name] = true
	}

	for _, wf := range detected {
		if !existing[wf.Name] {
			spec.Workflows = append(spec.Workflows, wf)
			existing[wf.Name] = true
		}
	}
	return spec
}
