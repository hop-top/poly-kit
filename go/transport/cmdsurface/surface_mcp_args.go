package cmdsurface

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"hop.top/kit/go/ai/cmdreflect"
)

// Positional arguments over MCP.
//
// An MCP tool takes one JSON object of named arguments; a cobra leaf
// takes flags by name and positional arguments by position. A leaf
// that declares its positionals (kit/args) publishes them as ONE
// extra property, "args": an array of strings in declared order —
// the same key and the same shape the REST projection's request body
// and the socket wire already use, so a caller moving between
// transports passes positionals the same way everywhere. The
// alternative, one named property per declared argument, would share
// a namespace with the leaf's flags and with every persistent flag
// the tool inherits, so a flag added later could silently take an
// argument's name; the array reserves exactly one name, and a
// trailing variadic argument needs no extra syntax.
//
// Required-ness follows kit/args: the property is required, with
// minItems set to the number of required arguments, when any
// argument is; optional arguments ("name?") only lengthen what the
// array may hold. No maxItems is published: kit/args has no variadic
// marker, and the command's own Args validator already refuses a
// count it does not accept.
//
// A leaf that takes positionals without declaring them, or whose
// "args" name is taken by a flag, publishes no such property and
// says so in its description: guessing names or positions would
// publish a contract the command never made.
//
// go/ai/toolspec/adapters/mcp.go renders the same rules statically
// for `<tool> spec --format mcp`; the static/live parity test holds
// the two together.

// mcpArgsProperty is the inputSchema property that carries a leaf's
// declared positional arguments.
const mcpArgsProperty = "args"

// Tool-description notes for a leaf whose positional arguments MCP
// cannot pass. Mirrored verbatim by go/ai/toolspec/adapters/mcp.go.
const (
	mcpNoteUndeclaredArgs = "Takes positional arguments it does not declare (kit/args), so they cannot be passed over MCP."
	mcpNoteArgsFlagTaken  = `Takes positional arguments that cannot be passed over MCP: its --args flag holds the "args" property.`
)

// MCPToolDescription returns the description leaf's MCP tool
// publishes: the command's one-line summary, followed by a note when
// the command takes positional arguments the tool cannot pass (see
// [MCPInputSchema]). The hand-rolled surface and
// hop.top/kit/go/transport/mcpsdk both publish this string.
func MCPToolDescription(leaf *Leaf) string {
	desc := leaf.Cmd.Short
	note := ""
	switch {
	case leaf.Descriptor != nil && leaf.Descriptor.UndeclaredArgs:
		note = mcpNoteUndeclaredArgs
	case len(mcpDeclaredArgs(leaf)) > 0 && !mcpArgsPublished(leaf):
		note = mcpNoteArgsFlagTaken
	}
	switch {
	case note == "":
		return desc
	case desc == "":
		return note
	default:
		return desc + " " + note
	}
}

// MCPInputSchema returns leaf's MCP tool inputSchema: one property
// per visible flag, local then inherited, with the flags cobra marks
// required listed as required; and, when the leaf declares
// positional arguments (kit/args) and no visible flag is named
// "args", an "args" property holding them as an array of strings in
// declared order. The hand-rolled surface and
// hop.top/kit/go/transport/mcpsdk both publish this schema.
func MCPInputSchema(leaf *Leaf) map[string]any {
	props, required := collectFlags(leaf.Cmd)
	if mcpArgsPublished(leaf) {
		declared := mcpDeclaredArgs(leaf)
		prop := map[string]any{
			"type":        "array",
			"items":       map[string]string{"type": "string"},
			"description": mcpArgsDescription(declared),
		}
		if n := requiredArgCount(declared); n > 0 {
			prop["minItems"] = n
			required = append(required, mcpArgsProperty)
		}
		props[mcpArgsProperty] = prop
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// MCPSplitArguments splits a tools/call arguments object into the
// Invocation's Flags and positional Args. When leaf publishes an
// "args" property (see [MCPInputSchema]) that key is taken out of the
// flags: it must hold an array of strings, at least as many as the
// leaf declares required, and becomes Args in order. Every other key
// is a flag, forwarded as-is. A leaf that publishes no "args"
// property gets every key as a flag, exactly as before positional
// arguments were projected.
//
// The error names the problem for the caller to correct; surfaces
// return it as an isError tool result.
func MCPSplitArguments(leaf *Leaf, arguments map[string]any) (map[string]any, []string, error) {
	if !mcpArgsPublished(leaf) {
		return arguments, nil, nil
	}
	raw, ok := arguments[mcpArgsProperty]
	if !ok {
		return arguments, nil, requireMCPArgs(leaf, nil)
	}
	flags := make(map[string]any, len(arguments)-1)
	for k, v := range arguments {
		if k != mcpArgsProperty {
			flags[k] = v
		}
	}
	list, ok := raw.([]any)
	if !ok && raw != nil {
		return nil, nil, errors.New(`"args" must be an array of strings`)
	}
	args := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, nil, errors.New(`"args" must be an array of strings`)
		}
		args = append(args, s)
	}
	if err := requireMCPArgs(leaf, args); err != nil {
		return nil, nil, err
	}
	if len(flags) == 0 {
		flags = nil
	}
	return flags, args, nil
}

// requireMCPArgs checks that every declared-required positional
// argument was supplied, naming the first one missing — the check
// and the wording the REST projection applies.
func requireMCPArgs(leaf *Leaf, args []string) error {
	declared := mcpDeclaredArgs(leaf)
	if len(args) < requiredArgCount(declared) {
		return errors.New("missing required argument: " + declared[len(args)].Name)
	}
	return nil
}

// mcpDeclaredArgs returns leaf's declared positional arguments, from
// its reflected descriptor.
func mcpDeclaredArgs(leaf *Leaf) []cmdreflect.Arg {
	if leaf == nil || leaf.Descriptor == nil {
		return nil
	}
	return leaf.Descriptor.Args
}

// mcpArgsPublished reports whether leaf's inputSchema carries the
// "args" property: it declares positional arguments and no visible
// flag already holds the name. The flag keeps the name because it
// was part of the tool's schema first; taking it would change what
// an existing caller's "args" means.
func mcpArgsPublished(leaf *Leaf) bool {
	if len(mcpDeclaredArgs(leaf)) == 0 || leaf.Cmd == nil {
		return false
	}
	return !flagPublished(leaf.Cmd, mcpArgsProperty)
}

// flagPublished reports whether collectFlags publishes a property
// for the flag called name: a visible, non-deprecated flag of that
// name, local or inherited.
func flagPublished(cmd *cobra.Command, name string) bool {
	for _, f := range []*pflag.Flag{
		cmd.LocalFlags().Lookup(name),
		cmd.InheritedFlags().Lookup(name),
	} {
		if f != nil && !f.Hidden && f.Deprecated == "" {
			return true
		}
	}
	return false
}

// requiredArgCount counts the required arguments in declared.
func requiredArgCount(declared []cmdreflect.Arg) int {
	n := 0
	for _, a := range declared {
		if a.Required {
			n++
		}
	}
	return n
}

// mcpArgsDescription renders the declared names in order, optional
// ones marked with "?" — the REST projection's wording, so both
// documents describe the array the same way.
func mcpArgsDescription(declared []cmdreflect.Arg) string {
	names := make([]string, 0, len(declared))
	for _, a := range declared {
		n := a.Name
		if !a.Required {
			n += "?"
		}
		names = append(names, n)
	}
	return "Positional arguments in order: " + strings.Join(names, ", ")
}
