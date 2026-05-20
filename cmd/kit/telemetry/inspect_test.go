// inspect_test.go covers `kit telemetry inspect` at the runInspect
// seam. Each test points XDG_STATE_HOME at a t.TempDir() so the
// spool dir resolves into isolated state, hand-writes spool files
// onto disk (bypassing the live sink — we want byte-exact control
// over what is queued), and drives runInspect directly.
//
// The byte-exact write avoids dragging in the HTTPSSink for tests
// that are about the inspect read path. The runtime sink has its
// own tests in hop.top/kit/go/runtime/telemetry/sink_https_test.go.

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
)

// withFreshXDGInspect points the XDG dirs at fresh t.TempDir roots
// for the duration of the test. Mirrors the withFreshXDG helper in
// status_test.go (kept separate to avoid cross-file ordering coupling
// in the test binary).
func withFreshXDGInspect(t *testing.T) string {
	t.Helper()
	cfg := t.TempDir()
	st := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_STATE_HOME", st)
	t.Setenv("XDG_DATA_HOME", filepath.Join(st, "_data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(st, "_cache"))
	return st
}

// writeSpoolFile writes lines to <spoolDir>/<stem>.jsonl, joining
// them with a newline and adding a trailing newline. Used to seed
// hand-crafted spool entries that runInspect will then read.
func writeSpoolFile(t *testing.T, spoolDir, stem string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		t.Fatalf("mkdir spool: %v", err)
	}
	path := filepath.Join(spoolDir, stem+".jsonl")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write spool: %v", err)
	}
}

// resolveSpoolDirForTest reproduces inspectSpoolDirPath inside the
// test so seed writes go to the same directory inspectCmd will
// resolve at read time. Cheaper than poking unexported helpers from
// across packages.
func resolveSpoolDirForTest(t *testing.T) string {
	t.Helper()
	dir, err := inspectSpoolDirPath()
	if err != nil {
		t.Fatalf("inspectSpoolDirPath: %v", err)
	}
	return dir
}

// TestInspect_EmptySpool_PrintsInfo runs against a fresh XDG dir
// (no spool dir, no files). The function should exit 0 and print
// the informational empty-spool message. This is THE common case
// for a healthy adopter.
func TestInspect_EmptySpool_PrintsInfo(t *testing.T) {
	withFreshXDGInspect(t)

	var stdout, stderr bytes.Buffer
	if err := runInspect(context.Background(), &stdout, &stderr, 10); err != nil {
		t.Fatalf("runInspect: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "No spooled telemetry events") {
		t.Errorf("expected informational empty message, got: %q", out)
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %q", stderr.String())
	}
}

// TestInspect_SingleFile_ReadsAllUpToN seeds 5 events in a single
// spool file, asks for 10, and asserts all 5 are emitted. The line
// count in the output is the operational assertion; the byte-level
// shape is asserted with a contains-check on a distinguishing
// substring per event.
func TestInspect_SingleFile_ReadsAllUpToN(t *testing.T) {
	withFreshXDGInspect(t)
	spool := resolveSpoolDirForTest(t)

	lines := []string{
		`{"name":"kit.cmd.run","seq":1}`,
		`{"name":"kit.cmd.run","seq":2}`,
		`{"name":"kit.cmd.run","seq":3}`,
		`{"name":"kit.cmd.run","seq":4}`,
		`{"name":"kit.cmd.run","seq":5}`,
	}
	writeSpoolFile(t, spool, "2026-05-19", lines)

	var stdout, stderr bytes.Buffer
	if err := runInspect(context.Background(), &stdout, &stderr, 10); err != nil {
		t.Fatalf("runInspect: %v", err)
	}

	outLines := splitNonEmpty(stdout.Bytes())
	if len(outLines) != 5 {
		t.Fatalf("emitted %d lines, want 5; output:\n%s", len(outLines), stdout.String())
	}
	for _, want := range []string{`"seq":1`, `"seq":2`, `"seq":3`, `"seq":4`, `"seq":5`} {
		if !bytes.Contains(stdout.Bytes(), []byte(want)) {
			t.Errorf("missing %q in output:\n%s", want, stdout.String())
		}
	}
}

// TestInspect_NLessThanTotal seeds 5 events, asks for 3, and asserts
// exactly 3 lines are emitted — and that they are the NEWEST 3
// (seq=5, 4, 3) in newest-first order, per the documented ordering
// contract in runInspect.
func TestInspect_NLessThanTotal(t *testing.T) {
	withFreshXDGInspect(t)
	spool := resolveSpoolDirForTest(t)

	writeSpoolFile(t, spool, "2026-05-19", []string{
		`{"seq":1}`,
		`{"seq":2}`,
		`{"seq":3}`,
		`{"seq":4}`,
		`{"seq":5}`,
	})

	var stdout, stderr bytes.Buffer
	if err := runInspect(context.Background(), &stdout, &stderr, 3); err != nil {
		t.Fatalf("runInspect: %v", err)
	}

	out := splitNonEmpty(stdout.Bytes())
	if len(out) != 3 {
		t.Fatalf("emitted %d lines, want 3; output:\n%s", len(out), stdout.String())
	}
	wantOrder := []string{`{"seq":5}`, `{"seq":4}`, `{"seq":3}`}
	for i, w := range wantOrder {
		if string(out[i]) != w {
			t.Errorf("line %d = %q, want %q", i, out[i], w)
		}
	}
}

// TestInspect_MultipleFiles_NewestFirst seeds two spool files with
// distinct dates. The newer file's events must appear before the
// older file's events in the emitted JSONL stream.
//
// Documented ordering: across files, NEWEST filename first
// (reverse-lexicographic on YYYY-MM-DD). Within a file, NEWEST line
// first (last-appended is most recent).
func TestInspect_MultipleFiles_NewestFirst(t *testing.T) {
	withFreshXDGInspect(t)
	spool := resolveSpoolDirForTest(t)

	writeSpoolFile(t, spool, "2026-05-18", []string{
		`{"day":18,"seq":1}`,
		`{"day":18,"seq":2}`,
		`{"day":18,"seq":3}`,
	})
	writeSpoolFile(t, spool, "2026-05-19", []string{
		`{"day":19,"seq":1}`,
		`{"day":19,"seq":2}`,
		`{"day":19,"seq":3}`,
	})

	var stdout, stderr bytes.Buffer
	if err := runInspect(context.Background(), &stdout, &stderr, 5); err != nil {
		t.Fatalf("runInspect: %v", err)
	}

	out := splitNonEmpty(stdout.Bytes())
	if len(out) != 5 {
		t.Fatalf("emitted %d lines, want 5; output:\n%s", len(out), stdout.String())
	}

	// The newest file (2026-05-19) contributes 3 events in newest-line-
	// first order: seq=3, 2, 1. Then we cross into the older file
	// (2026-05-18) and contribute 2 more (seq=3, 2) before hitting n=5.
	wantOrder := []string{
		`{"day":19,"seq":3}`,
		`{"day":19,"seq":2}`,
		`{"day":19,"seq":1}`,
		`{"day":18,"seq":3}`,
		`{"day":18,"seq":2}`,
	}
	for i, w := range wantOrder {
		if string(out[i]) != w {
			t.Errorf("line %d = %q, want %q", i, out[i], w)
		}
	}
}

// TestInspect_MalformedLineSkipped seeds a spool file with 2 valid
// events and 1 garbled middle line. The valid 2 must be emitted; the
// garbled line is skipped with a warning to stderr but does NOT
// abort the audit.
//
// The garbled line is placed in the middle of the file (not the
// tail) so the test exercises the "skip and continue" path even
// after the scanner has already accepted later valid lines.
func TestInspect_MalformedLineSkipped(t *testing.T) {
	withFreshXDGInspect(t)
	spool := resolveSpoolDirForTest(t)

	writeSpoolFile(t, spool, "2026-05-19", []string{
		`{"seq":1}`,
		`{this is not json}`,
		`{"seq":3}`,
	})

	var stdout, stderr bytes.Buffer
	if err := runInspect(context.Background(), &stdout, &stderr, 10); err != nil {
		t.Fatalf("runInspect: %v", err)
	}

	out := splitNonEmpty(stdout.Bytes())
	if len(out) != 2 {
		t.Fatalf("emitted %d lines, want 2; output:\n%s\nstderr:\n%s",
			len(out), stdout.String(), stderr.String())
	}
	// Confirm both valid events round-trip as JSON (i.e. we did not
	// emit the garbled line by mistake).
	for _, line := range out {
		var probe any
		if err := json.Unmarshal(line, &probe); err != nil {
			t.Errorf("emitted line is not valid JSON: %q (%v)", line, err)
		}
	}
	// Stderr should mention the skipped line so the operator notices.
	if !strings.Contains(stderr.String(), "malformed") {
		t.Errorf("expected stderr warning for malformed line, got: %q", stderr.String())
	}
}

// TestInspect_PostRedactInvariant documents the inspect/redact
// contract in code. The redact contract belongs to the EMITTER (and
// is tested in hop.top/kit/go/runtime/telemetry/redactor_test.go).
// inspect's contract is "show what was written to the spool,
// faithfully" — it does NOT re-run redaction on read.
//
// We seed a spool file whose payload contains a literal "sk-anth..."
// string. In production this would have been redacted UPSTREAM at
// emit time; here we are simulating a hypothetical redact bypass to
// prove that inspect would surface the failure (not silently mask
// it). A trust-building audit tool must show what is on disk; if it
// silently sanitized, an operator could never detect a redactor
// regression.
//
// THE TEST ASSERTS: the file contents are faithfully reproduced.
// IT DOES NOT ASSERT: that redact happened (different layer, tested
// elsewhere).
func TestInspect_PostRedactInvariant(t *testing.T) {
	withFreshXDGInspect(t)
	spool := resolveSpoolDirForTest(t)

	const sentinel = "sk-anthropic_abc123_test_secret"
	writeSpoolFile(t, spool, "2026-05-19", []string{
		`{"name":"kit.cmd.run","args":["` + sentinel + `"]}`,
	})

	var stdout, stderr bytes.Buffer
	if err := runInspect(context.Background(), &stdout, &stderr, 10); err != nil {
		t.Fatalf("runInspect: %v", err)
	}

	if !bytes.Contains(stdout.Bytes(), []byte(sentinel)) {
		t.Errorf("inspect dropped or rewrote bytes; expected sentinel %q in:\n%s",
			sentinel, stdout.String())
	}
}

// TestInspect_DefaultN10WhenFlagsZero documents the default-N
// behavior. With both --next and --last unset (n=0 at the runE
// boundary), inspect surfaces 10 events. We seed 15 and assert
// exactly 10 are emitted.
//
// This test drives inspectCmd via cobra so the flag-defaulting in
// the cobra wrapper is exercised — runInspect alone never sees
// n=0, the wrapper clamps it.
func TestInspect_DefaultN10WhenFlagsZero(t *testing.T) {
	withFreshXDGInspect(t)
	spool := resolveSpoolDirForTest(t)

	lines := make([]string, 0, 15)
	for i := 1; i <= 15; i++ {
		lines = append(lines, `{"seq":`+itoa(i)+`}`)
	}
	writeSpoolFile(t, spool, "2026-05-19", lines)

	cmd := inspectCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got := len(splitNonEmpty(stdout.Bytes())); got != 10 {
		t.Errorf("default-N emitted %d lines, want 10; output:\n%s",
			got, stdout.String())
	}
}

// TestInspectCmd_Wired smoke-checks that the cobra command can be
// constructed and that it exposes a RunE — guards against the
// AddCommand merge-point regressing into an empty command.
func TestInspectCmd_Wired(t *testing.T) {
	c := inspectCmd()
	if c.Use != "inspect" {
		t.Fatalf("Use = %q, want %q", c.Use, "inspect")
	}
	if c.RunE == nil {
		t.Fatalf("RunE is nil")
	}
	if c.Flags().Lookup("next") == nil {
		t.Errorf("--next flag not wired")
	}
	if c.Flags().Lookup("last") == nil {
		t.Errorf("--last flag not wired")
	}
}

// TestCmd_TelemetryTree_HasInspect asserts the parent Cmd wires the
// inspect subcommand. This test is permissive about other siblings
// (siblings land in parallel); it only fails if inspect is missing.
func TestCmd_TelemetryTree_HasInspect(t *testing.T) {
	root := Cmd()
	if root.Use != "telemetry" {
		t.Fatalf("parent Use = %q, want %q", root.Use, "telemetry")
	}
	for _, c := range root.Commands() {
		if c.Name() == "inspect" {
			return
		}
	}
	t.Errorf("subcommand %q not wired under `kit telemetry`", "inspect")
}

// splitNonEmpty splits buf on newlines and drops empty trailing
// lines. Used to count emitted JSONL records without tripping on
// the final newline.
func splitNonEmpty(buf []byte) [][]byte {
	parts := bytes.Split(buf, []byte("\n"))
	out := make([][]byte, 0, len(parts))
	for _, p := range parts {
		if len(bytes.TrimSpace(p)) == 0 {
			continue
		}
		out = append(out, p)
	}
	return out
}

// itoa is a local int→string helper kept tiny to avoid pulling in
// strconv for a single call site in the default-N test.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Ensure io is referenced — keeps the import explicit in case a
// future test grows past bytes.Buffer.
var _ io.Writer = (*bytes.Buffer)(nil)
