package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The candidate filter, unit-level. The built-binary tests in
// go/console/cli/flagerrexit own the end-to-end behavior; these pin the
// three filters independently so a regression names which one broke.

// typoTwoEdits is a transposition of "name" at Levenshtein distance 2 —
// the case that separates the suggester (distance <=2) from the corrector
// (distance <=1). Named rather than inlined because it is a deliberate
// misspelling used as test input, and the spell checker cannot tell that
// from prose.
const typoTwoEdits = "nm" + "ae"

func TestCorrectionCandidate(t *testing.T) {
	// No two candidates share a prefix with a typo used below: "nam"
	// prefixes both "name" and "namespace", which is an ambiguity that
	// belongs in its own case rather than silently shadowing the
	// one-edit case this table opens with.
	cands := []string{"name", "format", "force", "stage", "stale"}

	tests := []struct {
		name  string
		typed string
		want  string
		why   string
	}{
		{
			name:  "one edit, single match",
			typed: "nam",
			want:  "name",
			why:   "the case the feature exists for",
		},
		{
			name:  "two edits refused",
			typed: typoTwoEdits,
			want:  "",
			why:   "distance 2 suggests but never rewrites",
		},
		{
			name:  "ambiguous tie refused",
			typed: "stace",
			want:  "",
			why:   "--stage and --stale are equidistant; there is no winner",
		},
		{
			name:  "below length floor refused",
			typed: "na",
			want:  "",
			why:   "a two-character name is within one edit of too much",
		},
		{
			name:  "one edit with a substitution",
			typed: "nam3",
			want:  "name",
			why:   "one substitution, and no prefix of anything else",
		},
		{
			name:  "no candidate at all",
			typed: "zzzzzz",
			want:  "",
			why:   "nothing is close enough to name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := correctionCandidate(tt.typed, cands); got != tt.want {
				t.Errorf("correctionCandidate(%q) = %q, want %q (%s)",
					tt.typed, got, tt.want, tt.why)
			}
		})
	}
}

// TestCorrectionCandidate_Prefix covers the abbreviation signal on its
// own candidate set, where a prefix is unique.
//
// A prefix is applied past the one-edit ceiling ("namesp" is three edits
// from "namespace") because it is not a typo at all: it is the flag the
// caller was demonstrably in the middle of typing. A prefix shared by two
// candidates is refused for the same reason any tie is.
func TestCorrectionCandidate_Prefix(t *testing.T) {
	if got := correctionCandidate("namesp", []string{"namespace", "format"}); got != "namespace" {
		t.Errorf("unique prefix: got %q, want namespace", got)
	}
	if got := correctionCandidate("nam", []string{"name", "namespace"}); got != "" {
		t.Errorf("ambiguous prefix: got %q, want no correction", got)
	}
}

// TestCorrectionCandidate_TighterThanSuggestion is the relationship the
// plan fixes between the two thresholds: every correction is a
// suggestion, but not every suggestion is a correction. A change that
// widens the corrector to match the suggester breaks this.
func TestCorrectionCandidate_TighterThanSuggestion(t *testing.T) {
	cands := []string{"name", "format"}
	// Suggested (distance 2) but not corrected (distance > 1).
	if got := suggestFlags(typoTwoEdits, cands); len(got) != 1 || got[0] != "name" {
		t.Fatalf("premise changed: suggestFlags(%s) = %v, want [name]", typoTwoEdits, got)
	}
	if got := correctionCandidate(typoTwoEdits, cands); got != "" {
		t.Errorf("correctionCandidate(%s) = %q; the corrector must be tighter "+
			"than the suggester", typoTwoEdits, got)
	}
}

// --- the side-effect gate ------------------------------------------------

func TestAutocorrectAllowed(t *testing.T) {
	leaf := func(se string) *cobra.Command {
		c := &cobra.Command{Use: "x"}
		if se != "" {
			c.Annotations = map[string]string{sideEffectAnnotation: se}
		}
		return c
	}

	tests := []struct {
		name           string
		sideEffect     string
		mode           AutocorrectMode
		wantAllowed    bool
		wantDefaultYes bool
	}{
		{"read leaf in read mode", "read", AutocorrectRead, true, false},
		{"write leaf in read mode", "write", AutocorrectRead, false, false},
		{"write-local in read mode", "write-local", AutocorrectRead, false, false},
		{"write-shared in read mode", "write-shared", AutocorrectRead, false, false},
		{"destructive in read mode", "destructive", AutocorrectRead, false, false},
		{"destructive-local in read mode", "destructive-local", AutocorrectRead, false, false},
		{"destructive-shared in read mode", "destructive-shared", AutocorrectRead, false, false},
		{"interactive in read mode", "interactive", AutocorrectRead, false, false},
		{"unannotated in read mode", "", AutocorrectRead, false, false},

		{"read leaf in prompt mode defaults yes", "read", AutocorrectPrompt, true, true},
		{"write leaf in prompt mode defaults no", "write", AutocorrectPrompt, true, false},
		{"destructive in prompt mode defaults no", "destructive", AutocorrectPrompt, true, false},
		{"unannotated in prompt mode defaults no", "", AutocorrectPrompt, true, false},

		{"read leaf in off mode", "read", AutocorrectOff, false, false},
		{"destructive in off mode", "destructive", AutocorrectOff, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, defaultYes := autocorrectAllowed(leaf(tt.sideEffect), tt.mode)
			if allowed != tt.wantAllowed {
				t.Errorf("allowed = %v, want %v", allowed, tt.wantAllowed)
			}
			if defaultYes != tt.wantDefaultYes {
				t.Errorf("defaultYes = %v, want %v", defaultYes, tt.wantDefaultYes)
			}
		})
	}
}

// TestAutocorrectAllowed_ReadModeRefusesEveryNonRead is the gate stated
// as one property rather than a table row, so a newly added side-effect
// tier is covered without editing this test: read mode allows exactly the
// read tier and nothing else.
func TestAutocorrectAllowed_ReadModeRefusesEveryNonRead(t *testing.T) {
	for se := range validSideEffects {
		cmd := &cobra.Command{Use: "x", Annotations: map[string]string{
			sideEffectAnnotation: string(se),
		}}
		allowed, _ := autocorrectAllowed(cmd, AutocorrectRead)
		want := se == SideEffectRead
		if allowed != want {
			t.Errorf("side-effect %q: allowed = %v, want %v", se, allowed, want)
		}
	}
}

// --- policy resolution ---------------------------------------------------

func TestParseAutocorrectMode(t *testing.T) {
	tests := []struct {
		raw    string
		want   AutocorrectMode
		wantOK bool
	}{
		{"off", AutocorrectOff, true},
		{"prompt", AutocorrectPrompt, true},
		{"read", AutocorrectRead, true},
		{"READ", AutocorrectRead, true},
		{"  prompt  ", AutocorrectPrompt, true},
		{"", AutocorrectOff, false},
		{"yes", AutocorrectOff, false},
		{"write", AutocorrectOff, false},
		{"on", AutocorrectOff, false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, ok := parseAutocorrectMode(tt.raw)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("parseAutocorrectMode(%q) = (%q, %v), want (%q, %v)",
					tt.raw, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestAutocorrectMode_DefaultsOff is non-negotiable 1 at the resolver.
func TestAutocorrectMode_DefaultsOff(t *testing.T) {
	r := New(Config{Name: "t", Version: "0", Short: "t", DisableValidate: true})
	if got := r.autocorrectMode(r.Cmd); got != AutocorrectOff {
		t.Errorf("autocorrectMode with nothing set = %q, want off", got)
	}
}

// TestAutocorrectMode_EnvBeatsConfig covers the middle two rungs: the
// environment overrides a config-file value.
func TestAutocorrectMode_EnvBeatsConfig(t *testing.T) {
	r := New(Config{Name: "t", Version: "0", Short: "t", DisableValidate: true})
	r.Viper.Set(autocorrectViperKey, "prompt")
	if got := r.autocorrectMode(r.Cmd); got != AutocorrectPrompt {
		t.Fatalf("config rung not read: got %q, want prompt", got)
	}
	t.Setenv(autocorrectEnv, "read")
	if got := r.autocorrectMode(r.Cmd); got != AutocorrectRead {
		t.Errorf("env did not beat config: got %q, want read", got)
	}
}

// TestAutocorrectMode_BogusEnvFallsThrough: an unrecognized value at one
// rung is skipped rather than read as off, so a lower rung still applies.
func TestAutocorrectMode_BogusEnvFallsThrough(t *testing.T) {
	r := New(Config{Name: "t", Version: "0", Short: "t", DisableValidate: true})
	r.Viper.Set(autocorrectViperKey, "read")
	t.Setenv(autocorrectEnv, "sure-why-not")
	if got := r.autocorrectMode(r.Cmd); got != AutocorrectRead {
		t.Errorf("a bogus env value swallowed the config rung: got %q, want read", got)
	}
}

// TestAutocorrectFlag_IsRegisteredAndHidden: the global exists on every
// root and stays out of the parity-contract help surface, like the other
// kit-owned plumbing flags.
func TestAutocorrectFlag_IsRegisteredAndHidden(t *testing.T) {
	r := New(Config{Name: "t", Version: "0", Short: "t", DisableValidate: true})
	f := r.Cmd.PersistentFlags().Lookup(autocorrectFlag)
	if f == nil {
		t.Fatal("--autocorrect is not registered on the root")
	}
	if !f.Hidden {
		t.Error("--autocorrect should be hidden plumbing, like --confirm")
	}
	if f.DefValue != "" {
		t.Errorf("--autocorrect default = %q, want empty (resolves to off)", f.DefValue)
	}
}

// TestAutocorrectFlag_IsSuggestable keeps --autocorrect in the candidate
// set a typo is matched against, the same way --dry-run and --confirm are:
// a hidden-but-documented global is interface, so a typo of it gets
// corrected rather than answered with a help pointer.
func TestAutocorrectFlag_IsSuggestable(t *testing.T) {
	r := New(Config{Name: "t", Version: "0", Short: "t", DisableValidate: true})
	// Asserted at a LEAF, not at the root: the flag is persistent, so it
	// reaches the candidate set through InheritedFlags, which only a
	// child has. That is also the only place a typo of it can be typed.
	leaf := &cobra.Command{
		Use:         "list",
		Short:       "List",
		Annotations: map[string]string{sideEffectAnnotation: "read"},
		RunE:        func(*cobra.Command, []string) error { return nil },
	}
	r.Cmd.AddCommand(leaf)
	names := sortedFlagNames(leaf, r.hiddenDefaultFlagSet())
	for _, n := range names {
		if n == autocorrectFlag {
			return
		}
	}
	t.Errorf("--autocorrect is not among the suggestable names: %v", names)
}

// --- argv rewriting ------------------------------------------------------

func TestCorrectArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    []string
		changed bool
	}{
		{
			name:    "bare flag",
			args:    []string{"show", "--nam"},
			want:    []string{"show", "--name"},
			changed: true,
		},
		{
			name:    "flag with inline value",
			args:    []string{"show", "--nam=x"},
			want:    []string{"show", "--name=x"},
			changed: true,
		},
		{
			name:    "inline value containing the typed name",
			args:    []string{"show", "--nam=nam"},
			want:    []string{"show", "--name=nam"},
			changed: true,
		},
		{
			name:    "separate value token is left alone",
			args:    []string{"show", "--nam", "nam"},
			want:    []string{"show", "--name", "nam"},
			changed: true,
		},
		{
			name:    "nothing after a bare -- is rewritten",
			args:    []string{"show", "--", "--nam"},
			want:    []string{"show", "--", "--nam"},
			changed: false,
		},
		{
			name:    "token absent",
			args:    []string{"show", "--other"},
			want:    []string{"show", "--other"},
			changed: false,
		},
		{
			name:    "a positional that merely resembles the name",
			args:    []string{"show", "nam"},
			want:    []string{"show", "nam"},
			changed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := correctArgs(tt.args, "nam", "name")
			if changed != tt.changed {
				t.Errorf("changed = %v, want %v", changed, tt.changed)
			}
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Errorf("correctArgs = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCorrectArgs_DoesNotMutateInput: the caller's argv is still the
// typed one afterwards, so an audit record built from it shows what the
// user wrote.
func TestCorrectArgs_DoesNotMutateInput(t *testing.T) {
	in := []string{"show", "--nam=x"}
	if _, changed := correctArgs(in, "nam", "name"); !changed {
		t.Fatal("premise: expected a rewrite")
	}
	if in[1] != "--nam=x" {
		t.Errorf("input argv was mutated in place: %v", in)
	}
}

// --- the envelope --------------------------------------------------------

// TestCorrectionNotice_CarriesBothHalves is non-negotiable 4 at the
// constructor: an audit record needs the typed token as well as the
// corrected one, and neither is reconstructable from the other.
func TestCorrectionNotice_CarriesBothHalves(t *testing.T) {
	n := correctionNotice("nam", "name")
	if n.CorrectedFrom != "--nam" {
		t.Errorf("CorrectedFrom = %q, want --nam", n.CorrectedFrom)
	}
	if n.SuggestedFix != "--name" {
		t.Errorf("SuggestedFix = %q, want --name", n.SuggestedFix)
	}
	if n.ExitCode != 0 {
		t.Errorf("ExitCode = %d; a notice is not a failure", n.ExitCode)
	}
	if !strings.Contains(n.Message, "--nam") || !strings.Contains(n.Message, "--name") {
		t.Errorf("Message names only one half: %q", n.Message)
	}
}

// --- two prompts, one terminal -------------------------------------------

// TestPromptAutocorrect_ThenConfirm_ShareOneTerminal is the regression
// the shared-terminal migration exists to prevent.
//
// Both prompts kit can ask in a single invocation are driven here in
// order, over ONE terminal: the autocorrect question fires on a mistyped
// flag, and the corrected re-dispatch then reaches the confirm gate. The
// answers are supplied as a single script, so the first read buffers past
// its own newline and the second prompt can only succeed by reading what
// the first left behind.
//
// This goes RED two ways, which is the point of pinning it here rather
// than only on a pty (whose canonical line discipline hands out one line
// per read and hides the defect entirely):
//
//   - a bufio.Reader built per prompt discards the buffered "y\n", so the
//     confirm prompt sees EOF and aborts;
//   - a prompt that closes the terminal on return leaves the confirm gate
//     with nothing to ask on at all.
func TestPromptAutocorrect_ThenConfirm_ShareOneTerminal(t *testing.T) {
	var asked strings.Builder
	src := PromptSourceFromReadWriter(strings.NewReader("y\ny\n"), &asked)

	applied, wasAsked := promptAutocorrect(src, "forc", "force", false)
	if !wasAsked {
		t.Fatal("the autocorrect prompt reported no terminal to ask on")
	}
	if !applied {
		t.Fatal("the autocorrect prompt did not read its answer")
	}

	// The second prompt on the same terminal. Its answer was buffered by
	// the first prompt's read; only a retained reader still has it.
	if !promptConfirm(src, "This is a destructive operation. Continue?") {
		t.Error("the confirm prompt lost its answer to the autocorrect prompt's buffer")
	}

	// Both questions reached the terminal, in order, and neither went to
	// stderr.
	out := asked.String()
	if !strings.Contains(out, "Did you mean --force instead of --forc?") {
		t.Errorf("the autocorrect question never reached the terminal:\n%s", out)
	}
	if !strings.Contains(out, "Continue?") {
		t.Errorf("the confirm question never reached the terminal:\n%s", out)
	}
	if strings.Index(out, "Did you mean") > strings.Index(out, "Continue?") {
		t.Errorf("the questions reached the terminal out of order:\n%s", out)
	}
}

// TestPromptAutocorrect_ThenConfirm_SecondAnswerIsRead is the control for
// the test above: same two prompts over one terminal, but the confirm
// answer is "n". A second prompt that silently defaulted instead of
// reading would pass the accept case and fail here.
func TestPromptAutocorrect_ThenConfirm_SecondAnswerIsRead(t *testing.T) {
	src := PromptSourceFromReadWriter(strings.NewReader("y\nn\n"), &strings.Builder{})

	if applied, _ := promptAutocorrect(src, "forc", "force", false); !applied {
		t.Fatal("the autocorrect prompt did not read its answer")
	}
	if promptConfirm(src, "Continue?") {
		t.Error("the confirm prompt did not read the 'n' the script supplied")
	}
}

// TestPromptAutocorrect_ThenConfirm_ThirdPromptStillReads extends the
// chain past two, so the assertion is "the buffer is retained" rather
// than "the first prompt happened to leave one line behind".
func TestPromptAutocorrect_ThenConfirm_ThirdPromptStillReads(t *testing.T) {
	src := PromptSourceFromReadWriter(strings.NewReader("y\ny\ny\n"), &strings.Builder{})

	if applied, _ := promptAutocorrect(src, "forc", "force", false); !applied {
		t.Fatal("prompt 1 did not read its answer")
	}
	if !promptConfirm(src, "first?") {
		t.Fatal("prompt 2 did not read its answer")
	}
	if !promptConfirm(src, "second?") {
		t.Error("prompt 3 did not read its answer")
	}
}

// --- the prompt's own answers --------------------------------------------

// TestPromptAutocorrect_Answers pins the answer vocabulary and the
// side-effect-chosen default, including the hint the question advertises.
func TestPromptAutocorrect_Answers(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		defaultYes bool
		want       bool
		wantHint   string
	}{
		{"y accepts", "y\n", false, true, "[y/N]"},
		{"yes accepts", "yes\n", false, true, "[y/N]"},
		{"uppercase Y accepts", "Y\n", false, true, "[y/N]"},
		{"n declines", "n\n", true, false, "[Y/n]"},
		{"anything else declines", "maybe\n", true, false, "[Y/n]"},
		{"Enter takes the yes default on a read leaf", "\n", true, true, "[Y/n]"},
		{"Enter declines on a mutating leaf", "\n", false, false, "[y/N]"},
		// EOF is the operator ending the question, never consent: it
		// declines even where a bare Enter would have accepted.
		{"EOF declines despite the yes default", "", true, false, "[Y/n]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var asked strings.Builder
			src := PromptSourceFromReadWriter(strings.NewReader(tc.script), &asked)

			applied, wasAsked := promptAutocorrect(src, "nam", "name", tc.defaultYes)
			if !wasAsked {
				t.Fatal("asked = false with a terminal present")
			}
			if applied != tc.want {
				t.Errorf("applied = %v, want %v", applied, tc.want)
			}
			if !strings.Contains(asked.String(), tc.wantHint) {
				t.Errorf("question %q does not advertise %s", asked.String(), tc.wantHint)
			}
		})
	}
}

// TestPromptAutocorrect_NoTerminalNeverBlocks is the non-TTY fallback at
// unit level: asked=false, and no answer invented.
func TestPromptAutocorrect_NoTerminalNeverBlocks(t *testing.T) {
	noTTY := PromptSource(func() *PromptTTY { return nil })

	applied, asked := promptAutocorrect(noTTY, "nam", "name", true)
	if asked {
		t.Error("asked = true with no terminal to ask on")
	}
	if applied {
		t.Error("a correction was applied with nobody to ask")
	}
}
