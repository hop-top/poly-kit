package compliance

// Tests for the per-mode autocorrect arm of the Factor-4 runtime check.
//
// Each obligation gets its own stub, because the whole point of the arm is
// that a tool can be correct in one mode and wrong in another: a stub that
// behaved identically under every policy would prove nothing.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// autocorrectStub writes an executable shell script that branches on
// KIT_AUTOCORRECT, so one binary can answer the probe differently per
// mode the way a real tool does.
//
// Each of offBody/readBody is "<stderr text>|<exit code>"; the script
// echoes the text to stderr and exits with the code.
func autocorrectStub(t *testing.T, offStderr string, offCode int, readStderr string, readCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub is POSIX-only")
	}
	path := filepath.Join(t.TempDir(), "stub")
	script := `#!/bin/sh
case "$KIT_AUTOCORRECT" in
read)
  cat >&2 <<'READ_EOF'
` + readStderr + `
READ_EOF
  exit ` + itoa(readCode) + `
  ;;
*)
  cat >&2 <<'OFF_EOF'
` + offStderr + `
OFF_EOF
  exit ` + itoa(offCode) + `
  ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

const usageEnvelope = `{"code":"USAGE","message":"unknown flag --formt","suggested_fix":"--format","exit_code":2}`

// autocorrectSpec is the minimum toolspec the probe needs: one command
// that findReadCommand will pick (an output schema plus idempotent), since
// a correction is only legitimate on a read leaf.
func autocorrectSpec() *toolspecYAML {
	yes := true
	return &toolspecYAML{
		Name:          "stub",
		SchemaVersion: "1.0",
		Commands: []commandYAML{{
			Name:         "list",
			Contract:     &contractYAML{Idempotent: &yes, SideEffects: []string{"read"}},
			OutputSchema: &outSchemaYAML{Format: "json"},
		}},
	}
}

// TestRtContractsErrorsAutocorrect_NoSupportSkips is the honest tool
// with no autocorrect at all: non-zero in every mode, so the arm skips
// rather than failing it. Not having the feature is not a violation.
func TestRtContractsErrorsAutocorrect_NoSupportSkips(t *testing.T) {
	bin := autocorrectStub(t, usageEnvelope, 2, usageEnvelope, 2)
	got := rtContractsErrorsAutocorrect(bin, autocorrectSpec())
	if got.Status != "skip" {
		t.Errorf("status = %q, want skip (no autocorrect support)\n%+v", got.Status, got)
	}
}

// TestRtContractsErrorsAutocorrect_NoReadCommandSkips: with nothing safe
// to probe on, the arm measures nothing rather than guessing.
func TestRtContractsErrorsAutocorrect_NoReadCommandSkips(t *testing.T) {
	bin := autocorrectStub(t, usageEnvelope, 2, usageEnvelope, 2)
	got := rtContractsErrorsAutocorrect(bin, &toolspecYAML{Name: "stub"})
	if got.Status != "skip" {
		t.Errorf("status = %q, want skip\n%+v", got.Status, got)
	}
	if !strings.Contains(got.Details, "no read command") {
		t.Errorf("Details does not name the reason: %q", got.Details)
	}
}

// TestRtContractsErrorsAutocorrect_CorrectingByDefaultFails is the
// regression the arm exists to catch: a tool that quietly fixes flags
// with no opt-in has broken the non-zero-exit contract for every script
// that was relying on the failure.
func TestRtContractsErrorsAutocorrect_CorrectingByDefaultFails(t *testing.T) {
	bin := autocorrectStub(t, `{"code":"OK","corrected_from":"--formt"}`, 0,
		`{"code":"OK","corrected_from":"--formt"}`, 0)
	got := rtContractsErrorsAutocorrect(bin, autocorrectSpec())
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail (corrected with no policy set)\n%+v", got.Status, got)
	}
	if !strings.Contains(got.Details, "no autocorrect policy") {
		t.Errorf("Details does not name the violation: %q", got.Details)
	}
}

// TestRtContractsErrorsAutocorrect_AppliedWithCorrectedFromPasses is the
// compliant opt-in tool: exit 0 under read, and the envelope says so.
func TestRtContractsErrorsAutocorrect_AppliedWithCorrectedFromPasses(t *testing.T) {
	bin := autocorrectStub(t, usageEnvelope, 2,
		`{"code":"OK","message":"applied flag correction","corrected_from":"--formt","exit_code":0}`, 0)
	got := rtContractsErrorsAutocorrect(bin, autocorrectSpec())
	if got.Status != "pass" {
		t.Errorf("status = %q, want pass\n%+v", got.Status, got)
	}
}

// TestRtContractsErrorsAutocorrect_AppliedWithoutCorrectedFromFails is
// the arm's own obligation. The run silently became a different run, and
// a --format json caller has no way to find out.
func TestRtContractsErrorsAutocorrect_AppliedWithoutCorrectedFromFails(t *testing.T) {
	bin := autocorrectStub(t, usageEnvelope, 2,
		`{"code":"OK","message":"applied flag correction","exit_code":0}`, 0)
	got := rtContractsErrorsAutocorrect(bin, autocorrectSpec())
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail\n%+v", got.Status, got)
	}
	if !strings.Contains(got.Details, correctedFromField) {
		t.Errorf("Details does not name the missing field: %q", got.Details)
	}
}

// TestRtContractsErrorsAutocorrect_PlaintextNoticeCounts: the obligation
// is that the correction is DECLARED, not that it is declared as JSON. A
// tool whose default format is plaintext still satisfies it.
func TestRtContractsErrorsAutocorrect_PlaintextNoticeCounts(t *testing.T) {
	bin := autocorrectStub(t, usageEnvelope, 2,
		"OK: applied flag correction\nCorrected from: --formt", 0)
	got := rtContractsErrorsAutocorrect(bin, autocorrectSpec())
	if got.Status != "pass" {
		t.Errorf("status = %q, want pass\n%+v", got.Status, got)
	}
}

// --- the aggregation ------------------------------------------------------

// TestAggregateContractsErrors_WarnSurvivesASkip is the precedence rule
// the F13 aggregator does not need: the base sub-check's warn must not be
// collapsed into a pass just because the autocorrect arm had nothing to
// measure. That would hide the fix-less-envelope finding on exactly the
// tools that have not adopted autocorrect — which is most of them.
func TestAggregateContractsErrors_WarnSurvivesASkip(t *testing.T) {
	base := warn(FactorContractsErrors, "structured error carries no recovery guidance", "populate suggested_fix")
	arm := skip(FactorContractsErrors, "binary does not apply flag corrections")
	got := aggregateContractsErrors(base, arm)
	if got.Status != "warn" {
		t.Errorf("status = %q, want warn\n%+v", got.Status, got)
	}
	if got.Details != base.Details {
		t.Errorf("the warn's own Details was lost: %q", got.Details)
	}
}

// TestAggregateContractsErrors_FailBeatsEverything: a hard violation in
// either sub-check is the row's status.
func TestAggregateContractsErrors_FailBeatsEverything(t *testing.T) {
	got := aggregateContractsErrors(
		pass(FactorContractsErrors, "fine"),
		fail(FactorContractsErrors, "corrected with no policy set", "keep it off by default"),
	)
	if got.Status != "fail" {
		t.Errorf("status = %q, want fail\n%+v", got.Status, got)
	}
	if !strings.Contains(got.Details, "no policy set") {
		t.Errorf("the failing Details was lost: %q", got.Details)
	}
}

// TestAggregateContractsErrors_AllSkipSkips keeps the row honest when
// neither sub-check could measure anything.
func TestAggregateContractsErrors_AllSkipSkips(t *testing.T) {
	got := aggregateContractsErrors(
		skip(FactorContractsErrors, "same reason"),
		skip(FactorContractsErrors, "same reason"),
	)
	if got.Status != "skip" {
		t.Errorf("status = %q, want skip\n%+v", got.Status, got)
	}
	if got.Details != "same reason" {
		t.Errorf("identical skip reasons were not deduplicated: %q", got.Details)
	}
}

// TestAggregateContractsErrors_BothPassPasses is the compliant tool.
func TestAggregateContractsErrors_BothPassPasses(t *testing.T) {
	got := aggregateContractsErrors(
		pass(FactorContractsErrors, "structured"),
		pass(FactorContractsErrors, "declared"),
	)
	if got.Status != "pass" {
		t.Errorf("status = %q, want pass\n%+v", got.Status, got)
	}
}

// --- corrected_from detection --------------------------------------------

func TestCorrectionDeclared(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"json field", `{"code":"OK","corrected_from":"--formt"}`, true},
		{"yaml field", "code: OK\ncorrected_from: --formt\n", true},
		{"plaintext line", "OK: applied\nCorrected from: --formt\n", true},
		{
			name: "second document on a noisy stream",
			in:   "{\"level\":\"info\"}\n{\"corrected_from\":\"--forma\"}\n",
			want: true,
		},
		{"absent", `{"code":"OK","message":"applied"}`, false},
		{"present but blank", `{"corrected_from":""}`, false},
		{"present but whitespace", `{"corrected_from":"   "}`, false},
		{"empty stream", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := correctionDeclared(tt.in); got != tt.want {
				t.Errorf("correctionDeclared(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
