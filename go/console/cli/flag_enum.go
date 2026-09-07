// Flag value-enum registry for kit CLIs.
//
// A flag whose legal values form a closed set (`--status`, `--priority`,
// `--format`) is declared ONCE with WithFlagEnum. Three consumers then read
// that single annotation:
//
//  1. Parse-time errors (errcorrect.go) — a missing value renders the
//     allowed set instead of a bare "flag needs an argument".
//  2. Help text (applyFlagEnumHelp) — the usage line gains
//     "(one of: A, B, C)".
//  3. Shell completion (bindFlagEnumCompletions) — the shell offers the
//     same values.
//
// Single source of truth is the point: adopters that repeat the literal
// per consumer drift, and the error layer has nothing to query.
//
// Storage is a pflag annotation on the flag itself, so the values travel
// with the flag wherever cobra resolves it (leaf, parent, persistent set)
// and no side table has to be kept in sync.
//
// Ordering constraint (same as WithFlagValidator): register BEFORE
// Root.WrapRunE / Root.Execute. The enum is materialized onto the flag at
// Execute time; registrations added afterwards never reach the flag.
package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// FlagEnumAnnotation is the pflag annotation key carrying a flag's legal
// value set. Exported so adopters that register flags outside kit's
// builders (or generate them) can set the annotation directly:
//
//	f := cmd.Flags().Lookup("status")
//	f.Annotations = map[string][]string{cli.FlagEnumAnnotation: {"TODO", "DONE"}}
//
// WithFlagEnum is the supported path; this key is the contract it writes.
const FlagEnumAnnotation = "kit.cli.flag.enum"

// WithFlagEnum declares the closed set of legal values for a flag.
//
// The values are attached to the flag as a pflag annotation once the
// command tree is walked (at Execute / WrapRunE time), which lets adopters
// declare the enum before the flag itself is registered — construction
// order between kit builders and adopter subcommands is not fixed.
//
// Registering the same name twice overwrites the earlier set (last wins).
// An empty value list clears the registration.
//
// Declaring an enum does NOT validate anything by itself: the values are
// advisory metadata for errors, help, and completion. Pair it with
// WithFlagValidator when the value must actually be rejected.
func (r *Root) WithFlagEnum(name string, values ...string) *Root {
	if r == nil || name == "" {
		return r
	}
	if len(values) == 0 {
		delete(r.flagEnums, name)
		return r
	}
	if r.flagEnums == nil {
		r.flagEnums = make(map[string][]string)
	}
	r.flagEnums[name] = append([]string(nil), values...)
	return r
}

// FlagEnum returns the declared value set for name, or nil. The returned
// slice is a copy; mutating it does not affect the registration.
func (r *Root) FlagEnum(name string) []string {
	if r == nil {
		return nil
	}
	vals, ok := r.flagEnums[name]
	if !ok {
		return nil
	}
	return append([]string(nil), vals...)
}

// flagEnumValues reads the enum annotation off a resolved pflag.Flag.
// Returns nil when the flag carries no enum. This is the single read path
// used by every consumer — errors, help, completion — so they can never
// disagree about where the values live.
func flagEnumValues(f *pflag.Flag) []string {
	if f == nil || f.Annotations == nil {
		return nil
	}
	vals := f.Annotations[FlagEnumAnnotation]
	if len(vals) == 0 {
		return nil
	}
	return vals
}

// lookupFlagEnum resolves name against cmd's own, inherited, and
// persistent flag sets and returns its enum values.
func lookupFlagEnum(cmd *cobra.Command, name string) []string {
	return flagEnumValues(lookupFlag(cmd, name))
}

// lookupFlag resolves a flag by name across cmd's local, inherited, and
// persistent sets, walking up to the root. cmd.Flag already merges local
// and inherited; the parent walk covers a persistent flag registered on an
// ancestor that has not been merged yet (pre-Execute construction).
func lookupFlag(cmd *cobra.Command, name string) *pflag.Flag {
	if cmd == nil || name == "" {
		return nil
	}
	if f := cmd.Flag(name); f != nil {
		return f
	}
	for c := cmd; c != nil; c = c.Parent() {
		if f := c.PersistentFlags().Lookup(name); f != nil {
			return f
		}
		if f := c.Flags().Lookup(name); f != nil {
			return f
		}
	}
	return nil
}

// applyFlagEnums stamps every registered enum onto the matching flags
// across the whole command tree. Called from Execute before help
// rendering, validation, and parsing, so all three consumers see the
// annotation.
//
// Idempotent: re-stamping the same values is a no-op, and the help
// addendum (applied separately) guards against duplication itself.
func (r *Root) applyFlagEnums() {
	if r == nil || r.Cmd == nil || len(r.flagEnums) == 0 {
		return
	}
	stampFlagEnums(r.Cmd, r.flagEnums)
}

func stampFlagEnums(cmd *cobra.Command, enums map[string][]string) {
	for name, vals := range enums {
		for _, fs := range []*pflag.FlagSet{cmd.Flags(), cmd.PersistentFlags()} {
			f := fs.Lookup(name)
			if f == nil {
				continue
			}
			if f.Annotations == nil {
				f.Annotations = make(map[string][]string)
			}
			f.Annotations[FlagEnumAnnotation] = append([]string(nil), vals...)
		}
	}
	for _, c := range cmd.Commands() {
		stampFlagEnums(c, enums)
	}
}

// flagEnumHelpSuffix is the parenthetical appended to a flag's usage
// string. Kept as a distinct helper so the help writer and its test agree
// on the exact rendering.
func flagEnumHelpSuffix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return "(one of: " + strings.Join(values, ", ") + ")"
}

// applyFlagEnumHelp appends the enum values to each annotated flag's usage
// string so `--help` carries the set without a second declaration.
//
// Idempotent by suffix check: a flag whose usage already ends with the
// rendered suffix is left alone, so repeated Execute calls (tests, nested
// harnesses) do not stack parentheticals.
func (r *Root) applyFlagEnumHelp() {
	if r == nil || r.Cmd == nil {
		return
	}
	applyFlagEnumHelpTree(r.Cmd)
}

func applyFlagEnumHelpTree(cmd *cobra.Command) {
	for _, fs := range []*pflag.FlagSet{cmd.Flags(), cmd.PersistentFlags()} {
		fs.VisitAll(func(f *pflag.Flag) {
			suffix := flagEnumHelpSuffix(flagEnumValues(f))
			if suffix == "" || strings.HasSuffix(f.Usage, suffix) {
				return
			}
			if f.Usage == "" {
				f.Usage = suffix
				return
			}
			f.Usage += " " + suffix
		})
	}
	for _, c := range cmd.Commands() {
		applyFlagEnumHelpTree(c)
	}
}

// bindFlagEnumCompletions registers a cobra flag-completion function for
// every enum-annotated flag, so the shell offers exactly the declared
// values. Flags that already have a completion function registered are
// left alone: an adopter-supplied dynamic completer beats a static list.
func (r *Root) bindFlagEnumCompletions() {
	if r == nil || r.Cmd == nil || len(r.flagEnums) == 0 {
		return
	}
	bindFlagEnumCompletionsTree(r.Cmd)
}

func bindFlagEnumCompletionsTree(cmd *cobra.Command) {
	seen := make(map[string]bool)
	for _, fs := range []*pflag.FlagSet{cmd.Flags(), cmd.PersistentFlags()} {
		fs.VisitAll(func(f *pflag.Flag) {
			if seen[f.Name] {
				return
			}
			vals := flagEnumValues(f)
			if len(vals) == 0 {
				return
			}
			seen[f.Name] = true
			// RegisterFlagCompletionFunc errors when a function is
			// already registered for the flag. That is exactly the
			// "adopter wins" case, so the error is the signal to stop,
			// not a failure to report.
			_ = cmd.RegisterFlagCompletionFunc(f.Name, enumCompletionFunc(vals))
		})
	}
	for _, c := range cmd.Commands() {
		bindFlagEnumCompletionsTree(c)
	}
}

// enumCompletionFunc returns a cobra completion function serving values,
// prefix-filtered case-insensitively to match the completion package's
// Static behavior.
func enumCompletionFunc(values []string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		out := make([]string, 0, len(values))
		lower := strings.ToLower(toComplete)
		for _, v := range values {
			if strings.HasPrefix(strings.ToLower(v), lower) {
				out = append(out, v)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// sortedFlagNames returns every flag name visible to cmd (local,
// inherited, and ancestor-persistent), deduplicated and sorted. Used by
// the unknown-flag suggester so its candidate set matches what the user
// could legitimately have typed at this command.
func sortedFlagNames(cmd *cobra.Command) []string {
	if cmd == nil {
		return nil
	}
	seen := make(map[string]bool)
	add := func(f *pflag.Flag) {
		if f.Hidden {
			// Hidden flags are kit-owned plumbing or deliberately
			// undocumented. Suggesting one teaches an interface the tool
			// does not advertise.
			return
		}
		seen[f.Name] = true
	}
	cmd.Flags().VisitAll(add)
	cmd.InheritedFlags().VisitAll(add)
	for c := cmd; c != nil; c = c.Parent() {
		c.PersistentFlags().VisitAll(add)
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
