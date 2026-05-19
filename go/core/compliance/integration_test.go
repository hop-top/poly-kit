//go:build integration

// Package compliance_test, integration build tag.
//
// End-to-end compliance check against the real `examples/spaced`
// binary. This test is the GREEN-LIGHT verification for the
// kit-telemetry-compliance track: build spaced, flip its
// toolspec's `telemetry.enabled` from false to true (in a tmp
// copy — the source toolspec stays at `enabled: false`), then run
// compliance.Run end-to-end and assert against the observed score.
//
// Run with:
//
//	go test -tags=integration -race -count=1 ./go/core/compliance/...
//
// The default `go test ./...` run skips this file because of the
// build tag — building spaced takes ~10s and the test depends on
// the local source tree layout.
//
// Reality vs target (recorded 2026-05-19):
//
//   - Target per ADR-0037: 13/13 on the opt-in spaced binary.
//   - Observed: 9/13. The shortfall is structural and well-understood:
//
//   - F9 ObservableOps: skip — "runtime check only", but the
//     check is not yet exercised here (informational gap, not
//     spaced's fault).
//   - F10 Provenance: skip — spaced's read commands emit table
//     output by default, not a JSON object with a `_meta`
//     field. Adding `_meta` to spaced's output is a separate
//     adopter task; the compliance pkg correctly reports skip
//     when the output isn't a JSON object.
//   - F12 AuthLifecycle: skip — spaced does not declare
//     `state_introspection.auth_commands`. Spaced has no auth
//     surface; skip is the correct semantics.
//   - F13 ConsentingTelemetry: FAIL — spaced declares
//     `consent_subcommands: [status, enable, disable, reset,
//     inspect]` but its `telemetry` command only exposes
//     `telemetry get <mission>`. The canonical consent
//     subcommands are not yet wired in. This is the kit-consent
//     adopter integration gap and is expected pending the
//     adoption work tracked separately (per spaced/README.md L191).
//
// Additionally, the F13 runtime sub-checks (rtConsentingTelemetryInspect,
// rtConsentingTelemetryKillSwitch, rtConsentingTelemetryPrompt)
// exist in the package but are NOT yet wired into runRuntimeChecks.
// The F13 result here reflects the static check only.
//
// The test is therefore a baseline lock-in: it documents what
// `compliance.Run` returns for the real spaced binary today
// (9/13), names every shortfall, and provides a tripwire that
// fires when either side of the contract drifts. When the runtime
// wiring lands and spaced exposes the canonical telemetry
// subcommands, flip wantScore to 13 and wantF13Status to "pass".

package compliance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/core/compliance"
)

// wantScore is the baseline score recorded against the current
// spaced source. Bump this (and tighten the assertions below) as
// spaced closes adopter gaps.
const wantScore = 9

// wantTotal is the F13-opt-in denominator per ADR-0037: 13 when
// telemetry.enabled is true.
const wantTotal = 13

// TestE2E_SpacedCompliance builds the real spaced binary, flips
// the toolspec's telemetry block to enabled=true in a tmp copy,
// runs compliance.Run end-to-end, and asserts against the recorded
// baseline.
func TestE2E_SpacedCompliance(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Build spaced.
	spacedBin := filepath.Join(tmpDir, "spaced")
	// The compliance package lives at hops/main/go/core/compliance;
	// examples/spaced/go is at hops/main/examples/spaced/go — four
	// levels up + traverse.
	spacedSrc, err := filepath.Abs(filepath.Join("..", "..", "..", "examples", "spaced", "go"))
	if err != nil {
		t.Fatalf("resolve spaced source: %v", err)
	}
	if _, err := os.Stat(spacedSrc); err != nil {
		t.Skipf("spaced source not found at %s: %v", spacedSrc, err)
	}
	build := exec.Command("go", "build", "-buildvcs=false", "-o", spacedBin, spacedSrc)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build spaced: %v\n%s", err, out)
	}

	// 2. Flip the toolspec to telemetry.enabled=true in a tmp copy.
	srcSpec, err := filepath.Abs(filepath.Join("..", "..", "..", "examples", "spaced", "spaced.toolspec.yaml"))
	if err != nil {
		t.Fatalf("resolve toolspec: %v", err)
	}
	raw, err := os.ReadFile(srcSpec)
	if err != nil {
		t.Fatalf("read toolspec: %v", err)
	}
	flipped := strings.Replace(string(raw), "enabled: false", "enabled: true", 1)
	if flipped == string(raw) {
		t.Fatal("expected `enabled: false` token in toolspec but found none — " +
			"the test assumes a single-line literal flip site")
	}
	tmpSpec := filepath.Join(tmpDir, "spaced.toolspec.yaml")
	if err := os.WriteFile(tmpSpec, []byte(flipped), 0o600); err != nil {
		t.Fatalf("write tmp toolspec: %v", err)
	}

	// 3. Run compliance end-to-end. Signature is
	// Run(binaryPath, toolspecPath string) (*Report, error) — no
	// ctx parameter as of compliance.go's current Run definition.
	report, err := compliance.Run(spacedBin, tmpSpec)
	if err != nil {
		t.Fatalf("compliance.Run: %v", err)
	}
	if report == nil {
		t.Fatal("compliance.Run returned nil report without error")
	}

	// 4. Log every result up front so failure diagnostics are
	// always complete (vs only-on-failure logging that hides the
	// passing factors when one fails for an unrelated reason).
	for _, r := range report.Results {
		t.Logf("F%d %s: %s — %s", r.Factor, r.Name, r.Status, r.Details)
	}
	t.Logf("score: %d/%d (opt-in denominator)", report.Score, report.Total)

	// 5. Assert denominator. F13 opt-in must bump Total to 13
	// per ADR-0037; if Total comes back as 12, the toolspec flip
	// didn't take effect (e.g. parser regression) and the rest
	// of the assertions become meaningless.
	if report.Total != wantTotal {
		t.Fatalf("Total = %d, want %d (opt-in denominator). "+
			"This suggests the telemetry.enabled=true flip did not "+
			"register with the toolspec parser.",
			report.Total, wantTotal)
	}

	// 6. Assert score baseline. Bump wantScore as adopter gaps
	// close; a delta in either direction is informative.
	if report.Score != wantScore {
		t.Errorf("Score = %d/%d, want %d/%d. "+
			"Drift from baseline — see ADR-0037 + integration_test.go "+
			"header for the per-factor expectations.",
			report.Score, report.Total, wantScore, wantTotal)
	}

	// 7. Pinpoint F13. The status here is the load-bearing
	// signal for the kit-telemetry-compliance track — record it
	// explicitly so future bumps catch sign flips.
	var f13 *compliance.CheckResult
	for i := range report.Results {
		if report.Results[i].Factor == compliance.FactorConsentingTelemetry {
			f13 = &report.Results[i]
			break
		}
	}
	if f13 == nil {
		t.Fatal("F13 ConsentingTelemetry not in report — Run must " +
			"emit a row per factor per ADR-0037")
	}

	// Today's baseline: F13 fails because spaced's `telemetry`
	// command does not expose the canonical consent subcommands.
	// Flip wantF13Status to "pass" once kit-consent lands and
	// spaced wires telemetry status/enable/disable/reset/inspect.
	const wantF13Status = "fail"
	if f13.Status != wantF13Status {
		t.Errorf("F13 status = %q, want %q. Details: %s",
			f13.Status, wantF13Status, f13.Details)
	}

	// 8. Sanity: F13 details mention the missing consent subcommand
	// mapping. If the wording drifts that's fine — only the
	// substring "consent_subcommands" is locked, since adopters
	// grep error output for that token to navigate the fix.
	if !strings.Contains(f13.Details, "consent_subcommands") {
		t.Errorf("F13 details %q should mention `consent_subcommands` "+
			"so adopters can navigate the fix", f13.Details)
	}

	// 9. Vacuity note: F13 runtime sub-checks (inspect, killswitch,
	// prompt) are not yet routed through runRuntimeChecks. When the
	// runtime wiring lands, this test's F13 result becomes the static
	// + runtime aggregate; until then it's static-only. The test does
	// NOT need to change for that transition — the
	// aggregate-row-per-factor contract is stable and `mergeResults`
	// overrides static skips with runtime checks.
}
