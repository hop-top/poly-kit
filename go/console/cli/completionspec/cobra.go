package completionspec

import (
	"reflect"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// FlagEnumAnnotation is the pflag annotation carrying a flag's legal
// value set. It repeats cli.FlagEnumAnnotation, which this package
// cannot import: cli imports this package to register its commands.
const FlagEnumAnnotation = "kit.cli.flag.enum"

// argsAnnotation is kit's declared-positionals annotation: a comma
// separated name list, "?" marking an optional one. The grammar is
// cmdreflect's; a test holds the two readers together.
const argsAnnotation = "kit/args"

// FromCobra builds the spec model from a cobra command tree.
//
// What it reads, and the rule each follows:
//   - Commands: every child except cobra's `__complete` entries and a
//     nameless one (a help command replaced by an empty one). A
//     Hidden or Deprecated command is kept and marked Hidden, since help
//     leaves it out but it still parses.
//   - Flags: the flags a command declares (its own and its persistent
//     ones; inherited ones belong to the ancestor). The help flag is
//     left out: cobra adds it to every command as it runs, and terminals
//     offer help on their own. A root with a Version and no version
//     flag gets cobra's default one, which cobra adds only on a run of
//     the root itself.
//   - Descriptions and value names: pflag.UnquoteUsage, as help prints
//     them.
//   - Positionals: none under cobra.NoArgs; else kit/args when set,
//     else the operands in Use.
//   - Candidates, first match wins: the flag-enum annotation, cobra
//     ValidArgs, MarkFlagFilename, MarkFlagDirname, then any completion
//     function but cobra.NoFileCompletions as a dynamic callback.
//
// Commands and flags are sorted by name so the output does not depend
// on declaration order or cobra's sorting switches.
func FromCobra(root *cobra.Command) Spec {
	if root == nil {
		return Spec{}
	}
	cmd := buildCommand(root)
	if f, ok := versionFlag(root); ok {
		cmd.Flags = append(cmd.Flags, f)
		sortFlags(cmd.Flags)
	}
	return Spec{Root: cmd}
}

func buildCommand(c *cobra.Command) Command {
	out := Command{
		Name:        c.Name(),
		Aliases:     append([]string(nil), c.Aliases...),
		Description: c.Short,
		Hidden:      c.Hidden || c.Deprecated != "",
		Flags:       buildFlags(c),
		Args:        buildArgs(c),
	}
	for _, sub := range c.Commands() {
		switch sub.Name() {
		case "", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			continue
		}
		out.Commands = append(out.Commands, buildCommand(sub))
	}
	sort.SliceStable(out.Commands, func(i, j int) bool {
		return out.Commands[i].Name < out.Commands[j].Name
	})
	return out
}

func buildFlags(c *cobra.Command) []Flag {
	var out []Flag
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		out = append(out, buildFlag(c, f))
	})
	sortFlags(out)
	return out
}

func sortFlags(flags []Flag) {
	sort.SliceStable(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
}

func buildFlag(c *cobra.Command, f *pflag.Flag) Flag {
	varname, usage := pflag.UnquoteUsage(f)
	out := Flag{
		Name:        f.Name,
		Description: usage,
		Persistent:  c.PersistentFlags().Lookup(f.Name) == f,
		Hidden:      f.Hidden,
		Required:    hasAnnotation(f, cobra.BashCompOneRequiredFlag),
		Repeatable:  repeatable(f.Value.Type()),
	}
	if f.ShorthandDeprecated == "" {
		out.Short = f.Shorthand
	}
	if takesValue(f) {
		if varname == "" {
			varname = "value"
		}
		out.Arg = &Arg{
			Name:     varname,
			Optional: f.NoOptDefVal != "",
			Complete: flagCompletion(c, f),
		}
	}
	return out
}

// takesValue reports whether f is given a value. Switches and counters
// are not; pflag marks both with a NoOptDefVal, but so is a value flag
// whose value may be left out, so the type decides.
func takesValue(f *pflag.Flag) bool {
	switch f.Value.Type() {
	case "bool", "boolfunc", "count":
		return false
	}
	return true
}

// repeatable reports a pflag type that accumulates across repeats.
func repeatable(typ string) bool {
	switch {
	case typ == "count",
		strings.HasSuffix(typ, "Slice"),
		strings.HasSuffix(typ, "Array"),
		strings.HasPrefix(typ, "stringTo"):
		return true
	}
	return false
}

func hasAnnotation(f *pflag.Flag, key string) bool {
	_, ok := f.Annotations[key]
	return ok
}

func flagCompletion(c *cobra.Command, f *pflag.Flag) Completion {
	if vals := f.Annotations[FlagEnumAnnotation]; len(vals) > 0 {
		return Completion{Kind: CompletionChoices, Values: append([]string(nil), vals...)}
	}
	if exts, ok := f.Annotations[cobra.BashCompFilenameExt]; ok {
		return Completion{Kind: CompletionFile, Extensions: trimDots(exts)}
	}
	if hasAnnotation(f, cobra.BashCompSubdirsInDir) {
		return Completion{Kind: CompletionDir}
	}
	if fn, ok := c.GetFlagCompletionFunc(f.Name); ok && offersValues(fn) {
		return Completion{Kind: CompletionDynamic}
	}
	return Completion{}
}

func trimDots(exts []string) []string {
	if len(exts) == 0 {
		return nil
	}
	out := make([]string, len(exts))
	for i, e := range exts {
		out[i] = strings.TrimPrefix(e, ".")
	}
	return out
}

// buildArgs reads the positionals: none under cobra.NoArgs, else
// kit/args when declared, else the operands in Use. A command with
// candidates but no named positional gets one optional, variadic "arg"
// to hang them on.
func buildArgs(c *cobra.Command) []Arg {
	if c.Args != nil && sameFunc(c.Args, cobra.NoArgs) {
		return nil
	}
	args := declaredArgs(c)
	if args == nil {
		args = useArgs(c.Use)
	}
	comp := argsCompletion(c)
	if comp.Kind == CompletionNone {
		return args
	}
	if len(args) == 0 {
		args = []Arg{{Name: "arg", Optional: true, Variadic: true}}
	}
	for i := range args {
		args[i].Complete = comp
	}
	return args
}

// argsCompletion: cobra offers ValidArgs, or the ValidArgsFunction, at
// every position, so every positional shares it.
func argsCompletion(c *cobra.Command) Completion {
	if len(c.ValidArgs) > 0 {
		vals := make([]string, len(c.ValidArgs))
		for i, v := range c.ValidArgs {
			vals[i], _, _ = strings.Cut(v, "\t")
		}
		return Completion{Kind: CompletionChoices, Values: vals}
	}
	if offersValues(c.ValidArgsFunction) {
		return Completion{Kind: CompletionDynamic}
	}
	return Completion{}
}

// offersValues reports a completion function that may return values:
// any but nil and cobra.NoFileCompletions, cobra's "none" completer.
func offersValues(fn cobra.CompletionFunc) bool {
	return fn != nil && !sameFunc(fn, cobra.NoFileCompletions)
}

// sameFunc reports whether two funcs are the same top-level function.
func sameFunc(a, b any) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

func declaredArgs(c *cobra.Command) []Arg {
	raw := strings.TrimSpace(c.Annotations[argsAnnotation])
	if raw == "" {
		return nil
	}
	var out []Arg
	for _, n := range strings.Split(raw, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		a := Arg{}
		a.Name, a.Optional = strings.CutSuffix(n, "?")
		out = append(out, a)
	}
	return out
}

// useArgs parses the operands of a cobra Use line: "<x>" is required,
// "[x]" optional, a trailing "..." (inside or outside the brackets)
// variadic, a bare word required. A bracket spanning several words is
// one operand, its spaces dropped ("[topic | command]" is
// "topic|command"). cobra's "[flags]" and "[command]" placeholders,
// and flag syntax, are not operands.
func useArgs(use string) []Arg {
	words := useWords(use)
	if len(words) < 2 {
		return nil
	}
	var out []Arg
	for _, w := range words[1:] {
		switch w {
		case "[flags]", "[command]":
			continue
		}
		if strings.HasPrefix(strings.TrimLeft(w, "[<("), "-") {
			continue
		}
		a := Arg{}
		w, a.Variadic = strings.CutSuffix(w, "...")
		switch {
		case strings.HasPrefix(w, "[") && strings.HasSuffix(w, "]"):
			a.Optional = true
			w = w[1 : len(w)-1]
		case strings.HasPrefix(w, "<") && strings.HasSuffix(w, ">"):
			w = w[1 : len(w)-1]
		}
		if inner, ok := strings.CutSuffix(w, "..."); ok {
			w, a.Variadic = inner, true
		}
		if w == "" {
			continue
		}
		a.Name = w
		out = append(out, a)
	}
	return out
}

// useWords splits a Use line on spaces, keeping a bracketed group
// that spans several words as one word with its spaces removed.
func useWords(use string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	for _, w := range strings.Fields(use) {
		cur.WriteString(w)
		depth += strings.Count(w, "[") + strings.Count(w, "<") -
			strings.Count(w, "]") - strings.Count(w, ">")
		if depth > 0 {
			continue
		}
		out = append(out, cur.String())
		cur.Reset()
		depth = 0
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// versionFlag is cobra's default version flag for a root that has a
// Version and declares no version flag of its own.
func versionFlag(root *cobra.Command) (Flag, bool) {
	if root.Version == "" || root.LocalFlags().Lookup("version") != nil {
		return Flag{}, false
	}
	f := Flag{Name: "version", Description: "version for " + root.DisplayName()}
	if root.LocalFlags().ShorthandLookup("v") == nil {
		f.Short = "v"
	}
	return f, true
}
