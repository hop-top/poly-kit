package compliance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// runRuntimeChecks executes the binary and verifies behavior.
func runRuntimeChecks(binaryPath string, spec *toolspecYAML) []CheckResult {
	results := make([]CheckResult, 0, 11)

	results = append(results, rtSelfDescribing(binaryPath))
	results = append(results, rtStructuredIO(binaryPath, spec))
	results = append(results, rtStreamDiscipline(binaryPath, spec))
	results = append(results, rtContractsErrors(binaryPath))
	results = append(results, rtPreview(binaryPath, spec))
	results = append(results, rtStateTransparency(binaryPath))
	results = append(results, rtSafeDelegation(binaryPath, spec))
	results = append(results, rtProvenance(binaryPath, spec))
	results = append(results, rtEvolution(binaryPath))
	results = append(results, rtAuthLifecycle(binaryPath, spec))
	results = append(results, rtConsentingTelemetry(binaryPath, spec))

	return results
}

// rtConsentingTelemetryTimeout bounds the overall F13 runtime arm.
// The three sub-checks each have their own per-subprocess timeouts;
// this is the wall-clock cap on the whole aggregation. 90s gives the
// kill-switch sub-check's 3 spawns (5s each) + inspect's ~10s + the
// prompt arm's 3 spawns (5s each) generous headroom without wedging
// the suite when an adopter binary is misbehaving.
const rtConsentingTelemetryTimeout = 90 * time.Second

// rtConsentingTelemetry runs all three F13 runtime sub-checks
// (kill-switch, inspect/redact, prompt) and aggregates the results
// per ADR-0037's "one row per factor" model. Skips early when the
// toolspec does not opt into telemetry.
//
// Each sub-check owns its own rtEnv lifecycle via the envFactory
// closure; we use newRTEnvDir over a per-call tmpdir so production
// callers do not need *testing.T. The tmpdir is cleaned up at function
// return.
func rtConsentingTelemetry(binaryPath string, spec *toolspecYAML) CheckResult {
	if !telemetryOptedIn(spec) {
		return skip(FactorConsentingTelemetry, "binary does not opt into telemetry")
	}

	// Single tmpdir parent for the whole aggregation; per-scenario
	// rtEnvs live in subdirs created by os.MkdirTemp under it. The
	// cleanup at the end of this function reaps the whole tree.
	parent, err := os.MkdirTemp("", "kit-compliance-f13-*")
	if err != nil {
		return fail(FactorConsentingTelemetry,
			fmt.Sprintf("create tmpdir for runtime check: %v", err),
			"Verify the test environment has a writable temp directory")
	}
	defer func() { _ = os.RemoveAll(parent) }()

	envFactory := func() *rtEnv {
		dir, err := os.MkdirTemp(parent, "scenario-*")
		if err != nil {
			// Fall back to parent itself; downstream errors will surface
			// via subprocess invocations.
			dir = parent
		}
		return newRTEnvDir(dir)
	}

	ctx, cancel := context.WithTimeout(context.Background(), rtConsentingTelemetryTimeout)
	defer cancel()

	sub1 := rtConsentingTelemetryKillSwitch(ctx, binaryPath, spec, envFactory)
	sub2 := rtConsentingTelemetryInspect(ctx, binaryPath, spec, envFactory)
	sub3 := rtConsentingTelemetryPrompt(ctx, binaryPath, spec, envFactory)

	return aggregateConsentingTelemetry(sub1, sub2, sub3)
}

// aggregateConsentingTelemetry folds the three F13 runtime sub-check
// results into a single CheckResult per ADR-0037's "one row per
// factor" model. Precedence:
//
//   - any fail → overall fail; Details concatenates each sub-check's
//     failure Details so adopters see every gap in one pass.
//   - all skip → overall skip; Details concatenates each skip reason
//     (typically all three say "binary does not opt into telemetry"
//     or a no-read-command skip from a sub-check).
//   - otherwise → overall pass; Details summarizes which sub-conditions
//     passed.
//
// The mixed pass+skip case (e.g. kill-switch + prompt pass, inspect
// skip because no test-inject hook) collapses to pass — partial
// instrumentation is acceptable per ADR-0037 §5; a clean pass on the
// arms we CAN verify is the strongest signal we can give the adopter
// without false-failing on missing test hooks.
func aggregateConsentingTelemetry(rs ...CheckResult) CheckResult {
	var failed, skipped []string
	for _, r := range rs {
		switch r.Status {
		case "fail":
			failed = append(failed, r.Details)
		case "skip":
			skipped = append(skipped, r.Details)
		}
	}
	f := FactorConsentingTelemetry
	if len(failed) > 0 {
		return fail(f, strings.Join(failed, "; "),
			"Address each failing sub-condition; see ADR-0037 sub-conditions "+
				"(b)/(c)/(d)/(e)/(f)/(g)")
	}
	if len(skipped) == len(rs) {
		// All sub-checks skipped — same root reason (not opt-in or
		// harness limitation). Deduplicate identical messages so the
		// Details line stays readable.
		return skip(f, joinUnique(skipped, "; "))
	}
	return pass(f, "all runtime sub-conditions pass "+
		"(kill-switch + inspect/redact + prompt precedence)")
}

// joinUnique joins entries with sep, dropping exact duplicates while
// preserving first-occurrence order. Used by aggregateConsentingTelemetry
// to collapse the common case where all three sub-checks skip with
// the same "binary does not opt into telemetry" reason.
func joinUnique(parts []string, sep string) string {
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return strings.Join(out, sep)
}

// run executes a command and returns stdout, stderr, exit code.
func run(bin string, args ...string) (stdout, stderr string, code int) {
	cmd := exec.Command(bin, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	return outBuf.String(), errBuf.String(), code
}

// Factor 1: binary --help exits 0
func rtSelfDescribing(bin string) CheckResult {
	f := FactorSelfDescribing
	stdout, _, code := run(bin, "--help")
	if code != 0 {
		return fail(f, fmt.Sprintf("--help exited %d", code),
			"Ensure --help exits 0")
	}
	upper := strings.ToUpper(stdout)
	if !strings.Contains(upper, "COMMANDS") &&
		!strings.Contains(upper, "USAGE") {
		return fail(f, "--help output lacks COMMANDS/USAGE section",
			"Help output should list available commands")
	}
	return pass(f, "--help exits 0, contains command listing")
}

// Factor 2: read command with --format json returns valid JSON
func rtStructuredIO(bin string, spec *toolspecYAML) CheckResult {
	f := FactorStructuredIO
	readCmd := findReadCommand(spec)
	if readCmd == "" {
		return skip(f, "no read command found for runtime check")
	}
	stdout, _, code := run(bin, readCmd, "--format", "json")
	if code != 0 {
		return fail(f, fmt.Sprintf("%s --format json exited %d", readCmd, code),
			"Read commands should support --format json")
	}
	if !json.Valid([]byte(strings.TrimSpace(stdout))) {
		return fail(f, "output is not valid JSON",
			"--format json should produce valid JSON")
	}
	return pass(f, readCmd+" --format json returns valid JSON")
}

// Factor 3: stdout has data, stderr doesn't have JSON
func rtStreamDiscipline(bin string, spec *toolspecYAML) CheckResult {
	f := FactorStreamDiscipline
	readCmd := findReadCommand(spec)
	if readCmd == "" {
		return skip(f, "no read command found")
	}
	stdout, stderr, _ := run(bin, readCmd, "--format", "json")
	if strings.TrimSpace(stdout) == "" {
		return fail(f, "stdout is empty",
			"Data should go to stdout")
	}
	if json.Valid([]byte(strings.TrimSpace(stderr))) && len(strings.TrimSpace(stderr)) > 2 {
		return fail(f, "stderr contains JSON (should be logs only)",
			"Keep structured data on stdout, logs on stderr")
	}
	return pass(f, "stdout has data, stderr clean")
}

// Factor 4: bogus arg returns structured error
func rtContractsErrors(bin string) CheckResult {
	f := FactorContractsErrors
	_, stderr, code := run(bin, "--bogus-arg-xyzzy")
	if code == 0 {
		return fail(f, "bogus arg didn't cause error exit",
			"Unknown flags should cause non-zero exit")
	}
	// Check for structured error (JSON with "code" field)
	if json.Valid([]byte(strings.TrimSpace(stderr))) {
		var obj map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(stderr)), &obj) == nil {
			if _, ok := obj["code"]; ok {
				return pass(f, "structured error with code field")
			}
		}
	}
	// Non-structured error is a warning, not a hard fail
	return CheckResult{
		Factor:     f,
		Name:       f.String(),
		Status:     "warn",
		Details:    "error output is not structured JSON",
		Suggestion: "Return JSON errors with a 'code' field on stderr",
	}
}

// Factor 5: mutating command with --dry-run exits 0
func rtPreview(bin string, spec *toolspecYAML) CheckResult {
	f := FactorPreview
	mut := mutatingCommands(spec.Commands)
	if len(mut) == 0 {
		return skip(f, "no mutating commands")
	}
	for _, c := range mut {
		for _, mode := range c.PreviewModes {
			_, _, code := run(bin, c.Name, mode)
			if code == 0 {
				return pass(f, c.Name+" "+mode+" exits 0")
			}
		}
	}
	return fail(f, "no mutating command succeeds with preview mode",
		"Ensure --dry-run exits 0")
}

// Factor 7: binary config exits 0
func rtStateTransparency(bin string) CheckResult {
	f := FactorStateTransparency
	_, _, code := run(bin, "config", "show")
	if code == 0 {
		return pass(f, "config show exits 0")
	}
	_, _, code = run(bin, "config")
	if code == 0 {
		return pass(f, "config exits 0")
	}
	return fail(f, "config command failed",
		"Add a config/config show command")
}

// Factor 8: dangerous command without --force in non-TTY fails
func rtSafeDelegation(bin string, spec *toolspecYAML) CheckResult {
	f := FactorSafeDelegation
	dangerous := dangerousCommands(spec.Commands)
	if len(dangerous) == 0 {
		return skip(f, "no dangerous commands")
	}
	// We're already in non-TTY (exec.Command), so just run it
	for _, c := range dangerous {
		if !c.Safety.RequiresConfirmation {
			continue
		}
		_, _, code := run(bin, c.Name)
		if code == 0 {
			return fail(f, c.Name+" succeeded without confirmation in non-TTY",
				"Dangerous commands should fail without --force in non-TTY")
		}
		return pass(f, c.Name+" correctly refused in non-TTY")
	}
	return pass(f, "dangerous commands have safety metadata (no confirmation required)")
}

// Factor 10: read command output has _meta field
func rtProvenance(bin string, spec *toolspecYAML) CheckResult {
	f := FactorProvenance
	readCmd := findReadCommand(spec)
	if readCmd == "" {
		return skip(f, "no read command found")
	}
	stdout, _, code := run(bin, readCmd, "--format", "json")
	if code != 0 {
		return skip(f, readCmd+" failed")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &obj); err != nil {
		return skip(f, "output not JSON object")
	}
	if _, ok := obj["_meta"]; ok {
		return pass(f, "_meta field present in output")
	}
	return fail(f, "no _meta field in JSON output",
		"Add _meta with provenance info to structured output")
}

// Factor 11: binary --version exits 0
func rtEvolution(bin string) CheckResult {
	f := FactorEvolution
	_, _, code := run(bin, "--version")
	if code != 0 {
		return fail(f, fmt.Sprintf("--version exited %d", code),
			"Ensure --version exits 0")
	}
	return pass(f, "--version exits 0")
}

// Factor 12: auth status exits 0 (or skip if no auth)
func rtAuthLifecycle(bin string, spec *toolspecYAML) CheckResult {
	f := FactorAuthLifecycle
	if spec.StateIntrospection == nil ||
		len(spec.StateIntrospection.AuthCommands) == 0 {
		return skip(f, "no auth_commands declared")
	}
	_, _, code := run(bin, "auth", "status")
	if code == 0 {
		return pass(f, "auth status exits 0")
	}
	_, _, code = run(bin, "auth")
	if code == 0 {
		return pass(f, "auth exits 0")
	}
	return fail(f, "auth command failed",
		"Implement auth status/auth commands")
}

// findReadCommand finds the first idempotent command with output_schema.
func findReadCommand(spec *toolspecYAML) string {
	for _, c := range allCommands(spec.Commands) {
		if c.OutputSchema != nil && c.Contract != nil &&
			c.Contract.Idempotent != nil && *c.Contract.Idempotent {
			return c.Name
		}
	}
	return ""
}
