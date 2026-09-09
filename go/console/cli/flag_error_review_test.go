package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hop-top/fang/v2"
	"github.com/spf13/cobra"
	"hop.top/kit/go/console/output"
)

// --- Non-fang drivers get the envelope ----------------------------------
//
// Root.Prepare + Cmd.ExecuteContext is a documented path, and it is also
// how the in-process runner behind the served surfaces and the conformance
// harness drive a tree. None of them installs fang's error handler, so an
// enriched parse error that is only rendered there reaches stderr as
// cobra's plaintext "Error: USAGE: unknown flag --count" — no envelope, no
// Fix. usageError is the single writer for this seam on every driver.

// nofangTree returns a prepared tree and the buffer its stderr goes to,
// wired the way an adopter driving cobra directly wires it.
func nofangTree(t *testing.T, format string) (*Root, *strings.Builder) {
	t.Helper()
	root := &cobra.Command{Use: "tool", Short: "Tool", Long: "Tool."}
	root.PersistentFlags().String("format", format, "Output format")
	leaf := &cobra.Command{
		Use: "list", Short: "List", Long: "List.",
		Annotations: map[string]string{"kit/side-effect": "read", "kit/idempotent": "true"},
		RunE:        func(*cobra.Command, []string) error { return nil },
	}
	leaf.Flags().Int("counters", 0, "Counter mode")
	root.AddCommand(leaf)

	r := &Root{Cmd: root}
	r.installUsageClassification()

	stderr := &strings.Builder{}
	root.SetOut(&strings.Builder{})
	root.SetErr(stderr)
	return r, stderr
}

func TestNonFangDriver_ParseErrorRendersEnvelope(t *testing.T) {
	r, stderr := nofangTree(t, "json")
	r.Cmd.SetArgs([]string{"list", "--count"})

	err := r.Cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("want a parse error")
	}
	got := stderr.String()
	if !strings.Contains(got, `"code": "USAGE"`) {
		t.Errorf("no JSON envelope on a non-fang driver's stderr:\n%s", got)
	}
	if !strings.Contains(got, `"suggested_fix": "--counters"`) {
		t.Errorf("the recovery guidance did not reach stderr:\n%s", got)
	}
	if strings.Contains(got, "Error: USAGE") {
		t.Errorf("cobra's own plaintext printer also ran:\n%s", got)
	}
	// The envelope is still reachable off the returned error, which is
	// what an adopter main reads its exit code from.
	var env *output.Error
	if !errors.As(err, &env) {
		t.Fatalf("returned error carries no envelope: %v", err)
	}
	if env.ExitCode != int(ExitUsage) {
		t.Errorf("exit_code = %d, want %d", env.ExitCode, ExitUsage)
	}
}

// TestNonFangDriver_RendersExactlyOnce: the single-stderr-render guarantee
// has to hold on a driver with no fang handler too. Two renderings of one
// failure is what an agent parsing stderr reads as two errors.
func TestNonFangDriver_RendersExactlyOnce(t *testing.T) {
	r, stderr := nofangTree(t, "json")
	r.Cmd.SetArgs([]string{"list", "--count"})
	_ = r.Cmd.ExecuteContext(context.Background())

	if n := strings.Count(stderr.String(), `"code"`); n != 1 {
		t.Errorf("envelope rendered %d times, want exactly 1:\n%s", n, stderr.String())
	}
}

// TestFangHandler_StaysQuietOnAMarkedError: with usageError now writing
// every parse error itself, the fang handler must recognize the marker and
// print nothing, or the fang path doubles what the seam already wrote.
func TestFangHandler_StaysQuietOnAMarkedError(t *testing.T) {
	r, _ := nofangTree(t, "json")
	r.Cmd.SetArgs([]string{"list", "--count"})
	err := r.Cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("want a parse error")
	}
	if !errors.Is(err, errAlreadyRendered) {
		t.Fatal("the seam did not mark the error as rendered")
	}

	handled := &strings.Builder{}
	r.kitErrorHandler()(handled, fang.Styles{}, err)
	if handled.Len() != 0 {
		t.Errorf("fang handler re-rendered a marked error: %q", handled.String())
	}
}

// --- Adopter FlagErrorFunc envelopes survive ----------------------------

// TestAdopterFlagErrorFunc_EnvelopeIsNotClobbered: the enricher matches
// pflag's typed errors, and an adopter envelope RETAINS the pflag error, so
// matching through it would replace the adopter's code, exit code and fix
// with kit's. installUsageClassification documents the opposite ("kit
// classifies whatever it returns bare").
func TestAdopterFlagErrorFunc_EnvelopeIsNotClobbered(t *testing.T) {
	r, stderr := nofangTree(t, "")
	// Installed before kit's hook chains onto it, which is the ordering
	// an adopter gets: cli.New, then their own SetFlagErrorFunc, then
	// Execute/Prepare.
	r.Cmd.Annotations = nil
	r.Cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		out := output.WrapError(err, "ADOPTER", 7)
		out.SuggestedFix = "--count was renamed to --counters in v2"
		return out
	})
	r.installUsageClassification()
	r.Cmd.SetArgs([]string{"list", "--count"})

	err := r.Cmd.ExecuteContext(context.Background())
	var env *output.Error
	if !errors.As(err, &env) {
		t.Fatalf("no envelope: %v", err)
	}
	if env.Code != "ADOPTER" {
		t.Errorf("code = %q, want the adopter's own", env.Code)
	}
	if env.ExitCode != 7 {
		t.Errorf("exit_code = %d, want the adopter's 7", env.ExitCode)
	}
	if env.SuggestedFix != "--count was renamed to --counters in v2" {
		t.Errorf("suggested_fix = %q, want the adopter's own", env.SuggestedFix)
	}
	// Their envelope is what reached stderr, once.
	got := stderr.String()
	if !strings.Contains(got, "ADOPTER") {
		t.Errorf("the adopter's envelope was not rendered:\n%s", got)
	}
	if n := strings.Count(got, "ADOPTER:"); n != 1 {
		t.Errorf("rendered %d times, want 1:\n%s", n, got)
	}
}

// TestAdopterFlagErrorFunc_BareErrorIsStillEnriched: pass-through applies to
// an envelope, not to everything an adopter hook returns. A hook that
// returns pflag's error unchanged (or any bare error) still gets kit's
// classification and suggestion — that is the shipped behavior.
func TestAdopterFlagErrorFunc_BareErrorIsStillEnriched(t *testing.T) {
	r, _ := nofangTree(t, "")
	r.Cmd.Annotations = nil
	r.Cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return err })
	r.installUsageClassification()
	r.Cmd.SetArgs([]string{"list", "--count"})

	err := r.Cmd.ExecuteContext(context.Background())
	var env *output.Error
	if !errors.As(err, &env) {
		t.Fatalf("no envelope: %v", err)
	}
	if env.Code != output.CodeUsage || env.SuggestedFix != "--counters" {
		t.Errorf("bare error lost its enrichment: code=%q fix=%q", env.Code, env.SuggestedFix)
	}
}

// --- Enum scoping -------------------------------------------------------

// scopedEnumTree gives two leaves a --type flag with different legal sets,
// which is the case a name-keyed registry cannot represent.
func scopedEnumTree() (*Root, *cobra.Command, *cobra.Command) {
	root := &cobra.Command{Use: "tool", Short: "Tool", Long: "Tool."}
	root.PersistentFlags().String("format", "table", "Output format")
	list := &cobra.Command{
		Use: "list", Short: "List", Long: "List.",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	list.Flags().String("type", "", "Record type")
	export := &cobra.Command{
		Use: "export", Short: "Export", Long: "Export.",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	export.Flags().String("type", "", "Output type")
	root.AddCommand(list, export)
	return &Root{Cmd: root}, list, export
}

func TestWithCommandFlagEnum_ScopesToItsCommand(t *testing.T) {
	r, list, export := scopedEnumTree()
	r.WithCommandFlagEnum("list", "type", "TASK", "TRACK")
	r.WithCommandFlagEnum("export", "type", "json", "csv")
	r.applyFlagEnums()

	if got := flagEnumValues(list.Flags().Lookup("type")); strings.Join(got, ",") != "TASK,TRACK" {
		t.Errorf("list --type = %v, want its own set", got)
	}
	if got := flagEnumValues(export.Flags().Lookup("type")); strings.Join(got, ",") != "json,csv" {
		t.Errorf("export --type = %v, want its own set", got)
	}
	// Help follows the same annotation, so the two usage lines differ too.
	if got := list.Flags().Lookup("type").Usage; !strings.Contains(got, "TASK, TRACK") {
		t.Errorf("list help = %q", got)
	}
	if got := export.Flags().Lookup("type").Usage; !strings.Contains(got, "json, csv") {
		t.Errorf("export help = %q", got)
	}
}

func TestWithCommandFlagEnum_ScopedBeatsTreeWide(t *testing.T) {
	r, list, export := scopedEnumTree()
	r.WithFlagEnum("type", "GENERIC")
	r.WithCommandFlagEnum("list", "type", "TASK", "TRACK")
	r.applyFlagEnums()

	if got := flagEnumValues(list.Flags().Lookup("type")); strings.Join(got, ",") != "TASK,TRACK" {
		t.Errorf("scoped declaration lost to the tree-wide one: %v", got)
	}
	if got := flagEnumValues(export.Flags().Lookup("type")); strings.Join(got, ",") != "GENERIC" {
		t.Errorf("export should keep the tree-wide set: %v", got)
	}
}

func TestWithCommandFlagEnum_NestedPathAndAccessors(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	parent := &cobra.Command{Use: "widget"}
	leaf := &cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error { return nil }}
	leaf.Flags().String("type", "", "Type")
	parent.AddCommand(leaf)
	root.AddCommand(parent)
	r := &Root{Cmd: root}

	// Space-separated path, and the root name may lead it: CommandPath()
	// renders that form, so pasting one back has to work.
	r.WithCommandFlagEnum("widget list", "type", "A", "B")
	r.applyFlagEnums()
	if got := flagEnumValues(leaf.Flags().Lookup("type")); strings.Join(got, ",") != "A,B" {
		t.Errorf("nested path not stamped: %v", got)
	}
	if got := r.CommandFlagEnum("widget  list", "type"); strings.Join(got, ",") != "A,B" {
		t.Errorf("CommandFlagEnum = %v", got)
	}
	if got := r.FlagEnum("type"); got != nil {
		t.Errorf("a scoped registration must not read back as tree-wide: %v", got)
	}
}

func TestWithCommandFlagEnum_UnknownPathIsInert(t *testing.T) {
	r, list, _ := scopedEnumTree()
	r.WithCommandFlagEnum("nosuch leaf", "type", "A")
	r.applyFlagEnums() // must not panic
	if got := flagEnumValues(list.Flags().Lookup("type")); got != nil {
		t.Errorf("a path naming no command stamped something: %v", got)
	}
}

func TestScopedEnum_ReachesTheParseError(t *testing.T) {
	r, list, _ := scopedEnumTree()
	r.WithCommandFlagEnum("list", "type", "TASK", "TRACK")
	r.WithCommandFlagEnum("export", "type", "json", "csv")
	r.applyFlagEnums()
	r.installUsageClassification()
	r.Cmd.SetOut(&strings.Builder{})
	r.Cmd.SetErr(&strings.Builder{})

	r.Cmd.SetArgs([]string{"list", "--type"})
	env := envelopeFrom(t, r.Cmd.Execute())
	if env.SuggestedFix != "--type TASK" {
		t.Errorf("list got the wrong leaf's enum: %q", env.SuggestedFix)
	}
	if strings.Join(env.Alternatives, ",") != "--type TASK,--type TRACK" {
		t.Errorf("alternatives = %v", env.Alternatives)
	}
	_ = list
}

// --- Annotation-only enums serve completion too --------------------------

// TestFlagEnumAnnotation_DrivesCompletion: FlagEnumAnnotation is exported as
// the contract adopters may write directly. Help and the parse-error path
// read only the annotation, so completion must too — otherwise an adopter
// using the documented key gets two of the three consumers.
func TestFlagEnumAnnotation_DrivesCompletion(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	leaf := &cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error { return nil }}
	leaf.Flags().String("mode", "", "Mode")
	leaf.Flags().Lookup("mode").Annotations = map[string][]string{
		FlagEnumAnnotation: {"fast", "slow"},
	}
	root.AddCommand(leaf)

	// No registry entry at all: the annotation is the whole declaration.
	r := &Root{Cmd: root}
	r.applyFlagEnums()

	out, directive := runFlagCompletion(t, leaf, "mode", "")
	if strings.Join(out, ",") != "fast,slow" {
		t.Errorf("completion = %v, want the annotated values", out)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v", directive)
	}
	if got := leaf.Flags().Lookup("mode").Usage; !strings.Contains(got, "one of: fast, slow") {
		t.Errorf("help = %q", got)
	}
}

// --- Hidden-but-advertised flags are suggested ---------------------------

// TestSortedFlagNames_SuggestsWhatHelpShows: kit marks its plumbing globals
// Hidden so root --help matches the cross-language parity contract, but
// leaf help still lists them under GLOBAL FLAGS. A typo of one must be
// corrected rather than answered with a pointer to the page that lists it.
func TestSortedFlagNames_SuggestsWhatHelpShows(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	root.PersistentFlags().Bool("dry-run", false, "Preview")
	root.PersistentFlags().String("truly-internal", "", "")
	for _, n := range []string{"dry-run", "truly-internal"} {
		if err := root.PersistentFlags().MarkHidden(n); err != nil {
			t.Fatal(err)
		}
	}
	leaf := &cobra.Command{Use: "delete"}
	root.AddCommand(leaf)

	hiddenDefault := map[string]struct{}{"dry-run": {}}
	joined := strings.Join(sortedFlagNames(leaf, hiddenDefault), ",")
	if !strings.Contains(joined, "dry-run") {
		t.Errorf("a flag leaf help advertises was not a candidate: %s", joined)
	}
	if strings.Contains(joined, "truly-internal") {
		t.Errorf("a flag help never shows was offered: %s", joined)
	}
	// Without the exception the advertised flag drops out, which is the
	// state the fix corrects.
	if got := strings.Join(sortedFlagNames(leaf, nil), ","); strings.Contains(got, "dry-run") {
		t.Errorf("nil hiddenDefault should suggest no Hidden flag: %s", got)
	}
}

func TestUnknownFlag_HiddenDefaultTypoIsCorrected(t *testing.T) {
	root := &cobra.Command{Use: "tool", Short: "Tool", Long: "Tool."}
	root.PersistentFlags().Bool("dry-run", false, "Preview")
	if err := root.PersistentFlags().MarkHidden("dry-run"); err != nil {
		t.Fatal(err)
	}
	leaf := &cobra.Command{
		Use: "delete", Short: "Delete", Long: "Delete.",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	root.AddCommand(leaf)

	r := &Root{Cmd: root, hiddenDefaultFlags: []string{"dry-run"}}
	r.installUsageClassification()
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})
	root.SetArgs([]string{"delete", "--dry-rn"})

	env := envelopeFrom(t, root.Execute())
	if env.SuggestedFix != "--dry-run" {
		t.Errorf("suggested_fix = %q, want --dry-run", env.SuggestedFix)
	}
}

// --- Short typed names get no confident correction ----------------------

// TestUnknownFlag_ShortNameRefusesAConfidentFix mirrors the shorthand
// branch's refusal: a two-character name is within the distance cap of most
// of a real flag table, so naming one winner asserts a certainty the match
// does not carry.
func TestUnknownFlag_ShortNameRefusesAConfidentFix(t *testing.T) {
	root := &cobra.Command{Use: "tool", Short: "Tool", Long: "Tool."}
	leaf := &cobra.Command{
		Use: "sub", Short: "Sub", Long: "Sub.",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	leaf.Flags().Bool("ab", false, "Two-char flag")
	leaf.Flags().Int("limit", 0, "Max rows")
	root.AddCommand(leaf)
	r := &Root{Cmd: root}
	r.installUsageClassification()
	root.SetOut(&strings.Builder{})
	root.SetErr(&strings.Builder{})

	cases := []struct {
		name  string
		typed string
		// wantAlt is the candidate that must be demoted out of Fix.
		wantAlt string
	}{
		{"one char", "--x", ""},
		{"two chars near a real flag", "--ax", "--ab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root.SetArgs([]string{"sub", c.typed})
			env := envelopeFrom(t, root.Execute())
			if !strings.Contains(env.SuggestedFix, "--help") {
				t.Errorf("short name got a confident fix: %q", env.SuggestedFix)
			}
			if c.wantAlt != "" && strings.Join(env.Alternatives, ",") != c.wantAlt {
				t.Errorf("alternatives = %v, want %q", env.Alternatives, c.wantAlt)
			}
		})
	}

	// The floor is a floor, not a blanket refusal: at three characters the
	// distance signal is worth a Fix again.
	root.SetArgs([]string{"sub", "--lmit"})
	if env := envelopeFrom(t, root.Execute()); env.SuggestedFix != "--limit" {
		t.Errorf("a long-enough typo lost its fix: %q", env.SuggestedFix)
	}
}

// --- helpers ------------------------------------------------------------

func envelopeFrom(t *testing.T, err error) *output.Error {
	t.Helper()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	env := toCLIError(err)
	if env == nil {
		t.Fatalf("no envelope from %v", err)
	}
	return env
}
