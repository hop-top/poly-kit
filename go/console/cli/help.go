package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// installLeafHelp walks the command tree and installs a leaf-aware help
// renderer on every non-root command. The renderer asks fang to render help
// with inherited persistent flags hidden, then appends a separate
// "GLOBAL FLAGS" section listing those inherited flags. Mirrors the
// kubectl/gh/docker convention so leaf help shows only command-specific
// flags by default.
//
// Root help is left untouched — fang renders it normally with all flags
// under FLAGS.
func (r *Root) installLeafHelp() {
	root := r.Cmd
	hiddenDefault := make(map[string]struct{}, len(r.hiddenDefaultFlags))
	for _, name := range r.hiddenDefaultFlags {
		hiddenDefault[name] = struct{}{}
	}
	for _, c := range root.Commands() {
		installHelpRecursive(root, c, hiddenDefault)
	}
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
		renderGlobalFlags(c.OutOrStdout(), inherited)
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
// fang uses for FLAGS. We render uncolored so output stays legible across
// terminals without taking a hard dependency on fang's unexported style
// objects. The header is rendered uppercase to match fang's title casing.
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
