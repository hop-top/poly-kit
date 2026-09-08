package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"hop.top/kit/go/console/output"
)

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"abc", "abc", 0},
		{"status", "stats", 1},   // deletion
		{"status", "statuss", 1}, // insertion
		{"status", "ststus", 1},  // substitution
		{"status", "sttaus", 2},  // transposition costs 2 without Damerau
		{"limit", "limitt", 1},
		{"kitten", "sitting", 3},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSuggestFlags(t *testing.T) {
	flags := []string{"counters", "count-only", "format", "limit", "status"}
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		// Prefix beats distance: "count" is an abbreviation, not a typo.
		{"unique prefix", "counter", []string{"counters"}},
		{"ambiguous prefix", "count", []string{"counters", "count-only"}},
		// Distance-1 typo with no prefix match.
		{"typo one edit", "stats", []string{"status"}},
		{"typo doubled char", "limitt", []string{"limit"}},
		{"typo substitution", "formaz", []string{"format"}},
		// Beyond the distance cap: no guess is better than a wrong one.
		{"far miss", "zzzzzzzznope", nil},
		{"empty", "", nil},
		// An exact name never reaches the suggester in practice, but it
		// must not suggest itself if it does.
		{"exact", "status", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := suggestFlags(c.input, flags)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("suggestFlags(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

func TestSuggestFlags_NearestTierOnly(t *testing.T) {
	// "xtats" is distance 1 from "stats" and distance 2 from "status",
	// and a prefix of neither. Only the nearest tier competes, so the
	// distance-2 candidate does not pad the shortlist into ambiguity.
	got := suggestFlags("xtats", []string{"status", "stats"})
	if len(got) != 1 || got[0] != "stats" {
		t.Errorf("got %v, want [stats]", got)
	}
}

func TestSuggestFlags_PrefixShadowsDistance(t *testing.T) {
	// "stat" prefixes both, so the prefix branch answers and the
	// distance ranking never runs — the caller was demonstrably typing
	// one of these two, and neither is a better guess than the other.
	got := suggestFlags("stat", []string{"status", "stats"})
	if len(got) != 2 {
		t.Errorf("got %v, want both prefix matches", got)
	}
}

func TestSuggestFlags_NoCandidates(t *testing.T) {
	if got := suggestFlags("status", nil); got != nil {
		t.Errorf("got %v", got)
	}
}

// parseTree builds a root+leaf pair with the enum stamped, then returns the
// leaf. Errors are produced by driving real cobra parsing, so the tests
// exercise the same path a caller hits.
func parseTree(t *testing.T) (*Root, *cobra.Command) {
	t.Helper()
	root := &cobra.Command{Use: "tool", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("format", "table", "Output format")
	leaf := &cobra.Command{
		Use:  "list",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	leaf.Flags().String("status", "", "Status filter")
	leaf.Flags().Int("counters", 0, "Counter mode")
	leaf.Flags().Int("limit", 0, "Max rows")
	root.AddCommand(leaf)

	r := &Root{Cmd: root}
	r.WithFlagEnum("status", "TODO", "IN_PROGRESS", "DONE", "SKIPPED")
	r.applyFlagEnums()
	r.installUsageClassification()
	return r, leaf
}

// parseErr runs the tree with args and returns the envelope cobra produced.
func parseErr(t *testing.T, args ...string) *output.Error {
	t.Helper()
	r, _ := parseTree(t)
	r.Cmd.SetArgs(args)
	r.Cmd.SetOut(&strings.Builder{})
	r.Cmd.SetErr(&strings.Builder{})
	err := r.Cmd.Execute()
	if err == nil {
		t.Fatalf("args %v: want an error, got nil", args)
	}
	env := toCLIError(err)
	if env == nil {
		t.Fatalf("args %v: no envelope from %v", args, err)
	}
	return env
}

func TestUnknownFlag_UniquePrefixBecomesFix(t *testing.T) {
	env := parseErr(t, "list", "--count")
	if env.Code != output.CodeUsage {
		t.Errorf("code = %q, want %q", env.Code, output.CodeUsage)
	}
	if env.ExitCode != int(ExitUsage) {
		t.Errorf("exit_code = %d, want %d", env.ExitCode, ExitUsage)
	}
	if env.SuggestedFix != "--counters" {
		t.Errorf("suggested_fix = %q, want %q", env.SuggestedFix, "--counters")
	}
	if !strings.Contains(env.Message, "--count") {
		t.Errorf("message should name the flag typed: %q", env.Message)
	}
	if len(env.Alternatives) != 0 {
		t.Errorf("unambiguous match should not also list alternatives: %v", env.Alternatives)
	}
}

func TestUnknownFlag_TypoBecomesFix(t *testing.T) {
	env := parseErr(t, "list", "--stats")
	if env.SuggestedFix != "--status" {
		t.Errorf("suggested_fix = %q, want --status", env.SuggestedFix)
	}
}

func TestUnknownFlag_InheritedFlagIsACandidate(t *testing.T) {
	// --format lives on the root's persistent set; a typo of it at the
	// leaf must still be corrected.
	env := parseErr(t, "list", "--formt")
	if env.SuggestedFix != "--format" {
		t.Errorf("suggested_fix = %q, want --format", env.SuggestedFix)
	}
}

func TestUnknownFlag_NoMatchPointsAtHelp(t *testing.T) {
	env := parseErr(t, "list", "--zzzzzzzznope")
	if env.SuggestedFix != "run 'tool list --help' for usage" {
		t.Errorf("suggested_fix = %q, want the help invocation", env.SuggestedFix)
	}
	if len(env.Alternatives) != 0 {
		t.Errorf("no match should offer no alternatives: %v", env.Alternatives)
	}
}

func TestUnknownFlag_AmbiguousListsAlternatives(t *testing.T) {
	root := &cobra.Command{Use: "tool", SilenceErrors: true, SilenceUsage: true}
	leaf := &cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error { return nil }}
	leaf.Flags().Int("counters", 0, "Counter mode")
	leaf.Flags().Bool("count-only", false, "Count only")
	root.AddCommand(leaf)
	r := &Root{Cmd: root}
	r.installUsageClassification()
	r.Cmd.SetArgs([]string{"list", "--count"})
	r.Cmd.SetOut(&strings.Builder{})
	r.Cmd.SetErr(&strings.Builder{})

	env := toCLIError(r.Cmd.Execute())
	if env == nil {
		t.Fatal("no envelope")
	}
	// A tie must NOT be resolved by guessing: the fix points at help and
	// the candidates are listed for the caller to pick from.
	if env.SuggestedFix != "run 'tool list --help' for usage" {
		t.Errorf("suggested_fix = %q, want the help invocation on a tie", env.SuggestedFix)
	}
	joined := strings.Join(env.Alternatives, ",")
	if !strings.Contains(joined, "--counters") || !strings.Contains(joined, "--count-only") {
		t.Errorf("alternatives = %v, want both candidates", env.Alternatives)
	}
}

func TestUnknownShorthand_PointsAtHelpWithoutGuessing(t *testing.T) {
	env := parseErr(t, "list", "-Z")
	if env.Code != output.CodeUsage {
		t.Errorf("code = %q", env.Code)
	}
	// A single character is within edit distance of far too much.
	if env.SuggestedFix != "run 'tool list --help' for usage" {
		t.Errorf("suggested_fix = %q, want the help invocation", env.SuggestedFix)
	}
	if len(env.Alternatives) != 0 {
		t.Errorf("shorthand should offer no guesses: %v", env.Alternatives)
	}
}

func TestMissingValue_EnumFlagRendersTheSet(t *testing.T) {
	env := parseErr(t, "list", "--status")
	if env.Code != output.CodeUsage {
		t.Errorf("code = %q, want %q", env.Code, output.CodeUsage)
	}
	if env.ExitCode != int(ExitUsage) {
		t.Errorf("exit_code = %d, want %d", env.ExitCode, ExitUsage)
	}
	if !strings.Contains(env.Cause, "--status requires a value") {
		t.Errorf("cause = %q", env.Cause)
	}
	if env.SuggestedFix != "--status TODO" {
		t.Errorf("suggested_fix = %q, want a pasteable invocation", env.SuggestedFix)
	}
	want := []string{
		"--status TODO", "--status IN_PROGRESS",
		"--status DONE", "--status SKIPPED",
	}
	if strings.Join(env.Alternatives, "|") != strings.Join(want, "|") {
		t.Errorf("alternatives = %v, want %v", env.Alternatives, want)
	}
}

func TestMissingValue_NonEnumFlagFallsBackToTypeAndUsage(t *testing.T) {
	env := parseErr(t, "list", "--limit")
	if env.SuggestedFix != "--limit <int>" {
		t.Errorf("suggested_fix = %q, want the pflag type as placeholder", env.SuggestedFix)
	}
	if !strings.Contains(env.Cause, "Max rows") {
		t.Errorf("cause should carry the flag's usage: %q", env.Cause)
	}
	if len(env.Alternatives) != 0 {
		t.Errorf("no enum means no value list: %v", env.Alternatives)
	}
}

func TestFlagParseError_UnwrapsToPflagErrorAndEnvelope(t *testing.T) {
	r, _ := parseTree(t)
	r.Cmd.SetArgs([]string{"list", "--count"})
	r.Cmd.SetOut(&strings.Builder{})
	r.Cmd.SetErr(&strings.Builder{})
	err := r.Cmd.Execute()
	if err == nil {
		t.Fatal("want an error")
	}

	// The envelope must be reachable: an adopter main reads ExitCode off it
	// to pick the process exit code.
	var env *output.Error
	if !errors.As(err, &env) {
		t.Fatalf("envelope not reachable via errors.As: %v", err)
	}
	if env.ExitCode != int(ExitUsage) {
		t.Errorf("envelope ExitCode = %d, want %d", env.ExitCode, ExitUsage)
	}

	// The pflag error must stay matchable too: adding guidance is not a
	// license to sever a caller's errors.As.
	var notExist *pflag.NotExistError
	if !errors.As(err, &notExist) {
		t.Fatalf("original pflag error not reachable through the wrap: %v", err)
	}
	if notExist.GetSpecifiedName() != "count" {
		t.Errorf("GetSpecifiedName = %q", notExist.GetSpecifiedName())
	}
}

func TestFlagParseError_PassesThroughUnknownTypes(t *testing.T) {
	// An error kit does not recognize must come back untouched — the exit
	// code and message are all the caller has left.
	sentinel := errSentinelForParse{}
	got := flagParseError(&cobra.Command{Use: "x"}, nil, sentinel)
	if got != error(sentinel) {
		t.Errorf("got %v, want the original error", got)
	}
	if flagParseError(&cobra.Command{Use: "x"}, nil, nil) != nil {
		t.Error("nil should stay nil")
	}
}

type errSentinelForParse struct{}

func (errSentinelForParse) Error() string { return "some other parse failure" }

// The parse-error enricher shares the root's single FlagErrorFunc with
// the usage classifier, so re-installing must neither re-wrap the hook
// nor double the rendering. Asserted behaviorally: one envelope, one
// suggestion, however many times the seam is installed.
func TestParseErrorSeam_Idempotent(t *testing.T) {
	r, _ := parseTree(t)
	r.installUsageClassification()
	r.installUsageClassification()
	if r.Cmd.Annotations[usageFlagHookAnnotation] != "true" {
		t.Error("root not marked installed")
	}

	var stderr strings.Builder
	r.Cmd.SetArgs([]string{"list", "--count"})
	r.Cmd.SetOut(&strings.Builder{})
	r.Cmd.SetErr(&stderr)
	env := toCLIError(r.Cmd.Execute())
	if env == nil {
		t.Fatal("no envelope")
	}
	if env.SuggestedFix != "--counters" {
		t.Errorf("fix = %q, want --counters", env.SuggestedFix)
	}
	// A re-wrapped hook would enrich, then enrich the envelope again.
	if n := strings.Count(stderr.String(), "unknown flag"); n > 1 {
		t.Errorf("rendered %d times, want at most 1:\n%s", n, stderr.String())
	}
}

func TestHelpInvocation_NilCmd(t *testing.T) {
	if got := helpInvocation(nil); got != "run '--help' for usage" {
		t.Errorf("got %q", got)
	}
}
