package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestApplyFlagEnums_CompletionBoundOncePerRoot: repeated prepareTree calls
// on one Root must reach cobra's RegisterFlagCompletionFunc exactly once per
// enum flag.
//
// Cobra keeps flag-completion functions in a package-global map keyed by
// *pflag.Flag that is never pruned, and reaching it again is not free even
// when cobra refuses the duplicate: it takes the package lock and pins the
// closure argument for the duration. A served surface that rebuilds a tree
// per request drives this path once per request forever, so the bind is
// recorded on the root and skipped thereafter.
//
// Counting registrations rather than map entries is the point: cobra
// silently refuses a second registration for the same flag, so an entry
// count cannot tell a guarded bind from an unguarded one.
func TestApplyFlagEnums_CompletionBoundOncePerRoot(t *testing.T) {
	r, leaf := enumTree()
	r.WithFlagEnum("status", "TODO", "DONE")

	var registered []string
	restore := swapEnumCompletionBinder(func(cmd *cobra.Command, name string, _ []string) {
		registered = append(registered, cmd.Name()+" --"+name)
	})
	defer restore()

	for i := 0; i < 50; i++ {
		r.applyFlagEnums()
	}
	if len(registered) != 1 {
		t.Errorf("bound %d times over 50 prepares (%v), want 1", len(registered), registered)
	}
	if r.Cmd.Annotations[flagEnumCompletionAnnotation] != "true" {
		t.Error("the bind was not recorded on the root")
	}

	// The guard must not have cost the tree its completion: undo the
	// binder swap and bind for real on a fresh Root.
	restore()
	fresh, freshLeaf := enumTree()
	fresh.WithFlagEnum("status", "TODO", "DONE")
	fresh.applyFlagEnums()
	if out, _ := runFlagCompletion(t, freshLeaf, "status", ""); len(out) != 2 {
		t.Errorf("completion stopped serving values: %v", out)
	}
	_ = leaf
}

// TestApplyFlagEnums_RepeatedPrepareDoesNotStackHelp: the help suffix is
// applied in the same walk as the completion bind, so the guard must not
// let it stack either.
func TestApplyFlagEnums_RepeatedPrepareDoesNotStackHelp(t *testing.T) {
	r, leaf := enumTree()
	r.WithFlagEnum("status", "TODO", "DONE")
	for i := 0; i < 10; i++ {
		r.applyFlagEnums()
	}
	want := "Status filter (one of: TODO, DONE)"
	if got := leaf.Flags().Lookup("status").Usage; got != want {
		t.Errorf("usage = %q, want %q", got, want)
	}
}

// swapEnumCompletionBinder replaces the package's completion binder so a
// test can count the calls, and returns the restore function. Idempotent:
// calling restore twice is a no-op.
func swapEnumCompletionBinder(fn func(*cobra.Command, string, []string)) func() {
	prev := bindEnumCompletion
	bindEnumCompletion = fn
	restored := false
	return func() {
		if restored {
			return
		}
		bindEnumCompletion = prev
		restored = true
	}
}
