package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// coreGlobalFlags are the kit globals GLOBAL FLAGS keeps visible by
// default: output shape and noise, the knobs a reader of any command's
// help reaches for first, plus every global the cross-language parity
// contract requires root help to advertise (--no-hints, --offline;
// TestParityFlagsExactSet). Every other kit global folds into the
// "+N more" hint until --help-all.
var coreGlobalFlags = []string{"format", "output", "quiet", "verbose", "no-color", "no-hints", "offline"}

// groupHelpRow is the render-only flag that stands in for the
// --help-<id> family on root help. See foldGroupHelpFlags.
const groupHelpRow = "help-<group>"

// groupHelpRowWidth caps each line of the --help-<group> description
// so a tool with many groups wraps instead of running off the screen.
const groupHelpRowWidth = 64

// collapsedGlobalSet returns the globals GLOBAL FLAGS folds behind its
// --help-all hint: every kit persistent flag registered on pf so far
// except the core set, adjusted by the tool's HelpConfig.
//
// Called before the tool's own Config.Globals register, so those — and
// any persistent flag the tool adds later — show by default.
func collapsedGlobalSet(pf *pflag.FlagSet, help HelpConfig) map[string]struct{} {
	core := make(map[string]struct{}, len(coreGlobalFlags))
	for _, name := range coreGlobalFlags {
		core[name] = struct{}{}
	}
	out := make(map[string]struct{})
	pf.VisitAll(func(f *pflag.Flag) {
		if _, ok := core[f.Name]; !ok {
			out[f.Name] = struct{}{}
		}
	})
	for _, name := range help.ShowGlobals {
		delete(out, name)
	}
	for _, name := range help.CollapseGlobals {
		out[name] = struct{}{}
	}
	return out
}

// foldGroupHelpFlags hides the root's --help-<id> rows and shows one
// --help-<group> row listing the group IDs in their place, then returns
// the func that undoes it. A no-op below two groups, where the single
// --help-<id> row is already as short as it gets.
//
// The row is a real flag on the root's local set because fang renders
// FLAGS straight from c.Flags(); it is registered on first render and
// kept Hidden between renders, so it never reaches completion, parse
// suggestions, or a second Execute's help as a stray row. The --help-<id>
// flags themselves are untouched — hiding only drops their rows.
func (r *Root) foldGroupHelpFlags(c *cobra.Command) func() {
	noop := func() {}
	if c != r.Cmd {
		return noop
	}

	var ids []string
	var rows []*pflag.Flag
	for id := range r.groupTitles {
		if f := c.Flags().Lookup("help-" + id); f != nil && !f.Hidden {
			ids = append(ids, id)
			rows = append(rows, f)
		}
	}
	if len(rows) < 2 {
		return noop
	}
	sort.Strings(ids)

	row := c.Flags().Lookup(groupHelpRow)
	if row == nil {
		c.Flags().Bool(groupHelpRow, false, "")
		row = c.Flags().Lookup(groupHelpRow)
	}
	row.Usage = groupHelpRowUsage(ids)
	row.Hidden = false
	for _, f := range rows {
		f.Hidden = true
	}

	return func() {
		row.Hidden = true
		for _, f := range rows {
			f.Hidden = false
		}
	}
}

// groupHelpRowUsage describes the --help-<group> row, wrapping the ID
// list onto continuation lines (fang renders multi-line usage aligned).
func groupHelpRowUsage(ids []string) string {
	var lines []string
	line := "Show only one group's commands:"
	for i, id := range ids {
		word := " " + id
		if i < len(ids)-1 {
			word += ","
		}
		if len(line)+len(word) > groupHelpRowWidth {
			lines = append(lines, line)
			line = strings.TrimPrefix(word, " ")
			continue
		}
		line += word
	}
	return strings.Join(append(lines, line), "\n")
}
