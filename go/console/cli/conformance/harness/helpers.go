package harness

import (
	"strings"

	"github.com/spf13/cobra"
)

// resolveLeaf walks cmd's tree using args to find the leaf cobra
// command the harness is asserting against. The cobra "Find"
// helper returns the deepest matching command plus the leftover
// argv tail; the harness only cares about the leaf so the tail is
// discarded.
//
// Returns nil when cmd is nil or when Find errors (which only
// happens for unknown subcommands).
func resolveLeaf(cmd *cobra.Command, args []string) *cobra.Command {
	if cmd == nil {
		return nil
	}
	leaf, _, err := cmd.Find(args)
	if err != nil || leaf == nil {
		return cmd
	}
	return leaf
}

// walkLeaves invokes fn on every runnable leaf under root, skipping
// cobra built-ins (help, completion, __complete*) the way
// toolspec/cli does.
func walkLeaves(root *cobra.Command, fn func(*cobra.Command)) {
	if root == nil {
		return
	}
	if !root.HasSubCommands() {
		if isCobraBuiltin(root) {
			return
		}
		fn(root)
		return
	}
	for _, c := range root.Commands() {
		walkLeaves(c, fn)
	}
}

func isCobraBuiltin(cmd *cobra.Command) bool {
	if cmd == nil {
		return true
	}
	switch cmd.Name() {
	case "help", "completion", "man", "__complete", "__completeNoDesc":
		return true
	}
	if p := cmd.Parent(); p != nil && p.Name() == "completion" {
		return true
	}
	return !cmd.Runnable()
}

// trimCommandPath strips the leading root name from a cobra
// CommandPath, leaving just the leaf-under-root path components
// for matching against WithLeafExitOverride keys.
func trimCommandPath(root *cobra.Command, leaf *cobra.Command) string {
	if leaf == nil {
		return ""
	}
	path := leaf.CommandPath()
	if root != nil {
		prefix := root.Name() + " "
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
		} else if path == root.Name() {
			path = ""
		}
	}
	return path
}
