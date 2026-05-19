package compliance

// Tests for rtConsentingTelemetryInspect — ADR-0037 sub-conditions
// (b) + (g) honored at runtime, plus the audit-topic load-bearing
// assertion.
//
// Strategy: build the inspect-flavored stub binary
// (testdata/stub-telemetry-binary-inspect) once and drive scenarios
// via STUB_INSPECT modes. The stub honors KIT_BUS_SINK_PATH directly
// — same pattern as the kill-switch stub — so the rtEnv harness can
// observe redact-audit events without depending on adopter bus
// wiring.
//
// Build coordination: the kill-switch TestMain
// (runtime_killswitch_test.go) builds its own stub. There is one
// TestMain per package, so this file uses a sync.Once-gated helper
// instead of declaring a second TestMain. The stub is built lazily
// on first use; the once-guard guarantees a single build per test
// process even with -p parallel.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// inspectStubPath caches the absolute path to the freshly-built
// inspect stub. Set by ensureInspectStubBuilt() on first call.
var (
	inspectStubPath   string
	inspectStubOnce   sync.Once
	inspectStubBuildE error
)

// ensureInspectStubBuilt compiles the inspect stub binary into the
// test process's temp dir on first call. Subsequent calls reuse the
// cached path. The build dir is leaked intentionally — tests are
// short-lived processes and the OS reaps /tmp on its own schedule;
// adding teardown machinery here would mean coordinating with the
// sibling TestMain in runtime_killswitch_test.go.
//
// -buildvcs=false mirrors the kill-switch stub build flag — avoids
// the "error obtaining VCS status" failure in tlc bare-worktree
// layouts where the build can't read .git. Stub binaries are
// throwaway test scaffolding; VCS stamping is irrelevant.
func ensureInspectStubBuilt(t *testing.T) string {
	t.Helper()
	inspectStubOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "compliance-inspect-stub-*")
		if err != nil {
			inspectStubBuildE = fmt.Errorf("mkdir tmpdir: %w", err)
			return
		}
		path := filepath.Join(tmpDir, "stub-telemetry-binary-inspect")
		cmd := exec.Command("go", "build", "-buildvcs=false", "-o", path,
			"./testdata/stub-telemetry-binary-inspect")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			inspectStubBuildE = fmt.Errorf("go build inspect stub: %w", err)
			return
		}
		inspectStubPath = path
	})
	if inspectStubBuildE != nil {
		t.Fatalf("build inspect stub: %v", inspectStubBuildE)
	}
	return inspectStubPath
}

// inspectSpec returns a minimal opt-in toolspec the inspect runtime
// check will accept: a single read command + the telemetry block
// with the canonical consent subcommands declared. Tests that need
// a non-canonical subcommand set override .Telemetry directly on
// the returned struct.
func inspectSpec() *toolspecYAML {
	idempotent := true
	return &toolspecYAML{
		Name:          "stub-inspect",
		SchemaVersion: "1",
		Commands: []commandYAML{
			{
				Name:         "status",
				Contract:     &contractYAML{Idempotent: &idempotent},
				OutputSchema: &outSchemaYAML{Format: "json"},
			},
		},
		Telemetry: &telemetryYAML{
			Enabled:            true,
			ConsentSubcommands: []string{"status", "enable", "disable", "reset", "inspect"},
		},
	}
}

// runInspectWith builds the stub, wires STUB_INSPECT=<mode> via
// t.Setenv (newRTEnv only strips HOME/XDG/KIT_BUS_SINK*, so
// STUB_INSPECT flows through to subprocess Env), and invokes the
// check with a freshly-built envFactory closure.
//
// Each scenario rebuilds the rtEnv per inner call (the check creates
// two — one for arm A, one for arms B+C). The closure returns a new
// rtEnv each invocation so first-run state doesn't bleed.
func runInspectWith(t *testing.T, spec *toolspecYAML, stubMode string) CheckResult {
	t.Helper()
	bin := ensureInspectStubBuilt(t)
	t.Setenv("STUB_INSPECT", stubMode)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return rtConsentingTelemetryInspect(ctx, bin, spec, func() *rtEnv {
		return newRTEnv(t)
	})
}

func TestRtInspect_SkipsWhenNotOptedIn(t *testing.T) {
	// Spec with Telemetry == nil — same gate as the static check.
	idempotent := true
	spec := &toolspecYAML{
		Name:          "stub-inspect",
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
	bin := ensureInspectStubBuilt(t)

	got := rtConsentingTelemetryInspect(ctx, bin, spec, func() *rtEnv { return newRTEnv(t) })
	if got.Status != "skip" {
		t.Fatalf("status = %q, want skip; details=%q", got.Status, got.Details)
	}
	if got.Factor != FactorConsentingTelemetry {
		t.Fatalf("factor = %v, want FactorConsentingTelemetry", got.Factor)
	}
}

func TestRtInspect_PassWhenAllSubcommandsRespond(t *testing.T) {
	// "normal" mode: all subcommands return valid JSON; inspect
	// returns an object after redaction; audit topic fires.
	spec := inspectSpec()
	got := runInspectWith(t, spec, "normal")
	if got.Status != "pass" {
		t.Fatalf("status = %q, want pass; details=%q", got.Status, got.Details)
	}
}

func TestRtInspect_FailWhenInspectMissing(t *testing.T) {
	// "missing-inspect" mode: telemetry inspect returns exit 1 with
	// "unknown command". Other subcommands still respond.
	spec := inspectSpec()
	got := runInspectWith(t, spec, "missing-inspect")
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail; details=%q", got.Status, got.Details)
	}
	if !strings.Contains(got.Details, "inspect") {
		t.Fatalf("details = %q, want it to mention 'inspect'", got.Details)
	}
}

func TestRtInspect_FailWhenInspectReturnsPrimitive(t *testing.T) {
	// "primitive" mode: inspect returns the literal `true`. JSON-valid
	// but not an object/array — the strict (b) arm rejects it.
	spec := inspectSpec()
	got := runInspectWith(t, spec, "primitive")
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail; details=%q", got.Status, got.Details)
	}
	// The error message must distinguish "primitive" from generic
	// "invalid JSON" — adopters need the actionable signal.
	if !strings.Contains(got.Details, "primitive") &&
		!strings.Contains(got.Details, "object/array") {
		t.Fatalf("details = %q, want it to mention primitive or object/array", got.Details)
	}
}

func TestRtInspect_SkipWhenTestHookUnavailable(t *testing.T) {
	// "no-test-hook" mode: subcommands all respond, but inspect
	// ignores KIT_TELEMETRY_TEST_INJECT and returns `{}`. The check
	// detects this (no raw PII AND no placeholders in output) and
	// skips arms B + C with a reason — overall result is skip with
	// no failure noise.
	spec := inspectSpec()
	got := runInspectWith(t, spec, "no-test-hook")
	if got.Status != "skip" {
		t.Fatalf("status = %q, want skip; details=%q", got.Status, got.Details)
	}
	if !strings.Contains(got.Details, "KIT_TELEMETRY_TEST_INJECT") &&
		!strings.Contains(got.Details, "test-inject hook") {
		t.Fatalf("details = %q, want it to name the missing test hook", got.Details)
	}
}

func TestRtInspect_PassWhenRedactStrips(t *testing.T) {
	// "normal" mode (re-asserted with stricter checks): raw PII MUST
	// be absent from output; placeholders MUST be present. This is
	// the canonical pass-path; we duplicate the TestRtInspect_Pass
	// assertion because the surface area of "passes" is bigger than
	// "all subcommands respond" — we also need redact behavior +
	// audit topic to be observable, not just the subcommand surface.
	spec := inspectSpec()
	got := runInspectWith(t, spec, "normal")
	if got.Status != "pass" {
		t.Fatalf("status = %q, want pass; details=%q", got.Status, got.Details)
	}
	// Pass-message wording lock — downstream report rendering keys
	// off this phrasing in format.go's text formatter.
	for _, want := range []string{"post-redact", "audit topic"} {
		if !strings.Contains(got.Details, want) {
			t.Fatalf("details = %q, want substring %q", got.Details, want)
		}
	}
}

func TestRtInspect_FailsWhenRedactBypassed(t *testing.T) {
	// "no-redact" mode: stub echoes the inject payload verbatim.
	// Raw PII present → arm B fails; audit topic also silent → arm C
	// would fail too, but arm B's leak is the dominant signal.
	spec := inspectSpec()
	got := runInspectWith(t, spec, "no-redact")
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail; details=%q", got.Status, got.Details)
	}
	if !strings.Contains(got.Details, "leaked raw PII") {
		t.Fatalf("details = %q, want it to flag a PII leak", got.Details)
	}
	// The failure message must name at least one of the leaked
	// patterns so adopters can localize the bug.
	if !strings.Contains(got.Details, "email") &&
		!strings.Contains(got.Details, "token") {
		t.Fatalf("details = %q, want it to name the leaked pattern", got.Details)
	}
}

func TestRtInspect_FailsWhenAuditTopicSilent(t *testing.T) {
	// "silent-redact" mode: stub redacts (raw PII absent, placeholders
	// present) BUT does NOT publish kit.telemetry.redact.matched. This
	// is the load-bearing negative test: a no-op-but-blanking redactor
	// would pass arm B vacuously without arm C's audit-topic assertion.
	spec := inspectSpec()
	got := runInspectWith(t, spec, "silent-redact")
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail; details=%q", got.Status, got.Details)
	}
	if !strings.Contains(got.Details, "audit topic empty") &&
		!strings.Contains(got.Details, "kit.telemetry.redact.matched") {
		t.Fatalf("details = %q, want it to flag the silent audit topic",
			got.Details)
	}
}

// TestIsObjectOrArray exercises the helper directly. Edge cases
// matter for the (b) "not a primitive" check; table-driven keeps the
// coverage explicit.
func TestIsObjectOrArray(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"whitespace-only", "   \n\t", false},
		{"object", `{"k":"v"}`, true},
		{"object-empty", `{}`, true},
		{"array", `[1,2,3]`, true},
		{"array-empty", `[]`, true},
		{"primitive-true", `true`, false},
		{"primitive-null", `null`, false},
		{"primitive-number", `123`, false},
		{"primitive-string", `"hi"`, false},
		{"malformed", `{not json`, false},
		// Leading whitespace must be tolerated — adopter binaries
		// sometimes pretty-print with leading newlines.
		{"leading-ws-object", "  {\n  \"k\":1\n}", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isObjectOrArray(tc.in)
			if got != tc.want {
				t.Fatalf("isObjectOrArray(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestFilterByTopic locks the audit-topic filter behavior. Sanity:
// missing-topic events are skipped (not counted), exact-match string
// equality, non-matching topics pass through filtered out.
func TestFilterByTopic(t *testing.T) {
	events := []map[string]any{
		{"topic": "kit.telemetry.redact.matched", "n": float64(1)},
		{"topic": "kit.telemetry.invocation.emitted", "n": float64(2)},
		{"topic": "kit.telemetry.redact.matched", "n": float64(3)},
		{"n": float64(4)}, // no topic
		{"topic": 123},    // non-string topic
	}
	got := filterByTopic(events, "kit.telemetry.redact.matched")
	if len(got) != 2 {
		t.Fatalf("filterByTopic len = %d, want 2; got %v", len(got), got)
	}
	for _, ev := range got {
		if ev["topic"] != "kit.telemetry.redact.matched" {
			t.Fatalf("filterByTopic returned wrong topic: %v", ev["topic"])
		}
	}
}

// TestIntersectCanonical pins the (b) arm's behavior: declared
// subcommands outside the canonical set are dropped; missing canonical
// entries don't appear (the static check catches those).
func TestIntersectCanonical(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{}},
		{
			"all-canonical",
			[]string{"status", "enable", "disable", "reset", "inspect"},
			[]string{"status", "enable", "disable", "reset", "inspect"},
		},
		{
			"subset",
			[]string{"status", "inspect"},
			[]string{"status", "inspect"},
		},
		{
			"non-canonical-dropped",
			[]string{"status", "fancy", "inspect"},
			[]string{"status", "inspect"},
		},
		{
			"preserves-declared-order",
			[]string{"inspect", "status", "enable"},
			[]string{"inspect", "status", "enable"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := intersectCanonical(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("intersectCanonical(%v) len = %d, want %d (got %v)",
					tc.in, len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("intersectCanonical(%v)[%d] = %q, want %q",
						tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestSnippet ensures Details messages don't blow past the screen-
// width budget on adversarial stdout dumps. 80 char cap is the
// runtime_inspect.go contract.
func TestSnippet(t *testing.T) {
	short := "small payload"
	if got := snippet(short); got != short {
		t.Fatalf("snippet(short) = %q, want %q", got, short)
	}
	long := strings.Repeat("x", 200)
	got := snippet(long)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("snippet(long) lacks ellipsis: %q", got)
	}
	if len(got) > 83 { // 80 chars + "..."
		t.Fatalf("snippet(long) len = %d, want <= 83", len(got))
	}
}

// TestAggregate covers the pass/fail dispatch — empty details should
// always produce a pass; non-empty should produce fail with the
// suggestion populated.
func TestAggregate(t *testing.T) {
	if got := aggregate(FactorConsentingTelemetry, nil); got.Status != "pass" {
		t.Fatalf("aggregate(nil).Status = %q, want pass", got.Status)
	}
	got := aggregate(FactorConsentingTelemetry, []string{"a", "b"})
	if got.Status != "fail" {
		t.Fatalf("aggregate([a,b]).Status = %q, want fail", got.Status)
	}
	if !strings.Contains(got.Details, "a") || !strings.Contains(got.Details, "b") {
		t.Fatalf("aggregate details = %q, want substrings a and b", got.Details)
	}
	if got.Suggestion == "" {
		t.Fatalf("aggregate fail.Suggestion empty")
	}
}

// Sanity-belt: ensure the stub builds + emits the audit topic in
// "normal" mode by invoking it directly, parsing the bus JSONL, and
// confirming the topic is present. Without this, a regression in the
// stub itself could mask all the assertions in the main tests.
func TestRtInspect_StubAuditPublishSmoke(t *testing.T) {
	bin := ensureInspectStubBuilt(t)
	e := newRTEnv(t)
	if err := e.SeedConsent("granted", "test", 1); err != nil {
		t.Fatalf("seed consent: %v", err)
	}
	e.SetEnv("KIT_TELEMETRY_TEST_INJECT", `{"contact":"bob@x.io"}`)
	e.SetEnv("STUB_INSPECT", "normal")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, code := e.Run(ctx, bin, "telemetry", "inspect", "--format", "json")
	if code != 0 {
		t.Fatalf("stub inspect exit = %d, want 0", code)
	}
	events, _ := e.PollEvents(1, 500*time.Millisecond)
	hit := filterByTopic(events, "kit.telemetry.redact.matched")
	if len(hit) == 0 {
		t.Fatalf("expected >=1 kit.telemetry.redact.matched event from stub; got events=%v", events)
	}
	// Spot-check the rule_id field — adopters who read the audit
	// stream will key off this; if the stub drifts, the failure
	// message should be clear.
	first := hit[0]
	rid, _ := first["rule_id"].(string)
	if rid != "email" {
		// Marshal back for a readable failure message.
		b, _ := json.Marshal(first)
		t.Fatalf("first audit event rule_id = %q, want email; ev=%s", rid, b)
	}
}
