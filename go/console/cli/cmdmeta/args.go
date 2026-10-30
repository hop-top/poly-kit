package cmdmeta

import "strings"

// KeyArgs is the comma-separated list of a command's declared
// positional arguments, in order ([ParseArgs] holds the grammar).
// Cobra does not introspect argument names, so this annotation is
// the only place a command says what its operands are called; the
// manifest, the MCP and REST projections and shell completion all
// read it through ParseArgs so no two readers disagree.
const KeyArgs = "kit/args"

// ArgSpec is one entry of a kit/args declaration.
type ArgSpec struct {
	// Name is the argument name with every marker stripped.
	Name string
	// Optional reports a trailing "?": the argument may be omitted.
	Optional bool
	// Variadic reports a trailing "...": the argument takes every
	// remaining operand. A required variadic takes one or more, an
	// optional one ("name...?") zero or more. It must be declared
	// last; the grammar does not check that, the command's own Args
	// validator does.
	Variadic bool
}

// ParseArgs parses a kit/args value: a comma-separated list of
// names, whitespace around each trimmed, empty entries dropped.
// Each name may end in "..." (variadic), "?" (optional), or
// "...?" (optional variadic), in that order; any other suffix is
// part of the name. An empty or blank value yields nil, so an
// absent annotation stays distinguishable from an explicitly empty
// list.
//
//	name       required
//	name?      optional
//	name...    variadic, one or more
//	name...?   variadic, zero or more
func ParseArgs(raw string) []ArgSpec {
	var out []ArgSpec
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		a := ArgSpec{}
		entry, a.Optional = strings.CutSuffix(entry, "?")
		entry, a.Variadic = strings.CutSuffix(entry, "...")
		a.Name = entry
		out = append(out, a)
	}
	return out
}
