// status_test.go covers `kit telemetry status` end-to-end at the
// runStatus seam. Each test points XDG_CONFIG_HOME / XDG_STATE_HOME
// at a t.TempDir() so the consent file and installation_id live in
// isolated state, then drives runStatus directly (bypassing cobra)
// and asserts on the rendered bytes.
//
// The Mode tests use the exported telemetry.SetMode helper rather
// than the unexported resetForTest so we stay on the public API
// surface; the trade-off is that once SetMode runs the env-var read
// is bypassed for the rest of the process, which is exactly what we
// want for a deterministic test.

package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/consent"
	runtimetel "hop.top/kit/go/runtime/telemetry"
)

// withFreshXDG points both XDG_CONFIG_HOME and XDG_STATE_HOME at a
// fresh t.TempDir for the duration of the test. Mirrors the pattern
// in hop.top/kit/go/runtime/telemetry/installid_test.go::withFreshXDG
// and hop.top/kit/go/core/consent/consent_test.go::newTestStore so
// status tests share the same isolation contract as their underlying
// data sources.
func withFreshXDG(t *testing.T) (configHome, stateHome string) {
	t.Helper()
	cfg := t.TempDir()
	st := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_STATE_HOME", st)
	// Defense-in-depth: clear adjacent vars that adrg/xdg may consult
	// for fallback resolution on some platforms.
	t.Setenv("XDG_DATA_HOME", filepath.Join(st, "_data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(st, "_cache"))
	return cfg, st
}

// seedConsent writes a Decision through the canonical FileStore so the
// status read goes through the same path as a real `kit telemetry
// enable`. Returns the file path for tests that want to assert on
// permissions or on-disk shape.
func seedConsent(t *testing.T, d consent.Decision) string {
	t.Helper()
	s, err := consent.NewFileStore()
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := s.Set(context.Background(), d); err != nil {
		t.Fatalf("Set: %v", err)
	}
	return s.Path()
}

// TestStatus_Default_HumanRender exercises the no-state path: empty
// XDG dirs, no consent file yet, default Mode. The human render
// should still produce all three sections and report State as
// "unknown" with the source elided to "(none)".
func TestStatus_Default_HumanRender(t *testing.T) {
	withFreshXDG(t)

	var stdout, stderr bytes.Buffer
	if err := runStatus(context.Background(), &stdout, &stderr, output.Table); err != nil {
		t.Fatalf("runStatus: %v", err)
	}

	got := stdout.String()
	for _, want := range []string{
		"Consent:", "Identity:", "Mode:",
		"State:", "unknown",
		"Source:", "(none)",
		"Install ID:",
		"Path:",
		"Current:",
		"App prefix:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, got)
		}
	}

	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %q", stderr.String())
	}
}

// TestStatus_PostGranted_HumanRender seeds a granted decision and
// asserts the rendered output surfaces the state, the decided_at
// timestamp, and the recorded source. Decided_at is asserted via
// substring against the year so we don't have to clock-mock the
// store.
func TestStatus_PostGranted_HumanRender(t *testing.T) {
	withFreshXDG(t)

	decided := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	seedConsent(t, consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      decided,
		PromptVersion:  1,
		DecisionSource: consent.SourcePrompt,
	})

	var stdout, stderr bytes.Buffer
	if err := runStatus(context.Background(), &stdout, &stderr, output.Table); err != nil {
		t.Fatalf("runStatus: %v", err)
	}

	got := stdout.String()
	for _, want := range []string{
		"granted",
		"2026-05-19T12:00:00Z",
		"prompt",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestStatus_JSONFormat asserts the JSON render produces a payload
// that round-trips back into StatusOutput and carries the seeded
// State verbatim. Indented form is asserted via "  " prefix detection
// so the indentation contract stays explicit.
func TestStatus_JSONFormat(t *testing.T) {
	withFreshXDG(t)

	seedConsent(t, consent.Decision{
		State:          consent.StateDenied,
		DecidedAt:      time.Now().UTC().Truncate(time.Second),
		PromptVersion:  1,
		DecisionSource: consent.SourceFlag,
	})

	var stdout bytes.Buffer
	if err := runStatus(context.Background(), &stdout, io.Discard, output.JSON); err != nil {
		t.Fatalf("runStatus: %v", err)
	}

	raw := stdout.Bytes()
	if !bytes.Contains(raw, []byte("\n  \"consent\"")) {
		t.Errorf("output not indented: %s", raw)
	}

	var got StatusOutput
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json unmarshal: %v\nraw: %s", err, raw)
	}
	if got.Consent.State != string(consent.StateDenied) {
		t.Errorf("Consent.State = %q, want %q", got.Consent.State, consent.StateDenied)
	}
	if got.Consent.DecisionSource != string(consent.SourceFlag) {
		t.Errorf("Consent.DecisionSource = %q, want %q",
			got.Consent.DecisionSource, consent.SourceFlag)
	}
}

// TestStatus_YAMLFormat asserts the YAML render parses and carries
// the seeded State + prompt_version. YAML keys are checked via the
// wire vocabulary (snake_case) so a Go-field rename without a tag
// update fails this test.
func TestStatus_YAMLFormat(t *testing.T) {
	withFreshXDG(t)

	seedConsent(t, consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      time.Now().UTC().Truncate(time.Second),
		PromptVersion:  2,
		DecisionSource: consent.SourceEnv,
	})

	var stdout bytes.Buffer
	if err := runStatus(context.Background(), &stdout, io.Discard, output.YAML); err != nil {
		t.Fatalf("runStatus: %v", err)
	}

	raw := stdout.Bytes()
	if !bytes.Contains(raw, []byte("prompt_version:")) {
		t.Errorf("YAML missing snake_case prompt_version key: %s", raw)
	}

	var got StatusOutput
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("yaml unmarshal: %v\nraw: %s", err, raw)
	}
	if got.Consent.State != string(consent.StateGranted) {
		t.Errorf("Consent.State = %q, want %q", got.Consent.State, consent.StateGranted)
	}
	if got.Consent.PromptVersion != 2 {
		t.Errorf("Consent.PromptVersion = %d, want 2", got.Consent.PromptVersion)
	}
}

// TestStatus_InstallIDError_RendersGracefully exercises the partial-
// failure path. We point XDG_STATE_HOME at a path the process cannot
// create under (a file that exists where a dir would need to live);
// InstallationID returns an error and identityInfo folds it into the
// rendered InstallationID field as "(error: ...)". The rest of the
// output must still render.
func TestStatus_InstallIDError_RendersGracefully(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)

	// Point XDG_STATE_HOME at a regular file: MkdirAll(parent) will
	// fail because the parent's parent is a file, not a directory.
	stateRoot := t.TempDir()
	blocker := filepath.Join(stateRoot, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(blocker, "nested"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(stateRoot, "_data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(stateRoot, "_cache"))

	var stdout bytes.Buffer
	err := runStatus(context.Background(), &stdout, io.Discard, output.Table)
	// We expect the function itself to succeed — install_id errors
	// fold into the payload, they do not abort the render.
	if err != nil {
		// If consent path resolution also blew up under the hostile
		// state-home (unlikely with XDG_CONFIG_HOME pointing
		// elsewhere), at least confirm the error mentions one of the
		// expected subsystems so a regression here is debuggable.
		t.Fatalf("runStatus errored unexpectedly: %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "(error") {
		t.Errorf("expected '(error' marker in output, got:\n%s", got)
	}
	if !strings.Contains(got, "Mode:") {
		t.Errorf("partial failure dropped the Mode section:\n%s", got)
	}
}

// TestStatus_ModeReflectsCurrent asserts Mode.Current tracks the
// runtime telemetry global. The Mode global is process-wide, so we
// restore the prior Mode in t.Cleanup to keep this test compatible
// with parallel test runs in the same binary.
func TestStatus_ModeReflectsCurrent(t *testing.T) {
	withFreshXDG(t)

	prior := runtimetel.CurrentMode()
	t.Cleanup(func() { runtimetel.SetMode(prior) })

	runtimetel.SetMode(runtimetel.ModeAnon)

	var stdout bytes.Buffer
	if err := runStatus(context.Background(), &stdout, io.Discard, output.JSON); err != nil {
		t.Fatalf("runStatus: %v", err)
	}

	var got StatusOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if got.Mode.Current != "anon" {
		t.Errorf("Mode.Current = %q, want %q", got.Mode.Current, "anon")
	}
}

// TestStatusCmd_Wired smoke-checks that the cobra command can be
// constructed and that it exposes a RunE — guards against the
// AddCommand merge-point regressing into an empty command.
func TestStatusCmd_Wired(t *testing.T) {
	c := statusCmd()
	if c.Use != "status" {
		t.Fatalf("Use = %q, want %q", c.Use, "status")
	}
	if c.RunE == nil {
		t.Fatalf("RunE is nil")
	}
}

// TestCmd_TelemetryTree asserts the parent Cmd wires the status
// subcommand. Future tasks (T-0667..T-0669) extend this with their
// own assertions; the table form below keeps the merge surface
// minimal.
func TestCmd_TelemetryTree(t *testing.T) {
	root := Cmd()
	if root.Use != "telemetry" {
		t.Fatalf("parent Use = %q, want %q", root.Use, "telemetry")
	}

	want := map[string]bool{
		"status": false,
	}
	for _, c := range root.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q not wired under `kit telemetry`", name)
		}
	}
}
