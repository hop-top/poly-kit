package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// installLeafHelp walks the command tree and installs the FLAGS /
// GLOBAL FLAGS split on every command, root included. The renderer asks
// fang to render help with inherited persistent flags hidden, then
// appends a separate "GLOBAL FLAGS" section listing those inherited
// flags. Mirrors the kubectl/gh/docker convention so any command's help
// shows only command-specific flags by default.
func (r *Root) installLeafHelp() {
	root := r.Cmd
	hiddenDefault := make(map[string]struct{}, len(r.hiddenDefaultFlags))
	for _, name := range r.hiddenDefaultFlags {
		hiddenDefault[name] = struct{}{}
	}
	for _, c := range root.Commands() {
		installHelpRecursive(root, c, hiddenDefault)
	}
	r.installRootHelp(hiddenDefault)
}

// installRootHelp arms the ROOT command's own help with the same
// FLAGS / GLOBAL FLAGS split the leaves get, so every command in a kit
// CLI renders the same shape at every depth. Matters most for
// single-command binaries (sidecars, plugins) whose root IS the leaf:
// the leaf-only split can never fire there, and the tool's own flags
// would otherwise sit interleaved with kit's ~two dozen globals under
// one flat FLAGS block.
//
// Why a flag-parse seam rather than a plain SetHelpFunc. fang installs
// its own renderer with root.SetHelpFunc from inside fang.Execute
// (fang.go), which runs AFTER prepareTree — anything we set on the root
// here is overwritten before a single line is rendered. Leaves are
// immune because fang only touches the root and makeLeafHelpFunc
// resolves root.HelpFunc() lazily, at render time.
//
// cobra's own order gives us the seam: Command.execute() parses flags
// (command.go:919) and only then returns flag.ErrHelp, at which point
// ExecuteC resolves cmd.HelpFunc() (command.go:1153). Wrapping the
// root's --help pflag.Value means our Set runs between fang's install
// and that lazy resolve, so the value we capture is fang's renderer and
// the one we install is what cobra reaches for. --help-all takes the
// same route: applyGroupVisibility unhides the plumbing flags and
// rewrites the token to --help, which parses through this same Value.
//
// The swap is idempotent — a Root executed twice re-enters Set with the
// wrapper already in place, and the guard keeps it from stacking.
func (r *Root) installRootHelp(hiddenDefault map[string]struct{}) {
	root := r.Cmd
	// cobra registers --help at the last possible moment; do it now so
	// there is a Value to wrap.
	root.InitDefaultHelpFlag()
	f := root.Flags().Lookup("help")
	if f == nil {
		return
	}
	if _, already := f.Value.(*rootHelpSwap); already {
		return
	}
	f.Value = &rootHelpSwap{Value: f.Value, root: root, hiddenDefault: hiddenDefault}
}

// rootHelpSwap decorates the root's --help flag value. Parsing it to
// true is the signal that root help is about to render, and the last
// moment at which fang's renderer can be captured as our delegate.
type rootHelpSwap struct {
	pflag.Value
	root          *cobra.Command
	hiddenDefault map[string]struct{}
	armed         bool
}

func (s *rootHelpSwap) Set(v string) error {
	if err := s.Value.Set(v); err != nil {
		return err
	}
	if s.armed || s.String() != "true" {
		return nil
	}
	s.armed = true
	s.root.SetHelpFunc(makeRootHelpFunc(s.root.HelpFunc(), s.hiddenDefault))
	return nil
}

// Type reports the wrapped flag's type so cobra's help and completion
// still see a bool and render "--help" without a value placeholder.
func (s *rootHelpSwap) Type() string { return s.Value.Type() }

// makeRootHelpFunc mirrors makeLeafHelpFunc for the root: hide the
// globals so fang leaves them out of FLAGS, render, restore, then
// append our own GLOBAL FLAGS section.
//
// fangHelp is fang's renderer, captured at parse time — calling it is
// what keeps root help byte-identical apart from the flags that moved.
func makeRootHelpFunc(fangHelp func(*cobra.Command, []string), hiddenDefault map[string]struct{}) func(*cobra.Command, []string) {
	return func(c *cobra.Command, args []string) {
		globals := collectRootGlobals(c.Root(), hiddenDefault)

		prevHidden := make(map[*pflag.Flag]bool, len(globals))
		for _, f := range globals {
			prevHidden[f] = f.Hidden
			f.Hidden = true
		}

		fangHelp(c, args)

		for f, was := range prevHidden {
			f.Hidden = was
		}

		renderGlobalFlags(helpColorWriter(c), globals)
	}
}

// helpColorWriter wraps a command's help destination the way fang wraps
// its own (fang.go:131 — colorprofile.NewWriter over OutOrStdout with
// os.Environ()). Going through the same writer is what keeps the
// GLOBAL FLAGS section's styling in step with the rest of help: the
// profile it derives already encodes NO_COLOR, CLICOLOR_FORCE,
// TERM=dumb and "stdout is not a terminal", so none of those
// conventions need restating here.
//
// The env profile cannot see kit's own --no-color flag, which is a
// pflag bound to viper (cli.go:518), not an environment variable. When
// it is set, clamp the profile to NoTTY — the value the writer already
// uses for "strip every escape" — so the flag suppresses this section's
// styling on a terminal, where the env profile would otherwise keep it.
func helpColorWriter(c *cobra.Command) *colorprofile.Writer {
	w := colorprofile.NewWriter(c.OutOrStdout(), os.Environ())
	if noColorRequested(c) && w.Profile > colorprofile.NoTTY {
		w.Profile = colorprofile.NoTTY
	}
	return w
}

// noColorRequested reports whether --no-color was parsed on this run.
//
// Read off the root's persistent flag set rather than viper: help
// renders from cobra's own flag parse (the seam installRootHelp
// documents), which is complete by then, while the viper binding is
// only guaranteed to have been consulted once a RunE executes — and a
// --help run never reaches one.
func noColorRequested(c *cobra.Command) bool {
	f := c.Root().PersistentFlags().Lookup("no-color")
	if f == nil {
		return false
	}
	return f.Value.String() == "true"
}

// collectRootGlobals returns the flags that move out of the root's
// FLAGS section. InheritedFlags() is empty on a root by definition, so
// the source is PersistentFlags() — the same set signatureGlobalFlagSet
// calls the tool's globals, and exactly what every subcommand inherits.
//
// Root-local flags stay put: --help, --version and the --help-<group>
// family are registered non-persistently on the root, so they are
// absent from PersistentFlags() and keep rendering under FLAGS, which
// is where a reader of root help expects them.
//
// Filtering matches collectInherited — a flag its owner marked Hidden
// stays hidden unless it is one of the kit-owned plumbing defaults
// (--chdir, --config, --dry-run, …), which are suppressed from FLAGS
// for cross-language parity yet still belong in GLOBAL FLAGS. --help is
// excluded for the same reason it is there: every command shows its own.
func collectRootGlobals(root *cobra.Command, hiddenDefault map[string]struct{}) []*pflag.Flag {
	var out []*pflag.Flag
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			if _, ok := hiddenDefault[f.Name]; !ok {
				return
			}
		}
		if f.Name == "help" {
			return
		}
		out = append(out, f)
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func installHelpRecursive(root, c *cobra.Command, hiddenDefault map[string]struct{}) {
	c.SetHelpFunc(makeLeafHelpFunc(root, hiddenDefault))
	for _, sub := range c.Commands() {
		installHelpRecursive(root, sub, hiddenDefault)
	}
}

// makeLeafHelpFunc returns a cobra HelpFunc that delegates to the root
// command's HelpFunc (set by fang) with inherited persistent flags hidden,
// then appends a "GLOBAL FLAGS" section.
func makeLeafHelpFunc(root *cobra.Command, hiddenDefault map[string]struct{}) func(*cobra.Command, []string) {
	return func(c *cobra.Command, args []string) {
		inherited := collectInherited(c, hiddenDefault)

		// Hide inherited flags so fang doesn't render them under FLAGS.
		// Save prior state so we restore it after rendering — flags are
		// shared with the parent command, mutations propagate.
		prevHidden := make(map[*pflag.Flag]bool, len(inherited))
		for _, f := range inherited {
			prevHidden[f] = f.Hidden
			f.Hidden = true
		}

		// Delegate the main render to fang via root's HelpFunc.
		root.HelpFunc()(c, args)

		// Restore Hidden state before any other consumer sees the flags.
		for f, was := range prevHidden {
			f.Hidden = was
		}

		// Append our own GLOBAL FLAGS section.
		renderGlobalFlags(helpColorWriter(c), inherited)
	}
}

// collectInherited returns persistent flags inherited from ancestors of c
// that are not Hidden by their owner. The "help" flag is excluded — cobra
// treats it as inherited but every command shows its own --help.
//
// Kit-owned plumbing flags marked Hidden=true via Root.hiddenDefaultFlags
// (e.g. --chdir, --config, --dry-run) are kept hidden in the root --help
// FLAGS section for cross-language parity, but still surface here so leaf
// commands' GLOBAL FLAGS section advertises them.
func collectInherited(c *cobra.Command, hiddenDefault map[string]struct{}) []*pflag.Flag {
	var out []*pflag.Flag
	c.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			if _, ok := hiddenDefault[f.Name]; !ok {
				return
			}
		}
		if f.Name == "help" {
			return
		}
		out = append(out, f)
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// renderGlobalFlags writes a GLOBAL FLAGS section in the same visual shape
// fang uses for FLAGS. Only the heading is styled — the rows stay plain so
// output remains legible across terminals without taking a hard dependency
// on fang's unexported style objects. The header is rendered uppercase to
// match fang's title casing.
//
// w must be the colorprofile writer from helpColorWriter, not the raw
// command output: the heading's escapes are emitted unconditionally here
// and stripped there. Writing straight to OutOrStdout would leak a bold
// sequence into --no-color and non-terminal output.
func renderGlobalFlags(w io.Writer, flags []*pflag.Flag) {
	if len(flags) == 0 {
		return
	}

	// Compute the longest flag key so the description column lines up.
	keys := make([]string, len(flags))
	maxKey := 0
	for i, f := range flags {
		keys[i] = flagKey(f)
		if len(keys[i]) > maxKey {
			maxKey = len(keys[i])
		}
	}

	// Title style: uppercase, bold, padded — mirrors fang's title block.
	title := lipgloss.NewStyle().
		Bold(true).
		Padding(1, 0).
		Margin(0, 2).
		Render("GLOBAL FLAGS")
	_, _ = fmt.Fprintln(w, title)

	const leftPad = 4
	const gap = 2
	for i, f := range flags {
		key := keys[i]
		desc := titleFirst(f.Usage)
		if hasMeaningfulDefault(f) {
			desc += " (" + f.DefValue + ")"
		}
		line := strings.Repeat(" ", leftPad) + key +
			strings.Repeat(" ", maxKey-len(key)+gap) + desc
		_, _ = fmt.Fprintln(w, line)
	}
}

// flagKey formats a flag the way fang does: "-s --long" or just "--long".
func flagKey(f *pflag.Flag) string {
	if f.Shorthand == "" {
		return "--" + f.Name
	}
	return "-" + f.Shorthand + " --" + f.Name
}

// hasMeaningfulDefault matches fang's filter — bools, zero counts, and
// empty slices skip the "(default)" suffix.
func hasMeaningfulDefault(f *pflag.Flag) bool {
	switch f.DefValue {
	case "", "false", "0", "[]":
		return false
	}
	return true
}

// titleFirst capitalizes the first word so descriptions match fang's
// FlagDescription transform (titleFirstWord). Keeps output consistent
// between FLAGS and GLOBAL FLAGS sections.
func titleFirst(s string) string {
	runes := []rune(s)
	start := 0
	for start < len(runes) && unicode.IsSpace(runes[start]) {
		start++
	}
	if start >= len(runes) {
		return s
	}
	end := start
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}
	first := string(runes[start:end])
	if first == "" {
		return s
	}
	upper := strings.ToUpper(first[:1]) + first[1:]
	return string(runes[:start]) + upper + string(runes[end:])
}
