// Package completionspec exports a kit CLI's command tree as a
// completion spec for terminals that read one instead of running a
// shell completion script: a Fig spec (VS Code terminal suggest,
// Kiro CLI, inshellisense, Warp) and a Usage KDL spec (usage.jdx.dev,
// mise).
//
// One model, built once from the live cobra tree by [FromCobra], feeds
// both emitters ([RenderFig], [RenderUsage]). Values the tree states
// statically (flag enums, cobra ValidArgs, MarkFlagFilename,
// MarkFlagDirname) are written into the spec. Values a completion
// function computes at run time become a generator that calls back into
// the binary's hidden `__complete` entry, so the spec never snapshots
// data that changes.
//
// Output is byte-stable: commands and flags are sorted by name, and the
// emitters are pure functions of the model. contracts/completion-spec-v1
// pins the bytes every port must produce.
package completionspec

// CompletionKind says where an argument's candidate values come from.
type CompletionKind int

const (
	// CompletionNone: the spec offers nothing for the value.
	CompletionNone CompletionKind = iota
	// CompletionChoices: a fixed list, Completion.Values.
	CompletionChoices
	// CompletionFile: file paths, filtered by Completion.Extensions
	// when any are set.
	CompletionFile
	// CompletionDir: directory paths.
	CompletionDir
	// CompletionDynamic: the binary's `__complete` entry computes the
	// values when the user asks.
	CompletionDynamic
)

// Completion describes the candidates for one argument value.
type Completion struct {
	Kind CompletionKind
	// Values are the candidates of a CompletionChoices.
	Values []string
	// Extensions filter a CompletionFile, without the leading dot.
	Extensions []string
}

// Arg is a positional argument, or the value a flag takes.
type Arg struct {
	Name     string
	Optional bool
	Variadic bool
	Complete Completion
}

// Flag is one flag declared on a command.
type Flag struct {
	// Name is the long name, without dashes.
	Name string
	// Short is the one-letter shorthand, without the dash; empty when
	// the flag has none.
	Short       string
	Description string
	// Persistent flags apply to the declaring command and every
	// command below it.
	Persistent bool
	Hidden     bool
	Required   bool
	// Repeatable flags may be given more than once: counters, and
	// flags collecting a list.
	Repeatable bool
	// Arg is the value the flag takes; nil for a switch. When
	// Arg.Optional, the value must be attached (`--flag=value`) and a
	// bare flag means DefaultMissing: pflag's NoOptDefVal.
	Arg            *Arg
	DefaultMissing string
}

// Command is one node of the command tree.
type Command struct {
	Name        string
	Aliases     []string
	Description string
	// Hidden commands still parse; terminals just do not suggest them.
	Hidden   bool
	Flags    []Flag
	Args     []Arg
	Commands []Command
}

// Spec is the whole tree. Root.Name is the binary name; Version is
// the root's version, empty when it has none.
type Spec struct {
	Root    Command
	Version string
}
