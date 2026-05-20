package compliance

// Tests for rtConsentingTelemetryKillSwitch — sub-conditions (c) and
// (d) honored at runtime.
//
// Strategy: build a tiny stub binary (testdata/stub-telemetry-binary)
// once in TestMain and reuse it across scenarios. The stub honors
// KIT_BUS_SINK_PATH directly so the rtEnv harness can observe events
// without depending on real adopter bus wiring.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubBinaryPath is set by TestMain to the absolute path of the
// freshly-built stub-telemetry-binary. Tests that need a stub binary
// reference this var directly.
var stubBinaryPath string

// TestMain compiles the stub binary once for the whole package run.
// Doing it here (vs go:generate or a build hook) keeps the test
// fixture self-contained — no committed binary, no out-of-tree build
// step.
func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "compliance-stubs-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: mkdir tmpdir: %v\n", err)
		os.Exit(2)
	}
	defer os.RemoveAll(tmpDir)

	stubBinaryPath = filepath.Join(tmpDir, "stub-telemetry-binary")
	// -buildvcs=false avoids the "error obtaining VCS status" failure
	// in tlc bare-worktree layouts where the build can't read .git.
	// The stub binary is throwaway test scaffolding; VCS stamping is
	// irrelevant.
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", stubBinaryPath,
		"./testdata/stub-telemetry-binary")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: build stub: %v\n", err)
		os.Exit(2)
	}

	os.Exit(m.Run())
}

// killSwitchSpec returns a minimal opt-in toolspec the kill-switch
// runtime check will accept: a single read command (idempotent +
// output_schema) and the telemetry block with the requested
// kill_switch_envs.
func killSwitchSpec(killSwitchEnvs []string) *toolspecYAML {
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
			KillSwitchEnvs: killSwitchEnvs,
		},
	}
}

// stubEmitEnv wires STUB_EMIT=<mode> into the rtEnv before Run is
// called. The rtEnv's Env field is what exec.Command uses; injecting
// here mirrors how an adopter test would parameterize stub behavior.
//
// Implementation note: rtEnv.SetEnv is exposed for this kind of use,
// so we can drive the stub via a single env knob without re-wrapping
// the harness.
func stubEmitEnv(mode string) func(*rtEnv) {
	return func(e *rtEnv) { e.SetEnv("STUB_EMIT", mode) }
}

// runKillSwitchWith runs rtConsentingTelemetryKillSwitch with a
// pre-built spec, but first wraps newRTEnv calls so the stub gets
// STUB_EMIT set. Since rtConsentingTelemetryKillSwitch creates its
// own rtEnvs internally, we can't pass an Env modifier through the
// function signature — instead we set STUB_EMIT via the OS env
// before the check runs, and rely on newRTEnv NOT stripping it (it
// only strips HOME/XDG/KIT_BUS_SINK*). t.Setenv handles cleanup.
func runKillSwitchWith(t *testing.T, bin string, spec *toolspecYAML, stubMode string) CheckResult {
	t.Helper()
	t.Setenv("STUB_EMIT", stubMode)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return rtConsentingTelemetryKillSwitch(ctx, bin, spec, func() *rtEnv { return newRTEnv(t) })
}

func TestRtKillSwitch_SkipsWhenNotOptedIn(t *testing.T) {
	// Spec with Telemetry == nil — same gate as the static check.
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

	got := rtConsentingTelemetryKillSwitch(ctx, stubBinaryPath, spec, func() *rtEnv { return newRTEnv(t) })
	if got.Status != "skip" {
		t.Fatalf("status = %q, want skip; details=%q", got.Status, got.Details)
	}
	if got.Factor != FactorConsentingTelemetry {
		t.Fatalf("factor = %v, want FactorConsentingTelemetry", got.Factor)
	}
}

func TestRtKillSwitch_HonorsDoNotTrack(t *testing.T) {
	spec := killSwitchSpec([]string{"DO_NOT_TRACK", "KIT_TELEMETRY_MODE"})
	got := runKillSwitchWith(t, stubBinaryPath, spec, "respect")
	if got.Status != "pass" {
		t.Fatalf("status = %q, want pass; details=%q", got.Status, got.Details)
	}
}

func TestRtKillSwitch_HonorsKitTelemetryMode(t *testing.T) {
	// Same KIT_TELEMETRY_MODE shape as the previous test — the check
	// picks it via pickModeEnv. A separate test name makes the
	// per-condition coverage explicit even though the underlying
	// pass-path is shared with HonorsDoNotTrack.
	spec := killSwitchSpec([]string{"DO_NOT_TRACK", "KIT_TELEMETRY_MODE"})
	got := runKillSwitchWith(t, stubBinaryPath, spec, "respect")
	if got.Status != "pass" {
		t.Fatalf("status = %q, want pass; details=%q", got.Status, got.Details)
	}
	// Belt-and-braces — the pass message names the mode env, so we
	// can assert it picked KIT_TELEMETRY_MODE (not DO_NOT_TRACK).
	if !strings.Contains(got.Details, "KIT_TELEMETRY_MODE") {
		t.Fatalf("details = %q, want it to name KIT_TELEMETRY_MODE", got.Details)
	}
}

func TestRtKillSwitch_HonorsAppPrefixMode(t *testing.T) {
	spec := killSwitchSpec([]string{"DO_NOT_TRACK", "SPACED_TELEMETRY_MODE"})
	got := runKillSwitchWith(t, stubBinaryPath, spec, "respect")
	if got.Status != "pass" {
		t.Fatalf("status = %q, want pass; details=%q", got.Status, got.Details)
	}
	if !strings.Contains(got.Details, "SPACED_TELEMETRY_MODE") {
		t.Fatalf("details = %q, want it to name SPACED_TELEMETRY_MODE", got.Details)
	}
}

func TestRtKillSwitch_FailsWhenEventsLeak(t *testing.T) {
	spec := killSwitchSpec([]string{"DO_NOT_TRACK", "KIT_TELEMETRY_MODE"})
	// STUB_EMIT=on — stub ignores kill-switch envs, so events WILL
	// leak through. The check must catch the DO_NOT_TRACK leak
	// (step 2) and fail with a Details that names the env shape.
	got := runKillSwitchWith(t, stubBinaryPath, spec, "on")
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail; details=%q", got.Status, got.Details)
	}
	// Failure message must name which env-shape leaked so adopters
	// can localize the bug. DO_NOT_TRACK is checked first, so it's
	// the one we'll see in the leak message.
	if !strings.Contains(got.Details, "DO_NOT_TRACK") {
		t.Fatalf("details = %q, want it to name DO_NOT_TRACK leak", got.Details)
	}
	if got.Suggestion == "" {
		t.Fatalf("expected non-empty Suggestion for fail; got empty")
	}
}

func TestRtKillSwitch_SanityCheckCatchesBrokenHarness(t *testing.T) {
	spec := killSwitchSpec([]string{"DO_NOT_TRACK", "KIT_TELEMETRY_MODE"})
	// STUB_EMIT=never — baseline run emits zero events, which means
	// steps 2 + 3 are vacuously true. The check must reject this
	// at the baseline rather than declaring victory.
	got := runKillSwitchWith(t, stubBinaryPath, spec, "never")
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail; details=%q", got.Status, got.Details)
	}
	if !strings.Contains(got.Details, "harness sanity check") {
		t.Fatalf("details = %q, want it to flag the baseline sanity check",
			got.Details)
	}
}

func TestRtKillSwitch_FailsWhenNoModeEnvDeclared(t *testing.T) {
	// Edge case: spec opts in but only declares DO_NOT_TRACK (no
	// *_TELEMETRY_MODE entry). The static check would catch this,
	// but the runtime check should still degrade gracefully rather
	// than panic on an empty modeEnv.
	spec := killSwitchSpec([]string{"DO_NOT_TRACK"})
	got := runKillSwitchWith(t, stubBinaryPath, spec, "respect")
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail; details=%q", got.Status, got.Details)
	}
	if !strings.Contains(got.Details, "TELEMETRY_MODE") {
		t.Fatalf("details = %q, want it to mention the missing mode env",
			got.Details)
	}
}

func TestRtKillSwitch_SkipsWhenNoReadCommand(t *testing.T) {
	// Opt-in spec but no idempotent + output_schema command — the
	// check has no read command to invoke and should skip cleanly.
	spec := &toolspecYAML{
		Name:          "stub",
		SchemaVersion: "1",
		Commands: []commandYAML{
			{Name: "ping"}, // no contract, no output_schema
		},
		Telemetry: &telemetryYAML{
			Enabled:        true,
			KillSwitchEnvs: []string{"DO_NOT_TRACK", "KIT_TELEMETRY_MODE"},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := rtConsentingTelemetryKillSwitch(ctx, stubBinaryPath, spec, func() *rtEnv { return newRTEnv(t) })
	if got.Status != "skip" {
		t.Fatalf("status = %q, want skip; details=%q", got.Status, got.Details)
	}
}

// TestPickModeEnv exercises pickModeEnv directly — table-driven
// because the function is tiny but the cases (kit literal vs
// app-prefixed vs only-DO_NOT_TRACK) all matter.
func TestPickModeEnv(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, ""},
		{"only-dnt", []string{"DO_NOT_TRACK"}, ""},
		{"kit-literal", []string{"DO_NOT_TRACK", "KIT_TELEMETRY_MODE"}, "KIT_TELEMETRY_MODE"},
		{"app-prefix", []string{"DO_NOT_TRACK", "SPACED_TELEMETRY_MODE"}, "SPACED_TELEMETRY_MODE"},
		{"dnt-first-mode-second", []string{"DO_NOT_TRACK", "FOO_TELEMETRY_MODE"}, "FOO_TELEMETRY_MODE"},
		{"non-mode-noise", []string{"DO_NOT_TRACK", "KIT_BUS_SINK", "KIT_TELEMETRY_MODE"}, "KIT_TELEMETRY_MODE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickModeEnv(tc.in)
			if got != tc.want {
				t.Fatalf("pickModeEnv(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
