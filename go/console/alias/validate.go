package alias

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// NameErrorKind names the rule an alias name failed.
type NameErrorKind string

// The rules ValidateName applies, in order.
const (
	// NameEmpty: the name is the empty string.
	NameEmpty NameErrorKind = "empty"
	// NameWhitespace: the name holds a Unicode White_Space character.
	NameWhitespace NameErrorKind = "whitespace"
	// NameBuiltin: the name equals one of NameRules.Builtins.
	NameBuiltin NameErrorKind = "builtin"
	// NameDuplicate: the name is already a key of NameRules.Existing.
	NameDuplicate NameErrorKind = "duplicate"
)

// NameError reports an alias name ValidateName refused.
type NameError struct {
	Kind NameErrorKind
	Name string
	// Target is the existing alias's target for NameDuplicate.
	Target string
}

// Error renders the texts Root.Alias uses for the same rules.
func (e *NameError) Error() string {
	switch e.Kind {
	case NameBuiltin:
		return fmt.Sprintf("alias %q collides with command %q", e.Name, e.Name)
	case NameDuplicate:
		return fmt.Sprintf("alias %q already registered for %q", e.Name, e.Target)
	default:
		return fmt.Sprintf("alias name %q must be non-empty without whitespace", e.Name)
	}
}

// NameRules is what ValidateName checks a name against beyond its
// spelling. Both fields are optional.
type NameRules struct {
	// Builtins are names an alias must not take: the tool's own
	// commands and anything else it reserves. Compared exactly.
	Builtins []string
	// Existing is the alias map the name would join.
	Existing map[string]string
}

// ValidateName checks an alias name without a store, a config file or a
// command tree. Rules run in order and the first failure is returned as
// a *NameError: empty, whitespace (any Unicode White_Space character),
// builtin, duplicate.
func ValidateName(name string, rules NameRules) error {
	if name == "" {
		return &NameError{Kind: NameEmpty, Name: name}
	}
	if strings.IndexFunc(name, unicode.IsSpace) >= 0 {
		return &NameError{Kind: NameWhitespace, Name: name}
	}
	if slices.Contains(rules.Builtins, name) {
		return &NameError{Kind: NameBuiltin, Name: name}
	}
	if target, ok := rules.Existing[name]; ok {
		return &NameError{Kind: NameDuplicate, Name: name, Target: target}
	}
	return nil
}

// DetectCycles reports the alias cycles in aliases (name -> target). An
// alias points at another when the first word of its target is a key of
// the map. Each cycle is a path that starts at its smallest name,
// follows the aliases and ends with that name again ([a b a]); cycles
// are sorted by first name. An alias that only leads into a cycle is not
// part of it. Nil when there is none.
func DetectCycles(aliases map[string]string) [][]string {
	next := func(name string) (string, bool) {
		words := strings.Fields(aliases[name])
		if len(words) == 0 {
			return "", false
		}
		_, ok := aliases[words[0]]
		return words[0], ok
	}

	names := make([]string, 0, len(aliases))
	for n := range aliases {
		names = append(names, n)
	}
	slices.Sort(names)

	done := make(map[string]bool, len(aliases))
	var cycles [][]string
	for _, start := range names {
		if done[start] {
			continue
		}
		// Walk until the path repeats a name or leaves the map; the
		// repeat closes a cycle when it is on this walk.
		pos := map[string]int{}
		var walk []string
		cur, ok := start, true
		for ok && !done[cur] {
			if i, seen := pos[cur]; seen {
				cycles = append(cycles, canonicalCycle(walk[i:]))
				break
			}
			pos[cur] = len(walk)
			walk = append(walk, cur)
			cur, ok = next(cur)
		}
		for _, n := range walk {
			done[n] = true
		}
	}
	slices.SortFunc(cycles, func(a, b []string) int { return strings.Compare(a[0], b[0]) })
	return cycles
}

// canonicalCycle rotates members to start at the smallest name and
// closes the path with it.
func canonicalCycle(members []string) []string {
	minAt := 0
	for i, n := range members {
		if n < members[minAt] {
			minAt = i
		}
	}
	out := make([]string, 0, len(members)+1)
	out = append(out, members[minAt:]...)
	out = append(out, members[:minAt]...)
	return append(out, members[minAt])
}
