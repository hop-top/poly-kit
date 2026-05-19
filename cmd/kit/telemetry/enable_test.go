// enable_test.go covers `kit telemetry enable` at the runEnable seam.
// Each test isolates XDG with the same pattern as status_test.go's
// withFreshXDG so the consent file lives under t.TempDir().
//
// The env-blocked tests use t.Setenv (which auto-cleans) and assert
// that the store is NOT mutated when the chain refuses.

package telemetry

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/core/consent"
	runtimetel "hop.top/kit/go/runtime/telemetry"
)

// freshXDGForEnable mirrors status_test.go::withFreshXDG. Kept local
// (different filename, separate test binary slice) so the helper is
// self-contained and doesn't depend on test-init ordering.
func freshXDGForEnable(t *testing.T) {
	t.Helper()
	cfg := t.TempDir()
	st := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_STATE_HOME", st)
	t.Setenv("XDG_DATA_HOME", filepath.Join(st, "_data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(st, "_cache"))
	// Clear DO_NOT_TRACK and the kit/app prefix env so the precedence
	// chain in consentEnvBlocked is in a known state for the default
	// test path. Tests that need a kill switch t.Setenv it themselves.
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("KIT_TELEMETRY_MODE", "")
}

// readBackDecision opens a fresh FileStore under the current XDG dirs
// and returns whatever runEnable just wrote.
func readBackDecision(t *testing.T) consent.Decision {
	t.Helper()
	s, err := consent.NewFileStore()
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	d, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return d
}

func TestEnable_PersistsGrantedDecision(t *testing.T) {
	freshXDGForEnable(t)

	var stdout bytes.Buffer
	if err := runEnable(context.Background(), &stdout, false); err != nil {
		t.Fatalf("runEnable: %v", err)
	}

	d := readBackDecision(t)
	if d.State != consent.StateGranted {
		t.Errorf("State = %q, want %q", d.State, consent.StateGranted)
	}
	if d.DecisionSource != consent.SourceFlag {
		t.Errorf("DecisionSource = %q, want %q", d.DecisionSource, consent.SourceFlag)
	}
	if d.PromptVersion != PromptVersion {
		t.Errorf("PromptVersion = %d, want %d", d.PromptVersion, PromptVersion)
	}
	if d.DecidedAt.IsZero() {
		t.Errorf("DecidedAt is zero; expected a stamped time")
	}
	if !strings.Contains(stdout.String(), "Telemetry enabled") {
		t.Errorf("stdout missing confirmation: %q", stdout.String())
	}
}

func TestEnable_RefusesOnDoNotTrack(t *testing.T) {
	freshXDGForEnable(t)
	t.Setenv("DO_NOT_TRACK", "1")

	var stdout bytes.Buffer
	err := runEnable(context.Background(), &stdout, false)
	if err == nil {
		t.Fatal("runEnable: expected refusal, got nil")
	}
	if !strings.Contains(err.Error(), "DO_NOT_TRACK") {
		t.Errorf("error %q does not name DO_NOT_TRACK", err)
	}

	// Store must be untouched: Get returns StateUnknown.
	d := readBackDecision(t)
	if d.State != consent.StateUnknown {
		t.Errorf("State after refusal = %q, want %q", d.State, consent.StateUnknown)
	}
}

func TestEnable_RefusesOnKitTelemetryModeOff(t *testing.T) {
	freshXDGForEnable(t)
	t.Setenv("KIT_TELEMETRY_MODE", "off")

	var stdout bytes.Buffer
	err := runEnable(context.Background(), &stdout, false)
	if err == nil {
		t.Fatal("runEnable: expected refusal, got nil")
	}
	if !strings.Contains(err.Error(), "KIT_TELEMETRY_MODE") {
		t.Errorf("error %q does not name KIT_TELEMETRY_MODE", err)
	}

	d := readBackDecision(t)
	if d.State != consent.StateUnknown {
		t.Errorf("State after refusal = %q, want %q", d.State, consent.StateUnknown)
	}
}

func TestEnable_RefusesOnAppPrefixModeOff(t *testing.T) {
	freshXDGForEnable(t)
	runtimetel.SetAppPrefix("spaced")
	t.Cleanup(func() { runtimetel.SetAppPrefix("") })
	t.Setenv("SPACED_TELEMETRY_MODE", "off")

	var stdout bytes.Buffer
	err := runEnable(context.Background(), &stdout, false)
	if err == nil {
		t.Fatal("runEnable: expected refusal, got nil")
	}
	if !strings.Contains(err.Error(), "SPACED_TELEMETRY_MODE") {
		t.Errorf("error %q does not name SPACED_TELEMETRY_MODE", err)
	}

	d := readBackDecision(t)
	if d.State != consent.StateUnknown {
		t.Errorf("State after refusal = %q, want %q", d.State, consent.StateUnknown)
	}
}

func TestEnable_DryRunDoesNotWrite(t *testing.T) {
	freshXDGForEnable(t)

	var stdout bytes.Buffer
	if err := runEnable(context.Background(), &stdout, true); err != nil {
		t.Fatalf("runEnable dry-run: %v", err)
	}

	d := readBackDecision(t)
	if d.State != consent.StateUnknown {
		t.Errorf("dry-run wrote to store: state = %q", d.State)
	}
	if !strings.Contains(stdout.String(), "dry-run") {
		t.Errorf("stdout missing dry-run marker: %q", stdout.String())
	}
}

func TestEnable_PreservesPriorOtherKeys(t *testing.T) {
	freshXDGForEnable(t)

	// Pre-seed the YAML with an unrelated top-level key. The FileStore
	// promises to preserve sibling keys; this test asserts the chain
	// (enable -> FileStore.Set) keeps that promise end to end.
	s, err := consent.NewFileStore()
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	path := s.Path()
	if err := writeSeedFile(path, "other: foo\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var stdout bytes.Buffer
	if err := runEnable(context.Background(), &stdout, false); err != nil {
		t.Fatalf("runEnable: %v", err)
	}

	contents, err := readFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(contents, "other: foo") {
		t.Errorf("enable nuked sibling key. file:\n%s", contents)
	}
	if !strings.Contains(contents, "state: granted") {
		t.Errorf("enable did not write granted. file:\n%s", contents)
	}
}
