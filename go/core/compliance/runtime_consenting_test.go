package compliance

// Tests for T-0704: rtConsentingTelemetry aggregator + Run/RunRuntime
// wiring of the three F13 runtime sub-checks (kill-switch, inspect,
// prompt). Internal-test package so we can exercise the unexported
// aggregator + helper surface directly.
//
// End-to-end coverage of the FULL three-sub-check aggregation against
// a single binary is intentionally NOT attempted here: no stub in
// testdata/ implements all three protocols simultaneously (the
// kill-switch, inspect, and prompt stubs each speak a different
// shape). Cross-stub aggregation lives in the per-sub-check tests
// (runtime_killswitch_test.go, runtime_inspect_test.go,
// runtime_prompt_test.go) where each stub gets the right wiring.
// Here we focus on aggregator semantics + the orchestration in
// runRuntimeChecks via skip-path Run() calls.

import (
	"strings"
	"testing"
)

func TestAggregateConsentingTelemetry_AllPass(t *testing.T) {
	r1 := pass(FactorConsentingTelemetry, "kill switch ok")
	r2 := pass(FactorConsentingTelemetry, "inspect ok")
	r3 := pass(FactorConsentingTelemetry, "prompt ok")
	got := aggregateConsentingTelemetry(r1, r2, r3)
	if got.Status != "pass" {
		t.Fatalf("status = %q, want pass; details=%q", got.Status, got.Details)
	}
	if got.Factor != FactorConsentingTelemetry {
		t.Fatalf("factor = %v, want FactorConsentingTelemetry", got.Factor)
	}
}

func TestAggregateConsentingTelemetry_AnyFail(t *testing.T) {
	good := pass(FactorConsentingTelemetry, "ok")
	bad := fail(FactorConsentingTelemetry, "kill-switch leaked DO_NOT_TRACK", "fix it")
	got := aggregateConsentingTelemetry(good, bad, good)
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail; details=%q", got.Status, got.Details)
	}
	if !strings.Contains(got.Details, "kill-switch leaked") {
		t.Fatalf("details = %q, want it to include the failed sub-check reason", got.Details)
	}
	if got.Suggestion == "" {
		t.Fatalf("expected non-empty Suggestion on fail; got empty")
	}
}

func TestAggregateConsentingTelemetry_MultipleFails(t *testing.T) {
	f1 := fail(FactorConsentingTelemetry, "kill-switch leak", "x")
	f2 := fail(FactorConsentingTelemetry, "inspect missing", "y")
	good := pass(FactorConsentingTelemetry, "ok")
	got := aggregateConsentingTelemetry(f1, good, f2)
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail", got.Status)
	}
	for _, want := range []string{"kill-switch leak", "inspect missing"} {
		if !strings.Contains(got.Details, want) {
			t.Fatalf("details = %q, want substring %q", got.Details, want)
		}
	}
}

func TestAggregateConsentingTelemetry_AllSkipDedup(t *testing.T) {
	r := skip(FactorConsentingTelemetry, "binary does not opt into telemetry")
	got := aggregateConsentingTelemetry(r, r, r)
	if got.Status != "skip" {
		t.Fatalf("status = %q, want skip; details=%q", got.Status, got.Details)
	}
	// Dedup: identical messages collapse to one occurrence.
	if n := strings.Count(got.Details, "binary does not opt into telemetry"); n != 1 {
		t.Fatalf("dedup: occurrence count = %d, want 1; details=%q", n, got.Details)
	}
}

func TestAggregateConsentingTelemetry_AllSkipDifferentReasons(t *testing.T) {
	a := skip(FactorConsentingTelemetry, "no read command available")
	b := skip(FactorConsentingTelemetry, "no test-inject hook")
	c := skip(FactorConsentingTelemetry, "binary does not opt into telemetry")
	got := aggregateConsentingTelemetry(a, b, c)
	if got.Status != "skip" {
		t.Fatalf("status = %q, want skip", got.Status)
	}
	for _, want := range []string{"no read command", "no test-inject hook", "binary does not opt"} {
		if !strings.Contains(got.Details, want) {
			t.Fatalf("details = %q, want substring %q", got.Details, want)
		}
	}
}

// TestAggregateConsentingTelemetry_MixedPassSkip — ADR-0037 §5
// partial-instrumentation case: pass dominates over skip so the
// aggregate signals "what we could verify, verified clean".
func TestAggregateConsentingTelemetry_MixedPassSkip(t *testing.T) {
	good := pass(FactorConsentingTelemetry, "ok")
	skipR := skip(FactorConsentingTelemetry, "no test-inject hook")
	got := aggregateConsentingTelemetry(good, skipR, good)
	if got.Status != "pass" {
		t.Fatalf("status = %q, want pass (mixed pass+skip → pass); details=%q",
			got.Status, got.Details)
	}
}

// TestRtConsentingTelemetry_SkipsWhenNotOptedIn verifies the early
// skip in the runRuntimeChecks orchestration site (no spec.Telemetry
// block → skip without spawning any sub-check).
func TestRtConsentingTelemetry_SkipsWhenNotOptedIn(t *testing.T) {
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
	// stubBinaryPath is built by TestMain in runtime_killswitch_test.go.
	got := rtConsentingTelemetry(stubBinaryPath, spec)
	if got.Status != "skip" {
		t.Fatalf("status = %q, want skip; details=%q", got.Status, got.Details)
	}
	if got.Factor != FactorConsentingTelemetry {
		t.Fatalf("factor = %v, want FactorConsentingTelemetry", got.Factor)
	}
}

// TestRunRuntimeChecks_IncludesF13Slot confirms that runRuntimeChecks
// emits an F13 row (T-0704's wiring). The exact pass/fail outcome
// depends on the binary's behavior, but the SLOT must exist —
// previously the function returned 10 results, now it returns 11.
func TestRunRuntimeChecks_IncludesF13Slot(t *testing.T) {
	idempotent := true
	// Non-opt-in spec → F13 will skip but the slot must still be
	// present so the orchestration in compliance.go can merge it.
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
	}
	results := runRuntimeChecks(stubBinaryPath, spec)
	var f13 *CheckResult
	for i := range results {
		if results[i].Factor == FactorConsentingTelemetry {
			f13 = &results[i]
			break
		}
	}
	if f13 == nil {
		t.Fatalf("F13 row missing from runRuntimeChecks output; got %d results: %+v",
			len(results), results)
	}
	if f13.Status != "skip" {
		t.Fatalf("F13 status = %q, want skip (non-opt-in spec); details=%q",
			f13.Status, f13.Details)
	}
}

// TestJoinUnique pins the dedup helper used by the aggregator's
// all-skip path. Order preservation matters — adopters reading the
// joined Details should see scenarios in the order the sub-checks
// reported them.
func TestJoinUnique(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		sep  string
		want string
	}{
		{"empty", nil, "; ", ""},
		{"single", []string{"a"}, "; ", "a"},
		{"all-unique", []string{"a", "b", "c"}, "; ", "a; b; c"},
		{"all-dup", []string{"a", "a", "a"}, "; ", "a"},
		{"mixed-preserves-first-order", []string{"b", "a", "b", "c", "a"}, "; ", "b; a; c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := joinUnique(tc.in, tc.sep)
			if got != tc.want {
				t.Fatalf("joinUnique(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
