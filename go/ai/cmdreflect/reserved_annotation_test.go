package cmdreflect

import (
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/cmdmeta"
)

// TestReservedAnnotationIsManagementOnlyWithoutALookup pins that a
// subtree a kit factory marked reserved is management-only on every
// surface, nested or not, whether or not the reflection has the
// root's reserved-name lookup; and that AllowReserved still lifts it.
func TestReservedAnnotationIsManagementOnlyWithoutALookup(t *testing.T) {
	build := func() *cobra.Command {
		root := &cobra.Command{Use: "tool"}
		group := &cobra.Command{Use: "admin"}
		managed := &cobra.Command{Use: "migrate"}
		run := &cobra.Command{Use: "run", RunE: func(*cobra.Command, []string) error { return nil },
			Annotations: map[string]string{annSideEffect: "write-local"}}
		other := &cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error { return nil },
			Annotations: map[string]string{annSideEffect: "read"}}
		root.AddCommand(group)
		group.AddCommand(managed, other)
		managed.AddCommand(run)
		cmdmeta.SetReserved(managed)
		return root
	}

	for name, opts := range map[string][]Option{
		"no lookup":   nil,
		"with lookup": {WithReserved(fakeReserved{})},
	} {
		t.Run(name, func(t *testing.T) {
			tree := Reflect(build(), opts...)
			d := tree.Lookup("admin migrate run")
			if d == nil {
				t.Fatal("admin migrate run not reflected")
			}
			if d.Invocable || d.Reason != ReasonManagementOnly {
				t.Errorf("admin migrate run: invocable=%v reason=%q, want management-only", d.Invocable, d.Reason)
			}
			if o := tree.Lookup("admin list"); o == nil || !o.Invocable {
				t.Errorf("admin list: want invocable, got %+v", o)
			}
		})
	}

	tree := Reflect(build(), AllowReserved())
	if d := tree.Lookup("admin migrate run"); d == nil || !d.Invocable || !d.Surface.Reserved {
		t.Errorf("AllowReserved: want invocable and still marked reserved, got %+v", d)
	}
}
