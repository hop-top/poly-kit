// Package rules holds the scenario-rule data model + the four
// matchers that decide whether a parsed YAML document is
// scenario-shaped per design.md §1.
//
// The engine is pure: it does not read files, parse YAML, or write
// output. Callers pass a parsed *yaml.Node and a loaded *Set; the
// engine returns a list of Findings. This separation lets the
// scanner, the markdown extractor, and the formatter evolve
// independently of the rule semantics.
package rules

import (
	"gopkg.in/yaml.v3"

	"hop.top/kit/go/conformance/scenariorules"
)

// Set is the in-memory form of contracts/scenario-rules.json. Loaded
// by package loader; consumed by the matchers below.
type Set struct {
	SchemaVersion string
	RulesVersion  string
	Verbs         map[string]struct{}
	TopLevelKeys  map[string]struct{}
	Rules         []Rule
}

// Rule is a single compound detection rule. Kind is a closed enum
// matched in Apply; unknown kinds in the JSON cause loader to fail
// (we never silently ignore a rule we don't understand).
type Rule struct {
	ID          string
	Description string
	Kind        RuleKind
	// Kind-specific configuration. Only the field corresponding to
	// Kind is consulted; loader populates exactly one.
	Key      string   // key_at_root
	Keys     []string // any_key_in_set
	MinCount int      // assertions_list_verbs
}

// RuleKind is the closed enum of supported rule shapes.
type RuleKind string

const (
	KindKeyAtRoot                    RuleKind = "key_at_root"
	KindAnyKeyInSet                  RuleKind = "any_key_in_set"
	KindAssertionsListVerbs          RuleKind = "assertions_list_verbs"
	KindJudgeBlockWithPromptAndScore RuleKind = "judge_block_with_prompt_and_score"
)

// ErrUnknownRuleKind is returned by loader when the JSON declares a
// kind the engine does not implement. We bias toward failing loud so
// a stale binary can't silently false-negative on a new scenario
// shape.
//
// This is an alias to scenariorules.ErrUnknownRuleKind — both
// loaders share the same wire-format and therefore the same
// upgrade-the-binary sentinel.
var ErrUnknownRuleKind = scenariorules.ErrUnknownRuleKind

// Finding describes a single rule match against a parsed document.
// Line is the YAML node line (1-based) where the offending key was
// found; the scanner adjusts this for fenced markdown blocks.
type Finding struct {
	RuleID      string
	Description string
	MatchedKeys []string
	Line        int
}

// Apply runs every rule in set against doc and returns the matched
// findings. doc must be the document node (yaml.Node with Kind ==
// yaml.DocumentNode) — typically obtained by yaml.Unmarshal into a
// *yaml.Node and indexing Content[0], or by decoding directly into a
// *yaml.Node. A nil or non-mapping root produces zero findings.
func Apply(set *Set, doc *yaml.Node) []Finding {
	if set == nil || doc == nil {
		return nil
	}
	root := rootMapping(doc)
	if root == nil {
		return nil
	}
	var out []Finding
	for _, r := range set.Rules {
		if f, ok := r.match(set, root); ok {
			out = append(out, f)
		}
	}
	return out
}

// match dispatches r against the root mapping. The boolean is true
// when the rule fires.
func (r Rule) match(set *Set, root *yaml.Node) (Finding, bool) {
	switch r.Kind {
	case KindKeyAtRoot:
		return r.matchKeyAtRoot(root)
	case KindAnyKeyInSet:
		return r.matchAnyKeyInSet(root)
	case KindAssertionsListVerbs:
		return r.matchAssertionsListVerbs(set, root)
	case KindJudgeBlockWithPromptAndScore:
		return r.matchJudgeBlock(root)
	}
	return Finding{}, false
}

// matchKeyAtRoot — R1. Fires when r.Key appears at the document root.
func (r Rule) matchKeyAtRoot(root *yaml.Node) (Finding, bool) {
	keyNode, _ := mappingLookup(root, r.Key)
	if keyNode == nil {
		return Finding{}, false
	}
	return Finding{
		RuleID:      r.ID,
		Description: r.Description,
		MatchedKeys: []string{r.Key},
		Line:        keyNode.Line,
	}, true
}

// matchAnyKeyInSet — R3. Fires when any r.Keys entry appears anywhere
// in the document tree. Surveys §3 calls these "novel compound terms"
// — by design we don't restrict to root because the leak channel
// includes nested usage (e.g., per-step assertion blocks).
func (r Rule) matchAnyKeyInSet(root *yaml.Node) (Finding, bool) {
	hits, firstLine := collectKeysAnywhere(root, r.Keys)
	if len(hits) == 0 {
		return Finding{}, false
	}
	return Finding{
		RuleID:      r.ID,
		Description: r.Description,
		MatchedKeys: hits,
		Line:        firstLine,
	}, true
}

// matchAssertionsListVerbs — R2. Fires when root has an "assertions"
// key whose value is a sequence of mapping nodes, and >= MinCount of
// them have a "kind" entry whose scalar value is in set.Verbs.
func (r Rule) matchAssertionsListVerbs(set *Set, root *yaml.Node) (Finding, bool) {
	_, list := mappingLookup(root, "assertions")
	if list == nil || list.Kind != yaml.SequenceNode {
		return Finding{}, false
	}
	min := r.MinCount
	if min <= 0 {
		min = 2
	}
	count := 0
	matchedVerbs := make([]string, 0, len(list.Content))
	for _, item := range list.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		_, kindVal := mappingLookup(item, "kind")
		if kindVal == nil || kindVal.Kind != yaml.ScalarNode {
			continue
		}
		if _, ok := set.Verbs[kindVal.Value]; ok {
			count++
			matchedVerbs = append(matchedVerbs, kindVal.Value)
		}
	}
	if count < min {
		return Finding{}, false
	}
	return Finding{
		RuleID:      r.ID,
		Description: r.Description,
		MatchedKeys: matchedVerbs,
		Line:        list.Line,
	}, true
}

// matchJudgeBlock — R4. Fires when root.judge is a mapping containing
// (prompt or prompt_ref) AND (required_score or model). Bare judge:
// (without those four children) does not fire — it's too common a
// word.
func (r Rule) matchJudgeBlock(root *yaml.Node) (Finding, bool) {
	_, judge := mappingLookup(root, "judge")
	if judge == nil || judge.Kind != yaml.MappingNode {
		return Finding{}, false
	}
	hasPrompt := mappingHas(judge, "prompt") || mappingHas(judge, "prompt_ref")
	hasScore := mappingHas(judge, "required_score") || mappingHas(judge, "model")
	if !hasPrompt || !hasScore {
		return Finding{}, false
	}
	var keys []string
	for _, k := range []string{"prompt", "prompt_ref", "required_score", "model"} {
		if mappingHas(judge, k) {
			keys = append(keys, "judge."+k)
		}
	}
	return Finding{
		RuleID:      r.ID,
		Description: r.Description,
		MatchedKeys: keys,
		Line:        judge.Line,
	}, true
}

// rootMapping returns the mapping node at the root of a YAML
// document, unwrapping a single DocumentNode if present. Returns nil
// for empty or non-mapping documents.
func rootMapping(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// mappingLookup finds the value associated with key in a mapping
// node. yaml.v3 stores mappings as a flat [k1,v1,k2,v2,...] slice in
// Content. Returns (keyNode, valueNode) or (nil, nil) on miss.
func mappingLookup(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return k, m.Content[i+1]
		}
	}
	return nil, nil
}

// mappingHas reports whether m has a child key (any value type).
func mappingHas(m *yaml.Node, key string) bool {
	k, _ := mappingLookup(m, key)
	return k != nil
}

// collectKeysAnywhere walks the tree rooted at n and returns the
// (deduplicated) subset of keys[] that appear as mapping keys, along
// with the earliest line they appear at. Order in the returned
// []string matches keys[].
func collectKeysAnywhere(n *yaml.Node, keys []string) ([]string, int) {
	want := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		want[k] = struct{}{}
	}
	found := map[string]int{} // key -> earliest line
	walk(n, func(node *yaml.Node) {
		if node.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i]
			if k.Kind != yaml.ScalarNode {
				continue
			}
			if _, hit := want[k.Value]; !hit {
				continue
			}
			if prev, seen := found[k.Value]; !seen || k.Line < prev {
				found[k.Value] = k.Line
			}
		}
	})
	if len(found) == 0 {
		return nil, 0
	}
	out := make([]string, 0, len(found))
	earliest := 0
	for _, k := range keys {
		if line, ok := found[k]; ok {
			out = append(out, k)
			if earliest == 0 || line < earliest {
				earliest = line
			}
		}
	}
	return out, earliest
}

// walk invokes fn on n and every descendant, depth-first. Skips nil
// nodes and aliases (alias targets are reached through their own
// node).
func walk(n *yaml.Node, fn func(*yaml.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Content {
		walk(c, fn)
	}
}
