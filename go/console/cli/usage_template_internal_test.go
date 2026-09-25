package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// usageTemplate departs from cobra's default only for empty sections,
// hidden commands and the root's ungrouped section. Elsewhere, where every
// group lists a visible command, it must render byte for byte what cobra
// renders.
func TestUsageTemplate_MatchesCobraWhenNoSectionIsEmpty(t *testing.T) {
	build := func() *cobra.Command {
		run := func(*cobra.Command, []string) {}
		root := &cobra.Command{Use: "tool", Short: "A tool", Run: run, Example: "  tool go"}
		root.Flags().Bool("local", false, "a local flag")
		root.PersistentFlags().Bool("global", false, "a global flag")
		root.AddCommand(&cobra.Command{Use: "two", Short: "Second", Run: run, Aliases: []string{"2"}})
		sub := &cobra.Command{Use: "sub", Short: "Sub"}
		sub.AddGroup(&cobra.Group{ID: "a", Title: "GROUP A"})
		sub.AddCommand(
			&cobra.Command{Use: "one", Short: "First", GroupID: "a", Run: run},
			&cobra.Command{Use: "leaf", Short: "Leaf", Run: run},
		)
		root.AddCommand(sub)
		root.InitDefaultHelpCmd()
		return root
	}

	for _, path := range [][]string{nil, {"sub"}, {"two"}} {
		want := build()
		got := build()
		got.SetUsageTemplate(usageTemplate)

		wc, _, _ := want.Find(path)
		gc, _, _ := got.Find(path)
		assert.Equal(t, wc.UsageString(), gc.UsageString(), "usage for %v", path)
	}
}
