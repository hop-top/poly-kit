package cmdmeta

import (
	"sort"

	"github.com/spf13/cobra"
)

// destructiveNames is the one table of leaf names kit treats as
// destructive when a command carries no kit/side-effect annotation.
// Every kit gate and projection reads it through [IsDestructiveName]
// or [InferredDestructive]; none keeps its own copy.
var destructiveNames = map[string]bool{
	"delete":  true,
	"remove":  true,
	"rm":      true,
	"destroy": true,
	"purge":   true,
	"drop":    true,
}

// IsDestructiveName reports whether name is in the destructive-name
// table.
func IsDestructiveName(name string) bool { return destructiveNames[name] }

// InferredDestructive reports whether cmd is destructive by name
// alone: its kit/side-effect annotation is absent or empty and its
// leaf name is in the table. An explicit annotation, valid or not,
// always wins: a malformed value is a declaration defect, never
// inferred over.
func InferredDestructive(cmd *cobra.Command) bool {
	if cmd == nil || cmd.Annotations[KeySideEffect] != "" {
		return false
	}
	return IsDestructiveName(cmd.Name())
}

// DestructiveNames lists the table's names, sorted.
func DestructiveNames() []string {
	out := make([]string, 0, len(destructiveNames))
	for n := range destructiveNames {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
