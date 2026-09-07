// Package flagerrexit asserts flag-error exit codes on a real built
// binary.
//
// It lives in its own package for two reasons. Exit codes cannot be
// observed from inside the test process — `go run` masks them and
// Root.Execute reaches os.Exit on some paths — so a binary is mandatory.
// And go/console/cli's own package already owns a TestMain (the
// cross-language parity harness) that requires pnpm, so a fixture build
// cannot be added there without inheriting that dependency.
package flagerrexit_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var toolBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "flagerrexit-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "flagerrexit: MkdirTemp:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	toolBin = filepath.Join(dir, "flagerrtool")
	if runtime.GOOS == "windows" {
		toolBin += ".exe"
	}
	build := exec.Command("go", "build", "-buildvcs=false",
		"-o", toolBin, "./testdata/flagerrtool")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "flagerrexit: build failed: %v\n%s", err, out)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	stdout, stderr string
	code           int
}

func run(t *testing.T, args ...string) result {
	t.Helper()
	cmd := exec.Command(toolBin, args...)
	// NO_COLOR keeps stderr assertable; the fixture writes through fang.
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
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

func TestUnknownFlag_ExitsUsageWithFix(t *testing.T) {
	got := run(t, "list", "--count")
	if got.code != 2 {
		t.Errorf("exit = %d, want 2\nstderr:\n%s", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "Fix: --counters") {
		t.Errorf("stderr lost the correction:\n%s", got.stderr)
	}
}

func TestMissingValue_ExitsUsageWithEnum(t *testing.T) {
	got := run(t, "list", "--status")
	if got.code != 2 {
		t.Errorf("exit = %d, want 2\nstderr:\n%s", got.code, got.stderr)
	}
	for _, v := range []string{"TODO", "IN_PROGRESS", "DONE", "SKIPPED"} {
		if !strings.Contains(got.stderr, "--status "+v) {
			t.Errorf("stderr omitted %q:\n%s", v, got.stderr)
		}
	}
}

func TestFlagError_JSONEnvelopeCarriesFix(t *testing.T) {
	got := run(t, "--format", "json", "list", "--status")
	if got.code != 2 {
		t.Errorf("exit = %d, want 2\nstderr:\n%s", got.code, got.stderr)
	}
	var env struct {
		Code         string   `json:"code"`
		SuggestedFix string   `json:"suggested_fix"`
		Alternatives []string `json:"alternatives"`
		ExitCode     int      `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(got.stderr)), &env); err != nil {
		t.Fatalf("stderr is not one JSON document (%v):\n%s", err, got.stderr)
	}
	if env.Code != "USAGE" || env.ExitCode != 2 {
		t.Errorf("envelope = %+v", env)
	}
	if env.SuggestedFix == "" {
		t.Error("envelope carries no suggested_fix")
	}
	if len(env.Alternatives) != 4 {
		t.Errorf("alternatives = %v", env.Alternatives)
	}
}

// TestUsageError_RenderedExactlyOnce covers the failure that reaches
// stderr through the classifier rather than the parse-time enricher.
//
// An invalid flag VALUE is deliberately not enriched — kit has nothing to
// add to strconv's diagnosis — so it falls through to the usage
// classifier, which renders it itself. fang then calls its error handler
// regardless of cobra's SilenceErrors, so an unmarked return from that
// classifier prints the same envelope a second time. Plaintext, unlike
// the JSON envelope, doubles without any parse error to give it away,
// which is why it needs its own assertion.
func TestUsageError_RenderedExactlyOnce(t *testing.T) {
	got := run(t, "list", "--limit=abc")
	if got.code != 2 {
		t.Errorf("exit = %d, want 2\nstderr:\n%s", got.code, got.stderr)
	}
	if n := strings.Count(got.stderr, "USAGE: "); n != 1 {
		t.Errorf("envelope rendered %d times, want exactly 1:\n%s", n, got.stderr)
	}
	if n := strings.Count(got.stderr, "Fix: "); n != 1 {
		t.Errorf("fix rendered %d times, want exactly 1:\n%s", n, got.stderr)
	}
}

// TestUsageError_JSONRenderedOnce is the same guarantee in the structured
// format: two envelopes on stderr is not parseable as one document.
func TestUsageError_JSONRenderedOnce(t *testing.T) {
	got := run(t, "--format", "json", "list", "--limit=abc")
	if got.code != 2 {
		t.Errorf("exit = %d, want 2\nstderr:\n%s", got.code, got.stderr)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(got.stderr)), &env); err != nil {
		t.Fatalf("stderr is not one JSON document (%v):\n%s", err, got.stderr)
	}
	if env["code"] != "USAGE" {
		t.Errorf("code = %v, want USAGE", env["code"])
	}
}

// TestDestructive_MistypedFlagNeverAutoRuns is the hard constraint: a
// fuzzy match is a suggestion, never an action. If kit ever applied the
// correction itself, this deletes.
func TestDestructive_MistypedFlagNeverAutoRuns(t *testing.T) {
	got := run(t, "delete", "--forc")
	if got.code == 0 {
		t.Fatalf("mistyped flag on a destructive verb exited 0\nstdout:\n%s", got.stdout)
	}
	if strings.Contains(got.stdout, "DELETED") {
		t.Fatalf("kit auto-applied the guessed flag and ran the deletion\nstdout:\n%s", got.stdout)
	}
	if !strings.Contains(got.stderr, "--force") {
		t.Errorf("the correction was not even offered:\n%s", got.stderr)
	}
}

func TestNoFlagError_ExitsZero(t *testing.T) {
	got := run(t, "list", "--status", "DONE")
	if got.code != 0 {
		t.Errorf("exit = %d, want 0\nstderr:\n%s", got.code, got.stderr)
	}
}

func TestHelp_CarriesEnum(t *testing.T) {
	got := run(t, "list", "--help")
	if got.code != 0 {
		t.Errorf("exit = %d, want 0", got.code)
	}
	if !strings.Contains(got.stdout, "one of: TODO, IN_PROGRESS, DONE, SKIPPED") {
		t.Errorf("help lost the enum:\n%s", got.stdout)
	}
}
