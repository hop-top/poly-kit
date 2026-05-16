package usp

import (
	"sort"
	"strings"

	"hop.top/kit/go/ai/toolspec"
)

const maxAfterSuggestions = 5

// BuildSpec converts a TransitionMap into a ToolSpec with Workflows.
func BuildSpec(tool string, tm TransitionMap) *toolspec.ToolSpec {
	spec := &toolspec.ToolSpec{Name: tool}

	sameWf := buildToolWorkflow(tool, tm)
	if sameWf != nil {
		spec.Workflows = append(spec.Workflows, *sameWf)
	}

	crossWf := buildCrossToolWorkflow(tool, tm)
	if crossWf != nil {
		spec.Workflows = append(spec.Workflows, *crossWf)
	}

	return spec
}

// buildToolWorkflow creates a Workflow from transitions whose source
// matches tool. After keys are bare subcommands (e.g. "add", "commit")
// because the Workflow is tool-scoped (Name = tool). Values are full
// command strings ("git commit", "git push") so consumers can resolve
// cross-tool targets unambiguously.
func buildToolWorkflow(tool string, tm TransitionMap) *toolspec.Workflow {
	prefix := tool + " "
	after := make(map[string][]string)

	for from, targets := range tm {
		if !strings.HasPrefix(from, prefix) && from != tool {
			continue
		}
		sub := strings.TrimPrefix(from, prefix)
		ranked := rankTargets(targets)
		if len(ranked) > maxAfterSuggestions {
			ranked = ranked[:maxAfterSuggestions]
		}
		after[sub] = ranked
	}
	if len(after) == 0 {
		return nil
	}

	return &toolspec.Workflow{
		Name:  tool,
		Steps: topPath(tool, tm),
		After: after,
	}
}

// buildCrossToolWorkflow collects transitions where the source
// matches tool but the target does not.
func buildCrossToolWorkflow(tool string, tm TransitionMap) *toolspec.Workflow {
	prefix := tool + " "
	after := make(map[string][]string)

	for from, targets := range tm {
		if !strings.HasPrefix(from, prefix) && from != tool {
			continue
		}
		sub := strings.TrimPrefix(from, prefix)

		// Filter to cross-tool targets only.
		cross := make(map[string]int)
		for to, cnt := range targets {
			tTool := strings.SplitN(to, " ", 2)[0]
			if tTool != tool {
				cross[to] = cnt
			}
		}
		if len(cross) == 0 {
			continue
		}
		ranked := rankTargets(cross)
		if len(ranked) > maxAfterSuggestions {
			ranked = ranked[:maxAfterSuggestions]
		}
		after[sub] = ranked
	}
	if len(after) == 0 {
		return nil
	}

	return &toolspec.Workflow{
		Name:  tool + "-cross-tool",
		Steps: nil,
		After: after,
	}
}

// rankTargets returns target keys sorted by count descending.
func rankTargets(targets map[string]int) []string {
	type kv struct {
		key   string
		count int
	}
	pairs := make([]kv, 0, len(targets))
	for k, v := range targets {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].key < pairs[j].key
	})
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.key
	}
	return out
}

// topPath finds the most common sequence through transitions for the
// given tool using a greedy walk from the highest-count source.
func topPath(tool string, tm TransitionMap) []string {
	prefix := tool + " "

	// Find the source with the highest total outgoing count.
	var bestSrc string
	var bestTotal int
	for from, targets := range tm {
		if !strings.HasPrefix(from, prefix) && from != tool {
			continue
		}
		total := 0
		for _, c := range targets {
			total += c
		}
		if total > bestTotal {
			bestTotal = total
			bestSrc = from
		}
	}
	if bestSrc == "" {
		return nil
	}

	// Greedy walk: always pick the highest-count next step within tool.
	visited := map[string]bool{}
	var steps []string
	cur := bestSrc
	for cur != "" && !visited[cur] {
		visited[cur] = true
		steps = append(steps, cur)
		targets := tm[cur]
		var next string
		var nextCnt int
		for to, cnt := range targets {
			tTool := strings.SplitN(to, " ", 2)[0]
			if tTool != tool {
				continue
			}
			if cnt > nextCnt || (cnt == nextCnt && to < next) {
				next = to
				nextCnt = cnt
			}
		}
		cur = next
	}
	return steps
}
