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
// and no side table has to be kept in sync. Every consumer reads only the
// annotation, so a flag whose annotation an adopter set directly (see
// FlagEnumAnnotation) is served by all three.
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
// All three consumers — errors, help, completion — read the annotation and
// nothing else, so a flag annotated this way is served identically to one
// declared through the registry.
const FlagEnumAnnotation = "kit.cli.flag.enum"

// flagEnumCompletionAnnotation records that a Root's enum completions have
// been bound, so repeated prepareTree calls do not re-register them. Cobra
// keeps flag-completion functions in a package-global map keyed by
// *pflag.Flag and never prunes it, so a served surface that rebuilds a
// tree per request would otherwise pin one closure and one flag per enum
// flag per request for the life of the process.
const flagEnumCompletionAnnotation = "kit.cli.flag.enum.completions"

// flagEnumKey identifies a registration. An empty path is the tree-wide
// form: the values are stamped onto every matching flag anywhere in the
// tree. A non-empty path scopes the registration to one command, which is
// what a tree with two same-named flags carrying different legal sets
// needs — `list --type` and `export --type` are different flags, and one
// declaration must not silently reach the other.
type flagEnumKey struct {
	path string // space-separated command path below the root; "" = tree-wide
	name string
}

// WithFlagEnum declares the closed set of legal values for a flag,
// tree-wide: every flag of that name anywhere in the command tree gets the
// set. That is the right form for a flag whose meaning is global (a root
// persistent `--format`, a `--status` the whole tool shares).
//
// When two commands register the same flag NAME with different legal sets,
// use WithCommandFlagEnum instead: a tree-wide declaration is stamped onto
// both, and the last one registered wins everywhere — in errors, in help,
// and in completion.
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
	return r.withFlagEnum("", name, values...)
}

// WithCommandFlagEnum declares the closed set of legal values for one
// command's flag. path is the command path below the root, either as a
// single space-separated string ("widget list") or as separate words
// ("widget", "list"); the flag name is the last argument before values.
//
//	root.WithCommandFlagEnum("list", "type", "TASK", "TRACK")
//	root.WithCommandFlagEnum("export", "type", "json", "csv")
//
// Only the named command's flag is stamped, so two leaves may declare the
// same flag name with different sets and each keeps its own — errors,
// help, and completion all read the flag they resolved rather than a
// name-keyed table.
//
// A path naming no command in the tree stamps nothing; like a tree-wide
// registration against an absent flag, that is silent rather than fatal,
// because a tree may legitimately be built without the command a
// declaration anticipates.
func (r *Root) WithCommandFlagEnum(path, name string, values ...string) *Root {
	return r.withFlagEnum(normalizeCommandPath(path), name, values...)
}

func (r *Root) withFlagEnum(path, name string, values ...string) *Root {
	if r == nil || name == "" {
		return r
	}
	key := flagEnumKey{path: path, name: name}
	if len(values) == 0 {
		delete(r.flagEnums, key)
		return r
	}
	if r.flagEnums == nil {
		r.flagEnums = make(map[flagEnumKey][]string)
	}
	r.flagEnums[key] = append([]string(nil), values...)
	return r
}

// normalizeCommandPath collapses a command path to the space-separated
// form the registry keys on, so "widget  list" and "widget list" are the
// same command. A path that begins with the root's own name is accepted
// and the name dropped: CommandPath() renders it, so pasting one back is
// the obvious thing to try.
func normalizeCommandPath(path string) string {
	return strings.Join(strings.Fields(path), " ")
}

// FlagEnum returns the tree-wide declared value set for name, or nil. The
// returned slice is a copy; mutating it does not affect the registration.
func (r *Root) FlagEnum(name string) []string {
	return r.commandFlagEnum("", name)
}

// CommandFlagEnum returns the value set declared for one command's flag,
// or nil. Only the scoped registration is consulted; a tree-wide
// declaration of the same name is reported by FlagEnum.
func (r *Root) CommandFlagEnum(path, name string) []string {
	return r.commandFlagEnum(normalizeCommandPath(path), name)
}

func (r *Root) commandFlagEnum(path, name string) []string {
	if r == nil {
		return nil
	}
	vals, ok := r.flagEnums[flagEnumKey{path: path, name: name}]
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

// applyFlagEnums installs the registry on the command tree: it stamps each
// registered value set onto the matching flags, suffixes their help text,
// and binds their shell completion. One walk serves all three so they can
// never disagree about which flags carry an enum.
//
// Called from prepareTree, ahead of help rendering, validation, and
// parsing, so all three consumers see the annotation.
//
// Idempotent: re-stamping the same values is a no-op, the help addendum
// guards against duplication by suffix, and completion binding is guarded
// by an annotation on the root (cobra's completion map is a never-pruned
// package global — see flagEnumCompletionAnnotation).
func (r *Root) applyFlagEnums() {
	if r == nil || r.Cmd == nil {
		return
	}
	r.stampFlagEnums()
	bindCompletions := r.Cmd.Annotations == nil ||
		r.Cmd.Annotations[flagEnumCompletionAnnotation] != "true"
	walk(r.Cmd, func(cmd *cobra.Command) {
		visitFlagSets(cmd, func(f *pflag.Flag) {
			vals := flagEnumValues(f)
			if len(vals) == 0 {
				return
			}
			applyFlagEnumHelp(f, vals)
			if bindCompletions {
				bindEnumCompletion(cmd, f.Name, vals)
			}
		})
	})
	if bindCompletions {
		annotate(r.Cmd, flagEnumCompletionAnnotation)
	}
}

// stampFlagEnums writes each registration's values onto the flags it
// names: a tree-wide registration onto every matching flag in the tree, a
// command-scoped one onto that command's flag only.
func (r *Root) stampFlagEnums() {
	if len(r.flagEnums) == 0 {
		return
	}
	rootPath := r.Cmd.Name()
	walk(r.Cmd, func(cmd *cobra.Command) {
		path := normalizeCommandPath(strings.TrimPrefix(cmd.CommandPath(), rootPath))
		visitFlagSets(cmd, func(f *pflag.Flag) {
			// A command-scoped registration wins over a tree-wide one
			// for the same flag: the narrower declaration is the more
			// specific statement of intent.
			vals, ok := r.flagEnums[flagEnumKey{path: path, name: f.Name}]
			if !ok {
				vals, ok = r.flagEnums[flagEnumKey{name: f.Name}]
			}
			if !ok {
				return
			}
			if f.Annotations == nil {
				f.Annotations = make(map[string][]string)
			}
			f.Annotations[FlagEnumAnnotation] = append([]string(nil), vals...)
		})
	})
}

// visitFlagSets calls fn once per distinct flag reachable on cmd's own and
// persistent sets. A persistent flag appears in both once cobra has merged
// them, so the names are deduplicated.
func visitFlagSets(cmd *cobra.Command, fn func(*pflag.Flag)) {
	seen := make(map[string]bool)
	visit := func(f *pflag.Flag) {
		if seen[f.Name] {
			return
		}
		seen[f.Name] = true
		fn(f)
	}
	cmd.Flags().VisitAll(visit)
	cmd.PersistentFlags().VisitAll(visit)
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

// applyFlagEnumHelp appends the enum values to an annotated flag's usage
// string so `--help` carries the set without a second declaration.
//
// Idempotent by suffix check: a flag whose usage already ends with the
// rendered suffix is left alone, so repeated Execute calls (tests, nested
// harnesses) do not stack parentheticals.
func applyFlagEnumHelp(f *pflag.Flag, values []string) {
	suffix := flagEnumHelpSuffix(values)
	if suffix == "" || strings.HasSuffix(f.Usage, suffix) {
		return
	}
	if f.Usage == "" {
		f.Usage = suffix
		return
	}
	f.Usage += " " + suffix
}

// bindEnumCompletion registers the static completer for one enum flag. A
// package variable so the leak guard's test can count the calls that reach
// cobra: cobra silently refuses a duplicate registration, so an entry count
// cannot tell a guarded bind from an unguarded one.
var bindEnumCompletion = func(cmd *cobra.Command, name string, values []string) {
	// RegisterFlagCompletionFunc errors when a function is already
	// registered for the flag. That is exactly the "adopter wins" case, so
	// the error is the signal to stop, not a failure to report.
	_ = cmd.RegisterFlagCompletionFunc(name, enumCompletionFunc(values))
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

// sortedFlagNames returns every flag name `--help` would show for cmd
// (local, inherited, and ancestor-persistent), deduplicated and sorted.
// Used by the unknown-flag suggester so its candidate set matches what the
// user could legitimately have typed at this command.
//
// hiddenDefault names the kit-owned plumbing flags that root `--help`
// suppresses for cross-language parity but leaf help still advertises
// under GLOBAL FLAGS (--dry-run, --confirm, --chdir, --config…). Those are
// documented interface, so a typo of one gets corrected; a flag the tool
// hides everywhere is not, because suggesting it would teach an interface
// the tool does not advertise. Same rule as collectInherited in help.go —
// suggest what help shows.
func sortedFlagNames(cmd *cobra.Command, hiddenDefault map[string]struct{}) []string {
	if cmd == nil {
		return nil
	}
	seen := make(map[string]bool)
	add := func(f *pflag.Flag) {
		if f.Hidden {
			if _, ok := hiddenDefault[f.Name]; !ok {
				return
			}
		}
		seen[f.Name] = true
	}
	cmd.Flags().VisitAll(add)
	cmd.InheritedFlags().VisitAll(add)
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
