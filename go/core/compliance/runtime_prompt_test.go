package compliance

// Tests for rtConsentingTelemetryPrompt — sub-conditions (e) + (f)
// honored at runtime per the precedence chain.
//
// Build strategy: the kill-switch tests own the package-wide TestMain
// (it builds testdata/stub-telemetry-binary once for the whole run),
// so this file CANNOT define another TestMain. Instead the prompt
// stub is compiled lazily on first use via sync.Once — same one-time
// cost, scoped to the prompt tests, no per-test rebuild overhead.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// promptStubBinaryPath holds the absolute path to the freshly-built
// stub-telemetry-binary-prompt. Populated lazily by buildPromptStub
// on first use; reused across all prompt-runtime tests in the
// package.
var (
	promptStubBinaryPath string
	promptStubBuildOnce  sync.Once
	promptStubBuildErr   error
)

// buildPromptStub compiles testdata/stub-telemetry-binary-prompt
// into a tmpdir and stores the absolute path in
// promptStubBinaryPath. Idempotent via sync.Once — concurrent
// callers share one build. Mirrors TestMain in
// runtime_killswitch_test.go (same -buildvcs=false for tlc
// bare-worktree compatibility) but scoped to the prompt tests.
func buildPromptStub(t *testing.T) string {
	t.Helper()
	promptStubBuildOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "compliance-stubs-prompt-*")
		if err != nil {
			promptStubBuildErr = fmt.Errorf("mkdir tmpdir: %w", err)
			return
		}
		// We deliberately do NOT defer os.RemoveAll(tmpDir) here —
		// the binary needs to outlive the build call and survive
		// across all tests in the run. TestMain in the sibling file
		// is the package-level lifecycle hook; binding cleanup to
		// it would require coupling we'd rather avoid. The tmpdir
		// is small (a few MB) and gets cleaned by OS-level temp
		// reaper, so leaving it is acceptable for test fixtures.
		path := filepath.Join(tmpDir, "stub-telemetry-binary-prompt")
		cmd := exec.Command("go", "build", "-buildvcs=false", "-o", path,
			"./testdata/stub-telemetry-binary-prompt")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			promptStubBuildErr = fmt.Errorf("build stub: %w", err)
			return
		}
		promptStubBinaryPath = path
	})
	if promptStubBuildErr != nil {
		t.Fatalf("buildPromptStub: %v", promptStubBuildErr)
	}
	return promptStubBinaryPath
}

// promptSpec returns a minimal opt-in toolspec the prompt runtime
// check will accept: one read command (idempotent + output_schema)
// and a telemetry block. Mirrors killSwitchSpec from the sibling
// test file; kept separate so changes here stay isolated.
func promptSpec() *toolspecYAML {
	idempotent := true
	return &toolspecYAML{
		Name:          "stub",
		SchemaVersion: "1",
		Commands: []commandYAML{
			{
				Name: "status",
				Contract: &contractYAML{
					Idempotent: &idempotent,
				},
				OutputSchema: &outSchemaYAML{Format: "json"},
			},
		},
		Telemetry: &telemetryYAML{
			Enabled:        true,
			PromptVersion:  "v1",
			KillSwitchEnvs: []string{"DO_NOT_TRACK", "KIT_TELEMETRY_MODE"},
		},
	}
}

// runPromptWith invokes rtConsentingTelemetryPrompt with STUB_PROMPT
// pre-set via t.Setenv (newRTEnv does not strip STUB_PROMPT, only
// HOME/XDG/KIT_BUS_SINK*). Same pattern as runKillSwitchWith.
func runPromptWith(t *testing.T, bin string, spec *toolspecYAML, stubMode string) CheckResult {
	t.Helper()
	t.Setenv("STUB_PROMPT", stubMode)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return rtConsentingTelemetryPrompt(ctx, bin, spec, func() *rtEnv { return newRTEnv(t) })
}

func TestRtPrompt_SkipsWhenNotOptedIn(t *testing.T) {
	// Spec with Telemetry == nil — same gate as the static check and
	// the sibling kill-switch runtime check.
	idempotent := true
	spec := &toolspecYAML{
		Name:          "stub",
		SchemaVersion: "1",
		Commands: []commandYAML{
			{
				Name:         "status",
				Contract:     &contractYAML{Idempotent: &idempotent},
				OutputSchema: &outSchemaYAML{Format: "json"},
			},
		},
		Telemetry: nil,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use stubBinaryPath from the sibling test file — TestMain
	// already built it. We don't need our prompt stub for the
	// skip path.
	got := rtConsentingTelemetryPrompt(ctx, stubBinaryPath, spec, func() *rtEnv { return newRTEnv(t) })
	if got.Status != "skip" {
		t.Fatalf("status = %q, want skip; details=%q", got.Status, got.Details)
	}
	if got.Factor != FactorConsentingTelemetry {
		t.Fatalf("factor = %v, want FactorConsentingTelemetry", got.Factor)
	}
}

func TestRtPrompt_DoNotTrackPersistsDenied(t *testing.T) {
	// Scenario 1 from the plan: DO_NOT_TRACK=1 → state=denied,
	// decision_source=env. The full check exercises ALL three
	// scenarios in sequence and only passes when all three persist
	// correctly, so a pass here implicitly proves scenario 1 worked.
	bin := buildPromptStub(t)
	spec := promptSpec()
	got := runPromptWith(t, bin, spec, "normal")
	if got.Status != "pass" {
		t.Fatalf("status = %q, want pass; details=%q", got.Status, got.Details)
	}
}

func TestRtPrompt_NonTTYPersistsDenied(t *testing.T) {
	// Scenario 2 from the plan: fresh HOME, no env, non-TTY (always,
	// per exec.Command) → state=denied, decision_source=config.
	// Same end-to-end pass as scenario 1; the check covers both in a
	// single run. Kept as a separate test name for per-scenario
	// coverage clarity.
	bin := buildPromptStub(t)
	spec := promptSpec()
	got := runPromptWith(t, bin, spec, "normal")
	if got.Status != "pass" {
		t.Fatalf("status = %q, want pass; details=%q", got.Status, got.Details)
	}
}

func TestRtPrompt_EnvBeatsPersistedGranted(t *testing.T) {
	// Scenario 3 from the plan: pre-seeded granted + DO_NOT_TRACK=1
	// → no events emitted. The check passes only when all three
	// scenarios succeed, so a pass here proves scenario 3 worked.
	bin := buildPromptStub(t)
	spec := promptSpec()
	got := runPromptWith(t, bin, spec, "normal")
	if got.Status != "pass" {
		t.Fatalf("status = %q, want pass; details=%q", got.Status, got.Details)
	}
	// Belt-and-braces: the pass message names env-beats-persisted so
	// we can assert all three scenarios were exercised.
	if !strings.Contains(got.Details, "env-beats-persisted") {
		t.Fatalf("details = %q, want it to mention env-beats-persisted", got.Details)
	}
}

func TestRtPrompt_PromptVersionFieldNameLocked(t *testing.T) {
	// Sub-condition (f) field-name lock: persisted file MUST use
	// `prompt_version:` (NOT `consent_version:` or any other alias).
	// The normal stub stamps prompt_version correctly; this test
	// verifies the full check passes AND inspects the on-disk file
	// to confirm the literal key is present.
	bin := buildPromptStub(t)
	spec := promptSpec()
	got := runPromptWith(t, bin, spec, "normal")
	if got.Status != "pass" {
		t.Fatalf("status = %q, want pass; details=%q", got.Status, got.Details)
	}

	// Drive a single scenario directly to inspect the YAML — the
	// full check creates its rtEnvs internally and tears them down
	// before we can read the persisted file. Mirrors the
	// promptAssertPersisted flow but stops short of asserting.
	e := newRTEnv(t)
	e.SetEnv("STUB_PROMPT", "normal")
	e.SetEnv("DO_NOT_TRACK", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, _ = e.Run(ctx, bin, "status", "--format", "json")

	path := filepath.Join(e.XDGConfig, "kit", "telemetry.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted consent: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "prompt_version:") {
		t.Fatalf("persisted file missing `prompt_version:` key (field-name lock)\n---\n%s\n---", body)
	}
	// Negative assertion: no aliases. `consent_version` is explicitly
	// rejected.
	if strings.Contains(body, "consent_version:") {
		t.Fatalf("persisted file contains rejected `consent_version:` alias\n---\n%s\n---", body)
	}
}

func TestRtPrompt_FailsWhenDecisionSourceWrong(t *testing.T) {
	// STUB_PROMPT=bad-source — stub stamps decision_source="invalid"
	// (not in {env, flag, prompt, config}). The check must catch
	// this and surface the bad value in Details.
	bin := buildPromptStub(t)
	spec := promptSpec()
	got := runPromptWith(t, bin, spec, "bad-source")
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail; details=%q", got.Status, got.Details)
	}
	if !strings.Contains(got.Details, "invalid") {
		t.Fatalf("details = %q, want it to name the bad decision_source value (\"invalid\")", got.Details)
	}
	if got.Suggestion == "" {
		t.Fatalf("expected non-empty Suggestion for fail; got empty")
	}
}

func TestRtPrompt_FailsWhenPromptVersionMissing(t *testing.T) {
	// STUB_PROMPT=no-version — stub omits the `prompt_version:` line
	// entirely. The check must catch the missing canonical field
	// (sub-condition f field-name lock).
	bin := buildPromptStub(t)
	spec := promptSpec()
	got := runPromptWith(t, bin, spec, "no-version")
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail; details=%q", got.Status, got.Details)
	}
	if !strings.Contains(got.Details, "prompt_version") {
		t.Fatalf("details = %q, want it to mention the missing prompt_version field", got.Details)
	}
	if got.Suggestion == "" {
		t.Fatalf("expected non-empty Suggestion for fail; got empty")
	}
}
