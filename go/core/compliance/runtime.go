package compliance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	results = append(results, aggregateContractsErrors(
		rtContractsErrors(binaryPath),
		rtContractsErrorsAutocorrect(binaryPath, spec),
	))
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
// per the "one row per factor" model. Skips early when the toolspec
// does not opt into telemetry.
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
// results into a single CheckResult per the "one row per factor"
// model. Precedence:
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
// instrumentation is acceptable; a clean pass on the arms we CAN
// verify is the strongest signal we can give the adopter without
// false-failing on missing test hooks.
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
			"Address each failing sub-condition (b)/(c)/(d)/(e)/(f)/(g)")
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

// runEnv is run with extra environment entries appended, for probes whose
// obligation is stated against a policy the tool reads from the
// environment. The inherited environment is kept: a binary that needs
// PATH or HOME to start at all must still start.
func runEnv(bin string, env []string, args ...string) (stdout, stderr string, code int) {
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code = 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
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

// Factor 4: bogus arg returns structured error carrying a fix
//
// Two separate obligations, checked in order of severity:
//
//  1. Non-zero exit. A rejected flag that exits 0 is the worst outcome —
//     the caller cannot tell the invocation failed. Hard fail.
//  2. A structured envelope that carries recovery guidance. An error whose
//     only content is "unknown flag: --x" is a dead end: the caller has to
//     spend a --help round trip to learn what it should have typed. That
//     round trip is the cost this factor exists to eliminate, so a
//     structured error WITHOUT a fix is only a partial pass.
//
// Both obligations are stated against the SUGGEST-ONLY behavior, which is
// what the probe elicits: it passes an argument no real flag is close to
// and no autocorrect policy of its own, so a tool whose autocorrect is
// off — every tool, by default — is measured exactly as before.
//
// A tool invoked under an autocorrect policy is a different measurement,
// and rtContractsErrorsAutocorrect below makes it. Keeping the two apart
// matters: "exit non-zero on a bad flag" and "exit zero after fixing a
// bad flag" are both correct, and a probe that conflated them would
// either fail a compliant autocorrecting tool or stop noticing the
// exit-0 failure it exists to catch.
func rtContractsErrors(bin string) CheckResult {
	f := FactorContractsErrors
	_, stderr, code := run(bin, "--format", "json", "--bogus-arg-xyzzy")
	if code == 0 {
		return fail(f, "bogus arg didn't cause error exit",
			"Unknown flags should cause non-zero exit")
	}
	// Check for structured error (JSON with "code" field)
	if json.Valid([]byte(strings.TrimSpace(stderr))) {
		var obj map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(stderr)), &obj) == nil {
			if _, ok := obj["code"]; ok {
				if errorCarriesFix(obj) {
					return pass(f, "structured error with code field and recovery guidance")
				}
				return warn(f, "structured error carries no recovery guidance",
					"Populate suggested_fix (or alternatives) with a concrete "+
						"correction so the caller does not need a --help round trip")
			}
		}
	}
	// Non-structured error is a warning, not a hard fail
	return warn(f, "error output is not structured JSON",
		"Return JSON errors with a 'code' field on stderr")
}

// aggregateContractsErrors folds the two Factor-4 runtime sub-checks —
// the suggest-only envelope and the per-mode autocorrect semantics — into
// one row, per the "one row per factor" model the F13 arm already follows.
//
// Precedence matches aggregateConsentingTelemetry, with one addition it
// needs and F13 does not: a warn survives. The base sub-check reports warn
// for a tool whose error is unstructured or fix-less, and collapsing that
// to pass because the autocorrect arm skipped would hide the finding the
// factor exists to surface.
//
//   - any fail → fail, every failing Details concatenated.
//   - else any warn → warn, carrying that sub-check's guidance.
//   - all skip → skip.
//   - else pass.
func aggregateContractsErrors(rs ...CheckResult) CheckResult {
	f := FactorContractsErrors
	var failed, skipped []string
	var firstWarn *CheckResult
	for i, r := range rs {
		switch r.Status {
		case "fail":
			failed = append(failed, r.Details)
		case "skip":
			skipped = append(skipped, r.Details)
		case "warn":
			if firstWarn == nil {
				firstWarn = &rs[i]
			}
		}
	}
	if len(failed) > 0 {
		return fail(f, strings.Join(failed, "; "),
			"Address each failing sub-condition (structured error envelope; "+
				"autocorrect mode semantics)")
	}
	if firstWarn != nil {
		return *firstWarn
	}
	if len(skipped) == len(rs) {
		return skip(f, joinUnique(skipped, "; "))
	}
	return pass(f, "structured error with recovery guidance; "+
		"autocorrect mode semantics honored")
}

// autocorrectEnvVar is the environment name kit reads for its opt-in flag
// autocorrect policy. Set by the probe below to elicit each mode from a
// tool that supports it; a tool that does not simply ignores it, which is
// what makes the probe skip rather than fail.
const autocorrectEnvVar = "KIT_AUTOCORRECT"

// correctedFromField is the envelope key a tool MUST populate when it
// rewrote a flag for the caller. Named here because the probe's whole
// obligation is its presence.
const correctedFromField = "corrected_from"

// rtContractsErrorsAutocorrect checks the per-mode semantics of an opt-in
// flag autocorrect, when the tool has one.
//
// The obligations differ by mode, and each is the thing that mode's
// existence puts at risk:
//
//   - off (and the default, which is off): a mistyped flag exits
//     non-zero. This is the contract every other caller depends on, and a
//     tool that quietly corrects by default has broken it for every
//     script that was relying on the failure.
//   - read: IF a correction was applied — exit 0 on an invocation that
//     should have failed to parse — the envelope MUST carry
//     corrected_from. A run that silently becomes a different run is
//     unauditable, and stderr prose is not something a --format json
//     consumer reads.
//
// The probe is aimed at a READ command, not at the root, because a
// correction is only ever legitimate on one: the side-effect gate is
// per-leaf, and a tool that rewrote a flag on an unannotated root would
// be violating the gate rather than demonstrating the feature. Same
// read-command discovery the F2/F3 probes use, so a spec that names none
// skips here too.
//
// The near-miss token is a one-edit typo of --format, which every kit
// tool has. `--formt` rather than `--forma`: the latter is a PREFIX of
// --format, --format-opt and --format-help, so it is ambiguous by
// construction and no compliant tool would ever correct it — a probe
// built on it would report skip for every tool on earth and measure
// nothing.
//
// A tool with no autocorrect support ignores the environment variable and
// keeps exiting non-zero, which is indistinguishable from mode off and so
// reported as a skip rather than a failure: the factor does not require a
// tool to HAVE this feature, only to be honest about it if it does.
func rtContractsErrorsAutocorrect(bin string, spec *toolspecYAML) CheckResult {
	f := FactorContractsErrors
	const nearMiss = "--formt=json"

	readCmd := findReadCommand(spec)
	if readCmd == "" {
		return skip(f, "no read command found to probe autocorrect on")
	}

	// Obligation 1: the default is suggest-only. No policy in the
	// environment, a flag that cannot parse, and the exit must be
	// non-zero.
	if _, _, code := run(bin, readCmd, nearMiss); code == 0 {
		return fail(f,
			"a mistyped flag exited 0 with no autocorrect policy set",
			"Keep autocorrect off by default: a bad flag must exit non-zero "+
				"unless the caller opted in")
	}

	// Obligation 2: explicit off behaves as the default does.
	if _, _, code := runEnv(bin, []string{autocorrectEnvVar + "=off"},
		readCmd, nearMiss); code == 0 {
		return fail(f,
			"a mistyped flag exited 0 under an explicit off policy",
			"Honor the off value: it must not be read as unset")
	}

	// Obligation 3: under read, a correction that WAS applied is
	// declared. An unapplied one is equally correct — the tool may have
	// no autocorrect, or may judge the candidate too weak — so only the
	// exit-0 case carries an obligation.
	stdout, stderr, code := runEnv(bin, []string{autocorrectEnvVar + "=read"},
		readCmd, nearMiss)
	if code != 0 {
		return skip(f, "binary does not apply flag corrections under "+
			autocorrectEnvVar+"=read")
	}
	// The envelope is on stderr, where kit writes every envelope, so the
	// command's own data on stdout stays clean. Check both: a tool that
	// put it on stdout still declared it, and this factor is about the
	// declaration, not the stream (Factor 3 owns the stream).
	if correctionDeclared(stderr) || correctionDeclared(stdout) {
		return pass(f, "applied flag correction declares "+correctedFromField)
	}
	return fail(f,
		"a flag correction was applied but no "+correctedFromField+" was reported",
		"Emit "+correctedFromField+" in the structured envelope naming the token "+
			"the caller typed, so an agent and an audit log can both see that the "+
			"command that ran is not the command that was asked for")
}

// correctionDeclared reports whether s carries a corrected_from field.
//
// Tolerant of surrounding output on purpose: a tool may write the notice
// alongside logs, and a probe that demanded the stream be exactly one
// JSON document would be testing Factor 3's obligation, not this one.
// Every document on the stream is tried, then the raw text as a fallback
// for a YAML or plaintext rendering.
func correctionDeclared(s string) bool {
	dec := json.NewDecoder(strings.NewReader(s))
	for {
		var obj map[string]any
		if err := dec.Decode(&obj); err != nil {
			break
		}
		if v, ok := obj[correctedFromField].(string); ok && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return strings.Contains(s, correctedFromField+":") ||
		strings.Contains(s, "Corrected from:")
}

// errorCarriesFix reports whether a decoded error envelope offers the
// caller a way forward.
//
// Either field satisfies it. A single unambiguous correction belongs in
// suggested_fix; an ambiguous one belongs in alternatives, and a tool that
// declines to guess between candidates is behaving correctly, not
// incompletely. Empty strings and empty lists do not count — a present but
// blank field is the same dead end as an absent one.
//
// A bare `--help` pointer does not count either, in EITHER field. "Run
// tool --help for usage" is precisely the round trip this factor exists to
// eliminate: it tells the caller to go read the page they would have read
// anyway, and accepting it would let a tool pass "with recovery guidance"
// for offering none. A fix that names --help alongside something concrete
// still counts — the concrete part is the guidance.
func errorCarriesFix(obj map[string]any) bool {
	if fix, ok := obj["suggested_fix"].(string); ok && isConcreteFix(fix) {
		return true
	}
	alts, ok := obj["alternatives"].([]any)
	if !ok {
		return false
	}
	for _, a := range alts {
		if s, ok := a.(string); ok && isConcreteFix(s) {
			return true
		}
	}
	return false
}

// isConcreteFix reports whether s is recovery guidance rather than a
// pointer back at the help page.
//
// The test is what remains once every word that only exists to frame the
// help invocation is removed. "run 'tool sub --help' for usage",
// "tool --help", "see --help" all reduce to nothing and are refused;
// "--counters", "--status TODO", "use --format json (not --help)" keep a
// token of their own and are accepted.
func isConcreteFix(s string) bool {
	fields := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(s)), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\'' || r == '"' || r == '`' ||
			r == ',' || r == '.' || r == ';' || r == ':' || r == '(' || r == ')'
	})
	for _, w := range fields {
		switch w {
		case "--help", "-h", "help", "run", "see", "try", "use", "for", "usage",
			"the", "a", "an", "to", "and", "or", "then", "check", "consult", "with":
			continue
		}
		// A word that is neither help-framing nor a command path segment
		// leading up to --help is content. Command names are the one
		// ambiguous case: "tool sub --help" names a path, not a fix, so
		// treat a bare word as framing and require a flag, a value
		// assignment, or punctuation-bearing token to count.
		if strings.HasPrefix(w, "-") || strings.ContainsAny(w, "=<>[]{}/|@") {
			return true
		}
	}
	return false
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
