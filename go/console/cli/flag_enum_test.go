package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestWithFlagEnum_RoundTrip(t *testing.T) {
	r := &Root{}
	r.WithFlagEnum("status", "TODO", "DONE")
	got := r.FlagEnum("status")
	if len(got) != 2 || got[0] != "TODO" || got[1] != "DONE" {
		t.Fatalf("FlagEnum = %v", got)
	}
	// Returned slice is a copy: mutating it must not corrupt the registry.
	got[0] = "MUTATED"
	if r.FlagEnum("status")[0] != "TODO" {
		t.Error("FlagEnum returned a live reference to the registration")
	}
}

func TestWithFlagEnum_LastWinsAndClear(t *testing.T) {
	r := &Root{}
	r.WithFlagEnum("status", "A").WithFlagEnum("status", "B", "C")
	if got := r.FlagEnum("status"); len(got) != 2 || got[0] != "B" {
		t.Fatalf("last registration should win, got %v", got)
	}
	r.WithFlagEnum("status")
	if got := r.FlagEnum("status"); got != nil {
		t.Fatalf("empty value list should clear, got %v", got)
	}
}

func TestWithFlagEnum_NilSafe(t *testing.T) {
	var r *Root
	if r.WithFlagEnum("status", "A") != nil {
		t.Error("nil Root should return nil")
	}
	if r.FlagEnum("status") != nil {
		t.Error("nil Root should have no enums")
	}
}

// enumTree builds a two-level command tree with a persistent flag on the
// root and a local flag on the leaf, so stamping is exercised across both
// flag sets and both levels.
func enumTree() (*Root, *cobra.Command) {
	root := &cobra.Command{Use: "tool"}
	root.PersistentFlags().String("format", "table", "Output format")
	leaf := &cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error { return nil }}
	leaf.Flags().String("status", "", "Status filter")
	root.AddCommand(leaf)
	r := &Root{Cmd: root}
	return r, leaf
}

func TestApplyFlagEnums_StampsAcrossTree(t *testing.T) {
	r, leaf := enumTree()
	r.WithFlagEnum("status", "TODO", "DONE")
	r.WithFlagEnum("format", "json", "yaml")
	r.applyFlagEnums()

	if got := flagEnumValues(lookupFlag(leaf, "status")); len(got) != 2 || got[0] != "TODO" {
		t.Errorf("leaf-local flag not stamped: %v", got)
	}
	if got := flagEnumValues(lookupFlag(leaf, "format")); len(got) != 2 || got[0] != "json" {
		t.Errorf("root-persistent flag not stamped: %v", got)
	}
}

func TestApplyFlagEnums_UnknownNameIsInert(t *testing.T) {
	r, leaf := enumTree()
	r.WithFlagEnum("nosuchflag", "A", "B")
	r.applyFlagEnums() // must not panic
	if got := flagEnumValues(lookupFlag(leaf, "nosuchflag")); got != nil {
		t.Errorf("unknown flag name produced an enum: %v", got)
	}
}

func TestApplyFlagEnumHelp_AppendsOnceAndOnlyForEnums(t *testing.T) {
	r, leaf := enumTree()
	r.WithFlagEnum("status", "TODO", "DONE")
	r.applyFlagEnums()
	r.applyFlagEnums() // idempotency: must not stack parentheticals

	usage := leaf.Flags().Lookup("status").Usage
	want := "Status filter (one of: TODO, DONE)"
	if usage != want {
		t.Errorf("usage = %q, want %q", usage, want)
	}
	if got := leaf.InheritedFlags().Lookup("format").Usage; strings.Contains(got, "one of:") {
		t.Errorf("non-enum flag gained a suffix: %q", got)
	}
}

func TestApplyFlagEnumHelp_EmptyUsageGetsBareSuffix(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	root.Flags().String("mode", "", "")
	r := &Root{Cmd: root}
	r.WithFlagEnum("mode", "fast", "slow")
	r.applyFlagEnums()
	if got := root.Flags().Lookup("mode").Usage; got != "(one of: fast, slow)" {
		t.Errorf("usage = %q", got)
	}
}

func TestBindFlagEnumCompletions_ServesEnumPrefixFiltered(t *testing.T) {
	r, leaf := enumTree()
	r.WithFlagEnum("status", "TODO", "IN_PROGRESS", "DONE")
	r.applyFlagEnums()

	// Drive the registered completion the way cobra's __complete does.
	out, directive := runFlagCompletion(t, leaf, "status", "")
	if len(out) != 3 {
		t.Fatalf("empty prefix should offer every value, got %v", out)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	out, _ = runFlagCompletion(t, leaf, "status", "do")
	if len(out) != 1 || out[0] != "DONE" {
		t.Errorf("prefix %q should match DONE case-insensitively, got %v", "do", out)
	}
}

func TestBindFlagEnumCompletions_AdopterCompleterWins(t *testing.T) {
	r, leaf := enumTree()
	sentinel := []string{"adopter-supplied"}
	if err := leaf.RegisterFlagCompletionFunc("status",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return sentinel, cobra.ShellCompDirectiveNoFileComp
		}); err != nil {
		t.Fatalf("pre-register: %v", err)
	}
	r.WithFlagEnum("status", "TODO", "DONE")
	r.applyFlagEnums()

	out, _ := runFlagCompletion(t, leaf, "status", "")
	if len(out) != 1 || out[0] != "adopter-supplied" {
		t.Errorf("kit overrode the adopter's completer: %v", out)
	}
}

// runFlagCompletion invokes the completion function cobra has registered
// for the named flag on cmd's root.
func runFlagCompletion(t *testing.T, cmd *cobra.Command, flag, toComplete string) ([]string, cobra.ShellCompDirective) {
	t.Helper()
	fn, ok := cmd.GetFlagCompletionFunc(flag)
	if !ok || fn == nil {
		t.Fatalf("no completion function registered for --%s", flag)
	}
	return fn(cmd, nil, toComplete)
}

func TestFlagEnumValues_NoAnnotationIsNil(t *testing.T) {
	f := &pflag.Flag{Name: "x"}
	if got := flagEnumValues(f); got != nil {
		t.Errorf("bare flag reported an enum: %v", got)
	}
	f.Annotations = map[string][]string{FlagEnumAnnotation: {}}
	if got := flagEnumValues(f); got != nil {
		t.Errorf("empty annotation should read as no enum: %v", got)
	}
	if got := flagEnumValues(nil); got != nil {
		t.Errorf("nil flag: %v", got)
	}
}

func TestSortedFlagNames_ExcludesHidden(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	root.PersistentFlags().String("format", "", "")
	root.PersistentFlags().String("secret-plumbing", "", "")
	_ = root.PersistentFlags().MarkHidden("secret-plumbing")
	leaf := &cobra.Command{Use: "list"}
	leaf.Flags().String("status", "", "")
	root.AddCommand(leaf)

	names := sortedFlagNames(leaf, nil)
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "status") || !strings.Contains(joined, "format") {
		t.Errorf("missing visible flags: %v", names)
	}
	if strings.Contains(joined, "secret-plumbing") {
		t.Errorf("hidden flag offered as a suggestion: %v", names)
	}
	// Sorted, so ties in the suggester are deterministic.
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("not sorted: %v", names)
		}
	}
}

func TestSortedFlagNames_NilCmd(t *testing.T) {
	if got := sortedFlagNames(nil, nil); got != nil {
		t.Errorf("got %v", got)
	}
}
