package compliance

// Tests OF the runtime-check harness (rtEnv). Tests USING it
// (the kill-switch / inspect / prompt sub-checks) live in separate
// files. Using the internal-test package (compliance, not
// compliance_test) so we can exercise the unexported rtEnv surface
// without forcing it to be exported.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewRTEnv_CreatesIsolatedDirs(t *testing.T) {
	e := newRTEnv(t)

	for _, p := range []string{e.HomeDir, e.XDGConfig, e.XDGState} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected %s to be a directory", p)
		}
	}

	if filepath.Dir(e.XDGConfig) != e.HomeDir {
		t.Fatalf("XDGConfig %s not under HomeDir %s", e.XDGConfig, e.HomeDir)
	}
	// XDGState is <HomeDir>/.local/state — its grandparent is HomeDir.
	if filepath.Dir(filepath.Dir(e.XDGState)) != e.HomeDir {
		t.Fatalf("XDGState %s not under HomeDir %s", e.XDGState, e.HomeDir)
	}
	if filepath.Dir(e.BusFile) != e.HomeDir {
		t.Fatalf("BusFile %s not under HomeDir %s", e.BusFile, e.HomeDir)
	}

	// BusFile should NOT exist yet — sinks create it on first write.
	if _, err := os.Stat(e.BusFile); !os.IsNotExist(err) {
		t.Fatalf("expected BusFile %s to NOT exist yet, got err=%v", e.BusFile, err)
	}
}

func TestNewRTEnv_EnvStripsInheritedHOME(t *testing.T) {
	// t.Setenv handles cleanup automatically.
	t.Setenv("HOME", "/sentinel/path")
	t.Setenv("XDG_CONFIG_HOME", "/sentinel/config")
	t.Setenv("XDG_STATE_HOME", "/sentinel/state")
	t.Setenv("KIT_BUS_SINK", "sentinel-sink")
	t.Setenv("KIT_BUS_SINK_PATH", "/sentinel/bus")

	e := newRTEnv(t)

	checks := map[string]string{
		"HOME":              "/sentinel/path",
		"XDG_CONFIG_HOME":   "/sentinel/config",
		"XDG_STATE_HOME":    "/sentinel/state",
		"KIT_BUS_SINK":      "sentinel-sink",
		"KIT_BUS_SINK_PATH": "/sentinel/bus",
	}
	for key, sentinel := range checks {
		sentinelKV := key + "=" + sentinel
		for _, kv := range e.Env {
			if kv == sentinelKV {
				t.Fatalf("Env still contains inherited %s", sentinelKV)
			}
		}
	}

	// Positively assert the harness's own values are present.
	want := map[string]string{
		"HOME":              e.HomeDir,
		"XDG_CONFIG_HOME":   e.XDGConfig,
		"XDG_STATE_HOME":    e.XDGState,
		"KIT_BUS_SINK":      "jsonl",
		"KIT_BUS_SINK_PATH": e.BusFile,
	}
	for k, v := range want {
		if !envContains(e.Env, k, v) {
			t.Fatalf("Env missing %s=%s", k, v)
		}
	}
}

func TestRun_CapturesStdoutStderrExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell semantics required")
	}
	e := newRTEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stdout, stderr, code := e.Run(ctx, "/bin/sh", "-c", "echo hi >&2 ; echo bye ; exit 7")
	if stdout != "bye\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "bye\n")
	}
	if stderr != "hi\n" {
		t.Fatalf("stderr = %q, want %q", stderr, "hi\n")
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
}

func TestRun_TimeoutKillsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell semantics required")
	}
	e := newRTEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, code := e.Run(ctx, "/bin/sh", "-c", "sleep 10")
	elapsed := time.Since(start)

	if code == 0 {
		t.Fatalf("expected non-zero exit on timeout, got 0")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("Run took %v, expected <1s after 100ms deadline", elapsed)
	}
}

func TestEventsEmitted_MissingFileReturnsNil(t *testing.T) {
	e := newRTEnv(t)
	evs := e.EventsEmitted()
	if evs != nil {
		t.Fatalf("expected nil events for missing BusFile, got %v", evs)
	}
}

func TestEventsEmitted_ReadsJSONLines(t *testing.T) {
	e := newRTEnv(t)
	lines := []map[string]any{
		{"topic": "kit.config.snapshot.reloaded", "n": float64(1)},
		{"topic": "kit.telemetry.invocation.emitted", "n": float64(2)},
		{"topic": "kit.telemetry.redact.matched", "n": float64(3)},
	}
	writeJSONL(t, e.BusFile, lines)

	got := e.EventsEmitted()
	if len(got) != 3 {
		t.Fatalf("len(events) = %d, want 3 (got: %v)", len(got), got)
	}
	for i, want := range lines {
		if got[i]["topic"] != want["topic"] {
			t.Fatalf("events[%d].topic = %v, want %v", i, got[i]["topic"], want["topic"])
		}
		if got[i]["n"] != want["n"] {
			t.Fatalf("events[%d].n = %v, want %v", i, got[i]["n"], want["n"])
		}
	}
}

func TestEventsEmitted_SkipsMalformed(t *testing.T) {
	e := newRTEnv(t)
	var lines []string
	if b, err := json.Marshal(map[string]any{"topic": "good.one.a.b"}); err == nil {
		lines = append(lines, string(b))
	}
	lines = append(lines, "this is not json {")
	if b, err := json.Marshal(map[string]any{"topic": "good.two.a.b"}); err == nil {
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(e.BusFile, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write busfile: %v", err)
	}

	got := e.EventsEmitted()
	if len(got) != 2 {
		t.Fatalf("len(events) = %d, want 2 (malformed line should be skipped); got %v", len(got), got)
	}
	if got[0]["topic"] != "good.one.a.b" || got[1]["topic"] != "good.two.a.b" {
		t.Fatalf("topics = [%v, %v], want [good.one.a.b, good.two.a.b]",
			got[0]["topic"], got[1]["topic"])
	}
}

func TestPollEvents_ReturnsImmediatelyWhenSatisfied(t *testing.T) {
	e := newRTEnv(t)
	var lines []map[string]any
	for i := 0; i < 5; i++ {
		lines = append(lines, map[string]any{"topic": fmt.Sprintf("a.b.c.d%d", i)})
	}
	writeJSONL(t, e.BusFile, lines)

	start := time.Now()
	got, ok := e.PollEvents(3, 1*time.Second)
	elapsed := time.Since(start)
	if !ok {
		t.Fatalf("PollEvents ok=false despite 5 events present")
	}
	if len(got) != 5 {
		t.Fatalf("len(events) = %d, want 5", len(got))
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("PollEvents took %v, expected <100ms when already satisfied", elapsed)
	}
}

func TestPollEvents_DeadlineWithNoEvents(t *testing.T) {
	e := newRTEnv(t)
	start := time.Now()
	got, ok := e.PollEvents(1, 200*time.Millisecond)
	elapsed := time.Since(start)
	if ok {
		t.Fatalf("PollEvents ok=true on empty BusFile, got %v", got)
	}
	if got != nil {
		t.Fatalf("expected nil events, got %v", got)
	}
	// Allow some slack for goroutine scheduling.
	if elapsed < 180*time.Millisecond {
		t.Fatalf("PollEvents returned in %v, expected ~200ms deadline", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("PollEvents took %v, expected ~200ms deadline", elapsed)
	}
}

func TestSeedConsent_GrantedFixture(t *testing.T) {
	e := newRTEnv(t)
	if err := e.SeedConsent("granted", "prompt", 1); err != nil {
		t.Fatalf("SeedConsent: %v", err)
	}

	path := filepath.Join(e.XDGConfig, "kit", "config.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read consent file: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		"kit:",
		"telemetry:",
		"consent:",
		"state: granted",
		"prompt_version: 1",
		"decision_source: prompt",
		"decided_at: 2026-05-19T12:00:00Z",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("consent file missing %q\n---\n%s\n---", want, got)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat consent file: %v", err)
	}
	// Belt-and-braces: consent file SHOULD be 0600.
	// Mask off setuid/setgid/sticky bits; we only care about rwx for
	// owner/group/other. Test ensures we don't accidentally write
	// world-readable.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("consent file perm = %o, want 0600", perm)
	}
}

// TestSeedLegacyConsent_WritesPreRefactorShape verifies the
// legacy-fallback seeder lays down the pre-config.yaml layout.
func TestSeedLegacyConsent_WritesPreRefactorShape(t *testing.T) {
	e := newRTEnv(t)
	if err := e.SeedLegacyConsent("granted", "prompt", 1); err != nil {
		t.Fatalf("SeedLegacyConsent: %v", err)
	}
	path := filepath.Join(e.XDGConfig, "kit", "telemetry.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read legacy consent file: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		"telemetry:",
		"consent:",
		"state: granted",
		"prompt_version: 1",
		"decision_source: prompt",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("legacy consent file missing %q\n---\n%s\n---", want, got)
		}
	}
	// Critical negative assertion: legacy shape has NO `kit:` top key.
	if strings.HasPrefix(got, "kit:") {
		t.Fatalf("legacy seeder accidentally wrote canonical shape\n---\n%s\n---", got)
	}
}

func TestSetEnv_OverridesExisting(t *testing.T) {
	e := newRTEnv(t)
	if !envContains(e.Env, "KIT_BUS_SINK", "jsonl") {
		t.Fatalf("precondition: KIT_BUS_SINK=jsonl not in Env")
	}

	e.SetEnv("KIT_BUS_SINK", "stdout")

	if envContains(e.Env, "KIT_BUS_SINK", "jsonl") {
		t.Fatalf("old KIT_BUS_SINK=jsonl still present after SetEnv")
	}
	if !envContains(e.Env, "KIT_BUS_SINK", "stdout") {
		t.Fatalf("new KIT_BUS_SINK=stdout not present after SetEnv")
	}

	// Idempotency: SetEnv twice with same key should leave exactly
	// one entry.
	e.SetEnv("NEW_HARNESS_VAR", "v1")
	e.SetEnv("NEW_HARNESS_VAR", "v2")
	count := 0
	for _, kv := range e.Env {
		if strings.HasPrefix(kv, "NEW_HARNESS_VAR=") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one NEW_HARNESS_VAR entry, got %d", count)
	}
}

func TestUnsetEnv(t *testing.T) {
	e := newRTEnv(t)
	if !envContains(e.Env, "HOME", e.HomeDir) {
		t.Fatalf("precondition: HOME not in Env")
	}

	e.UnsetEnv("HOME")

	for _, kv := range e.Env {
		if strings.HasPrefix(kv, "HOME=") {
			t.Fatalf("HOME= still present after UnsetEnv: %s", kv)
		}
	}
}

// -- test helpers ---------------------------------------------------

// envContains reports whether env carries key=value.
func envContains(env []string, key, value string) bool {
	target := key + "=" + value
	for _, kv := range env {
		if kv == target {
			return true
		}
	}
	return false
}

// writeJSONL marshals each map as a single line and writes the
// concatenation to path with 0o600 perms. Fails the test on any
// marshal or write error.
func writeJSONL(t *testing.T, path string, lines []map[string]any) {
	t.Helper()
	var buf strings.Builder
	for _, m := range lines {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
