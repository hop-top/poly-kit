package flagerrexit_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Opt-in flag autocorrect, asserted on the built binary.
//
// Every assertion here needs a real process: the exit code, whether the
// leaf actually ran, and — for the prompt arm — a controlling terminal
// distinct from the process's stdin. None of the three is observable from
// inside a test process.

// runEnv is run with extra environment entries, for the KIT_AUTOCORRECT
// precedence assertions.
func runEnv(t *testing.T, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(toolBin, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// --- default off ---------------------------------------------------------

// TestAutocorrect_DefaultOff_SuggestsOnly is non-negotiable 1. Without an
// explicit policy the behavior is byte-for-byte the suggest-only contract:
// exit 2, a fix on stderr, and the leaf never runs.
func TestAutocorrect_DefaultOff_SuggestsOnly(t *testing.T) {
	got := run(t, "show", "--nam=x")
	if got.code != 2 {
		t.Errorf("exit = %d, want 2\nstderr:\n%s", got.code, got.stderr)
	}
	if strings.Contains(got.stdout, "SHOW") {
		t.Errorf("the leaf ran under the default policy\nstdout:\n%s", got.stdout)
	}
	if !strings.Contains(got.stderr, "Fix: --name") {
		t.Errorf("stderr lost the suggestion:\n%s", got.stderr)
	}
	if strings.Contains(got.stderr, "corrected_from") ||
		strings.Contains(got.stderr, "Corrected from") {
		t.Errorf("an uncorrected run reported a correction:\n%s", got.stderr)
	}
}

// TestAutocorrect_OffIsExplicitlyHonored covers the value, not just the
// absence: --autocorrect=off must not be read as "unset, fall through".
func TestAutocorrect_OffIsExplicitlyHonored(t *testing.T) {
	got := run(t, "--autocorrect=off", "show", "--nam=x")
	if got.code != 2 {
		t.Errorf("exit = %d, want 2\nstderr:\n%s", got.code, got.stderr)
	}
	if strings.Contains(got.stdout, "SHOW") {
		t.Errorf("--autocorrect=off still ran the leaf\nstdout:\n%s", got.stdout)
	}
}

// TestAutocorrect_ConfirmYesDoesNotImply is the separate-consents rule.
// A script that passes --confirm=yes has consented to a destructive
// action, not to kit guessing which flag it meant.
func TestAutocorrect_ConfirmYesDoesNotImply(t *testing.T) {
	got := run(t, "--confirm=yes", "show", "--nam=x")
	if got.code != 2 {
		t.Errorf("exit = %d, want 2\nstderr:\n%s", got.code, got.stderr)
	}
	if strings.Contains(got.stdout, "SHOW") {
		t.Errorf("--confirm=yes implied autocorrect\nstdout:\n%s", got.stdout)
	}
}

// --- read mode -----------------------------------------------------------

// TestAutocorrect_ReadMode_AppliesAndRuns is the feature working: a
// read-only leaf, an unambiguous one-edit typo, and the corrected VALUE
// reaching the command rather than the typed token.
func TestAutocorrect_ReadMode_AppliesAndRuns(t *testing.T) {
	got := run(t, "--autocorrect=read", "show", "--nam=x")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "SHOW name=x") {
		t.Errorf("the corrected flag's value never reached the leaf\nstdout:\n%s", got.stdout)
	}
}

// TestAutocorrect_ReadMode_SeparateValueToken covers `--nam x` (two argv
// elements) alongside the `--nam=x` form: only the name is rewritten, so
// the value survives as its own token.
func TestAutocorrect_ReadMode_SeparateValueToken(t *testing.T) {
	got := run(t, "--autocorrect=read", "show", "--nam", "sep")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "SHOW name=sep") {
		t.Errorf("value token was lost in the rewrite\nstdout:\n%s", got.stdout)
	}
}

// TestAutocorrect_EnvVarEnables is the KIT_AUTOCORRECT rung.
func TestAutocorrect_EnvVarEnables(t *testing.T) {
	got := runEnv(t, []string{"KIT_AUTOCORRECT=read"}, "show", "--nam=x")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "SHOW name=x") {
		t.Errorf("KIT_AUTOCORRECT=read did not apply\nstdout:\n%s", got.stdout)
	}
}

// TestAutocorrect_FlagBeatsEnv is the precedence order: the command line
// is the highest rung, so an explicit off overrides the environment.
func TestAutocorrect_FlagBeatsEnv(t *testing.T) {
	got := runEnv(t, []string{"KIT_AUTOCORRECT=read"},
		"--autocorrect=off", "show", "--nam=x")
	if got.code != 2 {
		t.Errorf("exit = %d, want 2 (flag must beat env)\nstderr:\n%s", got.code, got.stderr)
	}
	if strings.Contains(got.stdout, "SHOW") {
		t.Errorf("env won over an explicit --autocorrect=off\nstdout:\n%s", got.stdout)
	}
}

// TestAutocorrect_BogusModeFallsThroughToOff: a mistyped policy value is
// ignored, not fatal, and the default stands.
func TestAutocorrect_BogusModeFallsThroughToOff(t *testing.T) {
	got := run(t, "--autocorrect=yeah-sure", "show", "--nam=x")
	if got.code != 2 {
		t.Errorf("exit = %d, want 2\nstderr:\n%s", got.code, got.stderr)
	}
	if strings.Contains(got.stdout, "SHOW") {
		t.Errorf("an unrecognized policy value enabled correction\nstdout:\n%s", got.stdout)
	}
}

// --- the side-effect gate (mutation target a) ----------------------------

// TestAutocorrect_ReadMode_NeverCorrectsDestructive is non-negotiable 2,
// and the assertion that goes RED when the side-effect check is removed
// from autocorrectAllowed. `--forc` is one edit from `--force` on a leaf
// annotated destructive: every other filter passes, so only the gate
// stands between the typo and a deletion.
func TestAutocorrect_ReadMode_NeverCorrectsDestructive(t *testing.T) {
	got := run(t, "--autocorrect=read", "delete", "--forc")
	if got.code == 0 {
		t.Fatalf("a destructive leaf auto-ran on a corrected flag\nstdout:\n%s", got.stdout)
	}
	if strings.Contains(got.stdout, "DELETED") {
		t.Fatalf("the side-effect gate did not hold; the deletion ran\nstdout:\n%s", got.stdout)
	}
	// The correction must not be APPLIED, which is a stronger claim than
	// "the deletion did not run". With the gate removed the rewrite goes
	// through and the confirm gate catches it instead — non-zero, nothing
	// deleted, and this assertion the only thing that notices. Defense in
	// depth is welcome; a test that cannot see past it is not.
	if strings.Contains(got.stderr, "Corrected from") {
		t.Fatalf("the side-effect gate did not hold; a destructive leaf had its "+
			"flag rewritten and only the confirm gate stopped the run\nstderr:\n%s",
			got.stderr)
	}
	if !strings.Contains(got.stderr, "--force") {
		t.Errorf("the suggestion was not even offered:\n%s", got.stderr)
	}
}

// TestAutocorrect_ReadMode_NeverCorrectsWrite: the gate is "read", not
// "not destructive". A write leaf is refused in read mode too.
func TestAutocorrect_ReadMode_NeverCorrectsWrite(t *testing.T) {
	got := run(t, "--autocorrect=read", "update", "--forc")
	if got.code == 0 {
		t.Fatalf("a write leaf auto-ran on a corrected flag\nstdout:\n%s", got.stdout)
	}
	if strings.Contains(got.stdout, "UPDATED") {
		t.Fatalf("read mode corrected a write leaf\nstdout:\n%s", got.stdout)
	}
	if strings.Contains(got.stderr, "Corrected from") {
		t.Fatalf("a write leaf had its flag rewritten in read mode\nstderr:\n%s", got.stderr)
	}
}

// --- the candidate filter (mutation target b) ----------------------------

// TestAutocorrect_AmbiguousNeverApplies is non-negotiable 3's "exactly
// one match" half, and the assertion that goes RED when the distance
// filter is loosened enough to let a tie resolve. `--stace` is one edit
// from both --stage and --stale.
func TestAutocorrect_AmbiguousNeverApplies(t *testing.T) {
	got := run(t, "--autocorrect=read", "ambig", "--stace")
	if got.code != 2 {
		t.Fatalf("exit = %d, want 2; an ambiguous typo was auto-picked\nstdout:\n%s\nstderr:\n%s",
			got.code, got.stdout, got.stderr)
	}
	if strings.Contains(got.stdout, "AMBIG RAN") {
		t.Fatalf("kit picked a winner between two equidistant flags\nstdout:\n%s", got.stdout)
	}
	// Both candidates are still offered — refusing to guess is not
	// refusing to help.
	for _, want := range []string{"--stage", "--stale"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr omitted candidate %q:\n%s", want, got.stderr)
		}
	}
}

// twoEditTypo is a transposition of "name" at Levenshtein distance 2.
// Assembled from fragments because it is deliberate test input, not prose,
// and the spell checker cannot tell those apart.
var twoEditTypo = "--nm" + "ae"

// TestAutocorrect_TwoEditsNeverApplies is the tighter-than-suggestion
// half, and the second assertion that goes RED when the distance filter
// is loosened. A transposition two edits from the real flag is named as a
// Fix by the suggester while the corrector refuses to apply it. That
// divergence IS the contract: distance <=2 suggests, distance <=1
// rewrites.
func TestAutocorrect_TwoEditsNeverApplies(t *testing.T) {
	got := run(t, "--autocorrect=read", "show", twoEditTypo+"=x")
	if got.code != 2 {
		t.Fatalf("exit = %d, want 2; a two-edit typo was applied\nstdout:\n%s", got.code, got.stdout)
	}
	if strings.Contains(got.stdout, "SHOW") {
		t.Fatalf("a two-edit typo was rewritten\nstdout:\n%s", got.stdout)
	}
	// Still suggested: the suggestion threshold is unchanged.
	if !strings.Contains(got.stderr, "--name") {
		t.Errorf("the two-edit candidate stopped being suggested:\n%s", got.stderr)
	}
}

// TestAutocorrect_BelowLengthFloorNeverApplies keeps the existing
// minimum-length refusal binding on the corrector as well as the
// suggester.
func TestAutocorrect_BelowLengthFloorNeverApplies(t *testing.T) {
	got := run(t, "--autocorrect=read", "show", "--na")
	if got.code != 2 {
		t.Fatalf("exit = %d, want 2; a sub-floor name was corrected\nstdout:\n%s", got.code, got.stdout)
	}
	if strings.Contains(got.stdout, "SHOW") {
		t.Fatalf("a two-character name was rewritten\nstdout:\n%s", got.stdout)
	}
}

// TestAutocorrect_PrefixApplies: an abbreviation is the strongest signal
// available and is applied even past the one-edit ceiling. `--verbose-r`
// is three edits from `--verbose-rows` but an unambiguous prefix of it.
//
// Note the prefix must be unique across the WHOLE candidate set, globals
// included: a bare `--verb` also prefixes the root's own `--verbose`, and
// two candidates is no correction.
func TestAutocorrect_PrefixApplies(t *testing.T) {
	got := run(t, "--autocorrect=read", "list", "--verbose-r")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0; a unique prefix was refused\nstderr:\n%s", got.code, got.stderr)
	}
}

// TestAutocorrect_AmbiguousPrefixNeverApplies is the same rule's refusal
// side: `--verb` prefixes both the leaf's --verbose-rows and the root's
// --verbose, so there is no single answer to apply.
func TestAutocorrect_AmbiguousPrefixNeverApplies(t *testing.T) {
	got := run(t, "--autocorrect=read", "list", "--verb")
	if got.code != 2 {
		t.Fatalf("exit = %d, want 2; an ambiguous prefix was applied\nstdout:\n%s",
			got.code, got.stdout)
	}
}

// --- the envelope (mutation target c) ------------------------------------

// TestAutocorrect_JSONEnvelopeCarriesCorrectedFrom is non-negotiable 4,
// and the assertion that goes RED when corrected_from is dropped. A
// --format json caller has no other way to learn the command it ran is
// not the command it asked for.
func TestAutocorrect_JSONEnvelopeCarriesCorrectedFrom(t *testing.T) {
	got := run(t, "--format", "json", "--autocorrect=read", "show", "--nam=x")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	var env struct {
		Code          string `json:"code"`
		CorrectedFrom string `json:"corrected_from"`
		ExitCode      int    `json:"exit_code"`
	}
	// One document, not two: the suggest-only envelope must not also be
	// on stderr when the correction is applied.
	if err := json.Unmarshal([]byte(strings.TrimSpace(got.stderr)), &env); err != nil {
		t.Fatalf("stderr is not one JSON document (%v):\n%s", err, got.stderr)
	}
	if env.CorrectedFrom != "--nam" {
		t.Errorf("corrected_from = %q, want %q\nstderr:\n%s",
			env.CorrectedFrom, "--nam", got.stderr)
	}
	if env.Code != "OK" || env.ExitCode != 0 {
		t.Errorf("a successful corrected run reported %s/%d", env.Code, env.ExitCode)
	}
}

// TestAutocorrect_YAMLEnvelopeCarriesCorrectedFrom: same obligation in
// the other structured format.
func TestAutocorrect_YAMLEnvelopeCarriesCorrectedFrom(t *testing.T) {
	got := run(t, "--format", "yaml", "--autocorrect=read", "show", "--nam=x")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "corrected_from: --nam") {
		t.Errorf("yaml envelope lost corrected_from:\n%s", got.stderr)
	}
}

// TestAutocorrect_HumanNoteOnStderr: the one-line note for a human, in
// the default format. Never stderr-only, but never structured-only
// either.
func TestAutocorrect_HumanNoteOnStderr(t *testing.T) {
	got := run(t, "--autocorrect=read", "show", "--nam=x")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "Corrected from: --nam") {
		t.Errorf("no human-readable correction note:\n%s", got.stderr)
	}
	if strings.Contains(got.stdout, "Corrected from") {
		t.Errorf("the note leaked onto stdout, where the data lives:\n%s", got.stdout)
	}
}

// --- gates see the corrected argv ----------------------------------------

// TestAutocorrect_DryRunSeesCorrectedArgv is non-negotiable 5. The
// re-dispatch goes through the whole RunE chain, so --dry-run applies to
// the corrected invocation. A shortcut that ran the leaf directly would
// report dryrun=false here.
func TestAutocorrect_DryRunSeesCorrectedArgv(t *testing.T) {
	got := run(t, "--autocorrect=read", "--dry-run", "show", "--nam=x")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "dryrun=true") {
		t.Errorf("--dry-run did not reach the corrected run\nstdout:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "SHOW name=x") {
		t.Errorf("the corrected value did not reach the corrected run\nstdout:\n%s", got.stdout)
	}
}

// --- non-TTY fallback (mutation target d) --------------------------------

// TestAutocorrect_PromptMode_NonTTYFallsBackToSuggestOnly is
// non-negotiable 6's first half: no terminal, no prompt, no block. With
// stdin a pipe and no controlling terminal, prompt mode is
// indistinguishable from off.
func TestAutocorrect_PromptMode_NonTTYFallsBackToSuggestOnly(t *testing.T) {
	cmd := exec.Command(toolBin, "--autocorrect=prompt", "show", "--nam=x")
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Deliberately never written to and never closed until after the
	// wait deadline: a prompt that reads stdin would hang here, which is
	// the failure this test exists to catch.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exitErr *exec.ExitError
		code := 0
		if err != nil {
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			} else {
				t.Fatalf("wait: %v", err)
			}
		}
		if code != 2 {
			t.Errorf("exit = %d, want 2\nstderr:\n%s", code, stderr.String())
		}
		if strings.Contains(stdout.String(), "SHOW") {
			t.Errorf("prompt mode applied a correction with no terminal to ask on\nstdout:\n%s",
				stdout.String())
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("prompt mode BLOCKED with stdin a pipe and no tty; it must degrade to suggest-only")
	}
	_ = stdin.Close()
}

// TestAutocorrect_PromptMode_StdinClosedFallsBack is the same guarantee
// with stdin closed outright rather than an unwritten pipe.
func TestAutocorrect_PromptMode_StdinClosedFallsBack(t *testing.T) {
	cmd := exec.Command(toolBin, "--autocorrect=prompt", "show", "--nam=x")
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	cmd.Stdin = nil // no stdin at all
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run: %v", err)
		}
	}
	if code != 2 {
		t.Errorf("exit = %d, want 2\nstderr:\n%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "SHOW") {
		t.Errorf("prompt mode applied a correction with stdin closed\nstdout:\n%s", stdout.String())
	}
}

// TestAutocorrect_PromptMode_NeverConsumesPipedStdin is non-negotiable
// 6's second half, and the assertion that goes RED when the prompt reads
// stdin instead of /dev/tty.
//
// The setup is the one that makes the bug observable and is otherwise
// hard to reach: the process gets a REAL controlling terminal (so the
// prompt fires at all) while its stdin is a separate pipe carrying data
// the command must receive intact. A prompt reading stdin would eat
// LINE1 as its answer; a prompt reading /dev/tty leaves both lines for
// the leaf.
func TestAutocorrect_PromptMode_NeverConsumesPipedStdin(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	cmd := exec.Command(toolBin, "--autocorrect=prompt", "ingest", "--stric")
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	// The pty is the controlling terminal (and so /dev/tty inside the
	// child), but NOT stdin.
	cmd.Stdout = tty
	cmd.Stderr = tty
	setControllingTTY(cmd, tty)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		_ = tty.Close()
		t.Fatalf("start: %v", err)
	}
	_ = tty.Close() // the child owns it now

	go func() {
		_, _ = stdinPipe.Write([]byte("LINE1\nLINE2\n"))
		_ = stdinPipe.Close()
	}()

	// Answer the prompt on the terminal, which is where it must be asked.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_, _ = ptmx.Write([]byte("y\n"))
	}()

	// Drain the pty concurrently so the child never blocks on a full
	// buffer while we are waiting on it.
	outCh := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		outCh <- b.String()
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("prompt mode hung with a tty present and stdin piped")
	}
	_ = ptmx.Close()

	var out string
	select {
	case out = <-outCh:
	case <-time.After(5 * time.Second):
		t.Fatal("pty drain did not finish")
	}

	if !strings.Contains(out, "Did you mean --strict") {
		t.Fatalf("the prompt was never asked on the terminal\npty:\n%s", out)
	}
	// The payload must be intact. Losing LINE1 is exactly what a prompt
	// reading stdin does.
	if !strings.Contains(out, `INGESTED "LINE1\nLINE2\n"`) {
		t.Errorf("stdin was consumed by the prompt; payload is not intact\npty:\n%s", out)
	}
}

// TestAutocorrect_PromptMode_DeclineExitsTwoWithEnvelope is
// non-negotiable 7. A declined prompt is lossless: the same USAGE
// envelope a suggest-only run emits, and the same exit code.
func TestAutocorrect_PromptMode_DeclineExitsTwoWithEnvelope(t *testing.T) {
	out, code := runOnPTY(t, []string{"--format", "json", "--autocorrect=prompt", "show", "--nam=x"}, "n\n")
	if code != 2 {
		t.Fatalf("declining exited %d, want 2\npty:\n%s", code, out)
	}
	if strings.Contains(out, "SHOW name=") {
		t.Fatalf("a declined correction still ran the leaf\npty:\n%s", out)
	}
	// The envelope is the suggest-only one, structured and complete.
	jsonPart := out[strings.Index(out, "{"):]
	jsonPart = jsonPart[:strings.LastIndex(jsonPart, "}")+1]
	var env struct {
		Code          string `json:"code"`
		SuggestedFix  string `json:"suggested_fix"`
		CorrectedFrom string `json:"corrected_from"`
		ExitCode      int    `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(jsonPart), &env); err != nil {
		t.Fatalf("declined run's stderr is not a JSON envelope (%v)\npty:\n%s", err, out)
	}
	if env.Code != "USAGE" || env.ExitCode != 2 {
		t.Errorf("declined envelope = %s/%d, want USAGE/2", env.Code, env.ExitCode)
	}
	if env.SuggestedFix != "--name" {
		t.Errorf("declined envelope lost the suggestion: %q", env.SuggestedFix)
	}
	if env.CorrectedFrom != "" {
		t.Errorf("a declined run reported corrected_from = %q", env.CorrectedFrom)
	}
}

// TestAutocorrect_PromptMode_AcceptRuns is the prompt's happy path.
func TestAutocorrect_PromptMode_AcceptRuns(t *testing.T) {
	out, code := runOnPTY(t, []string{"--autocorrect=prompt", "show", "--nam=x"}, "y\n")
	if code != 0 {
		t.Fatalf("accepting exited %d, want 0\npty:\n%s", code, out)
	}
	if !strings.Contains(out, "SHOW name=x") {
		t.Errorf("an accepted correction did not run\npty:\n%s", out)
	}
}

// TestAutocorrect_PromptMode_EnterAcceptsOnRead is the side-effect-chosen
// default: a bare Enter on a read-only leaf accepts.
func TestAutocorrect_PromptMode_EnterAcceptsOnRead(t *testing.T) {
	out, code := runOnPTY(t, []string{"--autocorrect=prompt", "show", "--nam=x"}, "\n")
	if code != 0 {
		t.Fatalf("Enter on a read-only leaf exited %d, want 0\npty:\n%s", code, out)
	}
	if !strings.Contains(out, "[Y/n]") {
		t.Errorf("a read-only leaf did not advertise the yes default\npty:\n%s", out)
	}
	if !strings.Contains(out, "SHOW name=x") {
		t.Errorf("Enter did not accept on a read-only leaf\npty:\n%s", out)
	}
}

// TestAutocorrect_PromptMode_EnterDeclinesOnDestructive is the other half
// of the same rule: on a destructive verb the default is No, so only an
// explicit y proceeds.
func TestAutocorrect_PromptMode_EnterDeclinesOnDestructive(t *testing.T) {
	out, code := runOnPTY(t, []string{"--autocorrect=prompt", "delete", "--forc"}, "\n")
	if code == 0 {
		t.Fatalf("Enter on a destructive verb exited 0\npty:\n%s", out)
	}
	if strings.Contains(out, "DELETED") {
		t.Fatalf("Enter accepted a correction on a destructive verb\npty:\n%s", out)
	}
	if !strings.Contains(out, "[y/N]") {
		t.Errorf("a destructive leaf did not advertise the no default\npty:\n%s", out)
	}
}

// runOnPTY runs the tool with a real controlling terminal, writes answer
// to it, and returns everything the terminal saw plus the exit code.
//
// stdin is the pty as well here, which is the ordinary interactive shape;
// TestAutocorrect_PromptMode_NeverConsumesPipedStdin is the one case that
// deliberately separates the two.
func runOnPTY(t *testing.T, args []string, answer string) (string, int) {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	cmd := exec.Command(toolBin, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	setControllingTTY(cmd, tty)

	if err := cmd.Start(); err != nil {
		_ = tty.Close()
		t.Fatalf("start: %v", err)
	}
	_ = tty.Close()

	go func() {
		time.Sleep(300 * time.Millisecond)
		_, _ = ptmx.Write([]byte(answer))
	}()

	outCh := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		outCh <- b.String()
	}()

	code := 0
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case werr := <-done:
		var exitErr *exec.ExitError
		if werr != nil {
			if errors.As(werr, &exitErr) {
				code = exitErr.ExitCode()
			} else {
				t.Fatalf("wait: %v", werr)
			}
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("run %v hung on the pty", args)
	}
	_ = ptmx.Close()

	select {
	case out := <-outCh:
		return out, code
	case <-time.After(5 * time.Second):
		t.Fatal("pty drain did not finish")
	}
	return "", code
}

// --- two prompts on one terminal, with color detection live -------------

// TestAutocorrect_PromptThenConfirm_ColorDetectionKeepsTerminal is the
// regression for a hang between the autocorrect prompt and the confirm
// gate.
//
// Both prompts fire in one invocation: a mistyped flag on a destructive
// leaf asks "did you mean", and the corrected re-dispatch then reaches
// the destructive confirm gate. Between them, kit used to hand the
// first, DOOMED dispatch's error to fang's styled error handler. fang
// styles by querying the terminal background — OSC 11 plus DA1, with the
// terminal in RAW MODE, read twice at 2s each — and that read consumed
// the keystrokes the confirm prompt was about to ask for. The confirm
// prompt then blocked forever on a terminal whose answer had already
// been eaten.
//
// Every other test in this file sets NO_COLOR=1 and TERM=dumb, which is
// why none of them ever caught this: NO_COLOR does not reach fang's
// probe, but a dumb terminal changes what is worth asserting. This one
// deliberately runs with color ENABLED and a color-capable TERM, which
// is the ordinary interactive shell the report describes.
//
// The assertion is the second answer landing. A pty in canonical mode
// hands out one line per read, so a test that merely drives two prompts
// can pass against the defect; what cannot pass is the leaf running,
// because that requires the confirm prompt to have actually received the
// "y" the probe would have swallowed.
func TestAutocorrect_PromptThenConfirm_ColorDetectionKeepsTerminal(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	cmd := exec.Command(toolBin, "--autocorrect=prompt", "delete", "--forc")
	// Color ON. NO_COLOR and TERM are stripped rather than merely
	// omitted: the test runner's own environment may carry either, and
	// inheriting one would silence the very probe under test.
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "NO_COLOR=") || strings.HasPrefix(e, "TERM=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "TERM=xterm-256color")
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	setControllingTTY(cmd, tty)

	if err := cmd.Start(); err != nil {
		_ = tty.Close()
		t.Fatalf("start: %v", err)
	}
	_ = tty.Close()

	// Answer both prompts. The gap is longer than one probe (2s) but
	// shorter than the pair (4s), so a run that still probes has already
	// swallowed the second answer by the time the confirm prompt asks.
	go func() {
		time.Sleep(500 * time.Millisecond)
		_, _ = ptmx.Write([]byte("y\n"))
		time.Sleep(700 * time.Millisecond)
		_, _ = ptmx.Write([]byte("y\n"))
	}()

	outCh := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		outCh <- b.String()
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the confirm prompt never got its answer: background color detection read the terminal between the two prompts")
	}
	_ = ptmx.Close()

	var out string
	select {
	case out = <-outCh:
	case <-time.After(5 * time.Second):
		t.Fatal("pty drain did not finish")
	}

	if !strings.Contains(out, "Did you mean --force") {
		t.Fatalf("the autocorrect prompt never asked\npty:\n%s", out)
	}
	if !strings.Contains(out, "Continue?") {
		t.Fatalf("the confirm gate never asked\npty:\n%s", out)
	}
	// The leaf running is the proof the confirm prompt read its answer.
	// Nothing else in this transcript distinguishes "answered" from
	// "asked and then blocked".
	if !strings.Contains(out, "DELETED") {
		t.Errorf("the confirm prompt did not receive its answer\npty:\n%s", out)
	}
	// The probe's own bytes must not be on the terminal at all. This is
	// the direct assertion on the mechanism, independent of timing.
	if strings.Contains(out, "\x1b]11;?") {
		t.Errorf("a background-color query reached the terminal between the prompts\npty:\n%q", out)
	}
}

// TestAutocorrect_ReadMode_NoTerminalQuery is the same mechanism on the
// path that has no prompt to steal from, so it cannot hang and the cost
// is latency instead: read mode used to spend four seconds on two
// background queries before the corrected run produced any output.
//
// Asserted on the escape bytes rather than on a wall-clock budget, which
// would be flaky on a loaded runner.
func TestAutocorrect_ReadMode_NoTerminalQuery(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer func() { _ = ptmx.Close() }()

	cmd := exec.Command(toolBin, "--autocorrect=read", "show", "--nam=x")
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "NO_COLOR=") || strings.HasPrefix(e, "TERM=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "TERM=xterm-256color")
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	setControllingTTY(cmd, tty)

	if err := cmd.Start(); err != nil {
		_ = tty.Close()
		t.Fatalf("start: %v", err)
	}
	_ = tty.Close()

	outCh := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		outCh <- b.String()
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("read-mode correction hung on the pty")
	}
	_ = ptmx.Close()

	var out string
	select {
	case out = <-outCh:
	case <-time.After(5 * time.Second):
		t.Fatal("pty drain did not finish")
	}

	if !strings.Contains(out, "SHOW name=x") {
		t.Fatalf("the corrected run did not reach the leaf\npty:\n%s", out)
	}
	if strings.Contains(out, "\x1b]11;?") {
		t.Errorf("a background-color query reached the terminal on a corrected run\npty:\n%q", out)
	}
}
