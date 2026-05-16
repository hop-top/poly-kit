package usp

import "fmt"

// TransitionMap counts how often one command follows another.
// Key: "tool subcmd" -> "tool subcmd" -> count.
type TransitionMap map[string]map[string]int

// CountTransitions builds a transition map from multiple sessions.
// Each session is a []ParsedCommand sequence. Transitions with a
// total count below minCount are pruned from the result.
func CountTransitions(sessions [][]ParsedCommand, minCount int) TransitionMap {
	tm := make(TransitionMap)
	for _, session := range sessions {
		for i := 0; i+1 < len(session); i++ {
			from := cmdKey(session[i])
			to := cmdKey(session[i+1])
			if tm[from] == nil {
				tm[from] = make(map[string]int)
			}
			tm[from][to]++
		}
	}
	// Prune entries below minCount.
	for from, targets := range tm {
		for to, count := range targets {
			if count < minCount {
				delete(targets, to)
			}
		}
		if len(targets) == 0 {
			delete(tm, from)
		}
	}
	return tm
}

// MergeTransitions adds all counts from src into dst in-place.
func MergeTransitions(dst, src TransitionMap) {
	for from, targets := range src {
		if dst[from] == nil {
			dst[from] = make(map[string]int)
		}
		for to, count := range targets {
			dst[from][to] += count
		}
	}
}

// cmdKey produces the canonical "tool subcmd" key for a parsed command.
func cmdKey(pc ParsedCommand) string {
	if pc.SubCmd == "" {
		return pc.Tool
	}
	return fmt.Sprintf("%s %s", pc.Tool, pc.SubCmd)
}
