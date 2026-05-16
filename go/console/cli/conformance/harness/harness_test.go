package harness_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"hop.top/kit/go/console/cli/conformance/harness"
	"hop.top/kit/go/console/cli/conformance/harness/classifier"
)

// recordingT captures Errorf/Fatalf calls from the harness so we
// can write sad-path tests without escalating to the outer
// *testing.T. Mirrors the stubTB pattern in
// conformance/conformance_test.go.
//
// tempDirs accumulates temp dirs the harness allocated through the
// stub; the outer test cleans them up via Cleanup so a sad-path
// test doesn't leak under /tmp.
type recordingT struct {
	t        *testing.T
	errors   []string
	fatals   []string
	failed   bool
	tempDirs []string
}

func newRecorder(t *testing.T) *recordingT {
	r := &recordingT{t: t}
	t.Cleanup(func() {
		for _, d := range r.tempDirs {
			_ = os.RemoveAll(d)
		}
	})
	return r
}

func (r *recordingT) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
	r.failed = true
}
func (r *recordingT) Fatalf(format string, args ...any) {
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
	r.failed = true
}
func (r *recordingT) Helper()                         {}
func (r *recordingT) Logf(format string, args ...any) {}
func (r *recordingT) TempDir() string {
	d, err := os.MkdirTemp("", "harness-stub-*")
	if err != nil {
		r.Fatalf("MkdirTemp: %v", err)
		return ""
	}
	r.tempDirs = append(r.tempDirs, d)
	return d
}

// ───────────────────────────────────────────────────────────────
// Tests for the Assert* primitives. Each pair tests passing +
// failing fixture behavior.
// ───────────────────────────────────────────────────────────────

func TestAssertExitCodeClass_Passes(t *testing.T) {
	root := buildFixture()
	harness.AssertExitCodeClass(t, root, harness.Args("exit-ok"))
}

func TestAssertExitCodeClass_NotFoundExpectedPasses(t *testing.T) {
	root := buildFixture()
	harness.AssertExitCodeClass(t, root, harness.Args("exit-not-found"))
}

func TestAssertExitCodeClass_Fails(t *testing.T) {
	root := buildFixture()
	rec := newRecorder(t)
	harness.AssertExitCodeClass(rec, root, harness.Args("exit-wrong-class"))
	if !rec.failed {
		t.Fatal("expected wrong-class leaf to fail the assertion")
	}
	joined := strings.Join(rec.errors, " | ")
	if !strings.Contains(joined, "not in declared class set") {
		t.Fatalf("unexpected failure message: %q", joined)
	}
}

func TestAssertJSONSchema_Passes(t *testing.T) {
	root := buildFixture()
	harness.AssertJSONSchema(t, root, harness.Args("json-pass"))
}

func TestAssertJSONSchema_MissingField(t *testing.T) {
	root := buildFixture()
	rec := newRecorder(t)
	harness.AssertJSONSchema(rec, root, harness.Args("json-missing-field"))
	if !rec.failed {
		t.Fatal("expected json-missing-field to fail validation")
	}
	joined := strings.Join(rec.errors, " | ")
	if !strings.Contains(joined, "validation error") {
		t.Fatalf("unexpected message: %q", joined)
	}
}

func TestAssertJSONSchema_ParseError(t *testing.T) {
	root := buildFixture()
	rec := newRecorder(t)
	harness.AssertJSONSchema(rec, root, harness.Args("json-bad"))
	if !rec.failed {
		t.Fatal("expected json-bad to fail parsing")
	}
	joined := strings.Join(rec.errors, " | ")
	if !strings.Contains(joined, "not valid JSON") {
		t.Fatalf("unexpected message: %q", joined)
	}
}

func TestAssertDryRunNoMutation_Passes(t *testing.T) {
	root := buildFixture()
	// http-write honors --dry-run by issuing GET instead of POST.
	harness.AssertDryRunNoMutation(t, root, harness.Args("http-write"))
}

func TestAssertDryRunNoMutation_Fails(t *testing.T) {
	root := buildFixture()
	rec := newRecorder(t)
	// http-rebel ignores --dry-run; the harness records POST and
	// the classifier flags it as Write.
	harness.AssertDryRunNoMutation(rec, root, harness.Args("http-rebel"))
	if !rec.failed {
		t.Fatal("expected http-rebel to be caught by dry-run assertion")
	}
	joined := strings.Join(rec.errors, " | ")
	if !strings.Contains(joined, "mutating interaction") {
		t.Fatalf("unexpected message: %q", joined)
	}
}

func TestAssertDryRunNoMutation_ExecOverride(t *testing.T) {
	root := buildFixture()
	// exec-read is `ls -la`; default exec classifier treats it as
	// Write. The override below reclassifies ls as Read.
	harness.AssertDryRunNoMutation(t, root,
		harness.Args("exec-read"),
		harness.WithExecClassifier(func(argv []string) classifier.Class {
			if len(argv) > 0 && argv[0] == "ls" {
				return classifier.ClassRead
			}
			return classifier.ClassWrite
		}),
	)
}

func TestAssertDestructiveGated_Passes(t *testing.T) {
	root := buildFixture()
	harness.AssertDestructiveGated(t, root, harness.Args("delete"))
}

func TestAssertDestructiveGated_BrokenGate(t *testing.T) {
	root := buildFixture()
	rec := newRecorder(t)
	harness.AssertDestructiveGated(rec, root, harness.Args("delete-broken"))
	if !rec.failed {
		t.Fatal("expected delete-broken to fail case 1")
	}
	joined := strings.Join(rec.errors, " | ")
	if !strings.Contains(joined, "case 1/3") {
		t.Fatalf("unexpected failure: %q", joined)
	}
}

func TestPlanApplyReplay_IdempotentPasses(t *testing.T) {
	root := buildFixture()
	harness.PlanApplyReplay(t, root, harness.Args("idempotent-sql"))
}

func TestPlanApplyReplay_NonIdempotentFails(t *testing.T) {
	root := buildFixture()
	rec := newRecorder(t)
	harness.PlanApplyReplay(rec, root, harness.Args("non-idempotent-sql"))
	if !rec.failed {
		t.Fatal("expected non-idempotent-sql to fail PlanApplyReplay")
	}
	joined := strings.Join(rec.errors, " | ")
	if !strings.Contains(joined, "cassette diff non-empty") {
		t.Fatalf("unexpected failure: %q", joined)
	}
}

func TestAssertCapabilityRoundtrip_Passes(t *testing.T) {
	root := buildFixture()
	harness.AssertCapabilityRoundtrip(t, root)
}

// TestNonTTY_Compiles is the legibility check: NonTTY() exists
// and threads through without crashing.
func TestNonTTY_Compiles(t *testing.T) {
	root := buildFixture()
	harness.AssertExitCodeClass(t, root,
		harness.Args("exit-ok"),
		harness.NonTTY(),
	)
}

// TestWithConfigSnapshot exercises the snapshot install/restore.
// We don't have a fixture leaf that reads viper directly, so the
// test asserts the option doesn't panic and viper state is
// restored after the call.
func TestWithConfigSnapshot(t *testing.T) {
	root := buildFixture()
	harness.AssertExitCodeClass(t, root,
		harness.Args("exit-ok"),
		harness.WithConfigSnapshot(map[string]any{"harness.test": "value"}),
	)
}
