package cmdmeta_test

import (
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/cmdmeta"
)

// TestReservedIsATreeProperty pins that a command marked reserved
// reserves itself and everything under it, wherever it is mounted,
// and nothing beside it.
func TestReservedIsATreeProperty(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	group := &cobra.Command{Use: "config"}
	managed := &cobra.Command{Use: "alias"}
	leaf := &cobra.Command{Use: "add"}
	sibling := &cobra.Command{Use: "show"}
	root.AddCommand(group)
	group.AddCommand(managed, sibling)
	managed.AddCommand(leaf)

	cmdmeta.SetReserved(managed)
	for _, c := range []*cobra.Command{managed, leaf} {
		if !cmdmeta.InReservedTree(c) {
			t.Errorf("%s: want reserved", c.CommandPath())
		}
	}
	for _, c := range []*cobra.Command{root, group, sibling} {
		if cmdmeta.InReservedTree(c) {
			t.Errorf("%s: want not reserved", c.CommandPath())
		}
	}
	if cmdmeta.InReservedTree(nil) {
		t.Error("nil: want not reserved")
	}
}
