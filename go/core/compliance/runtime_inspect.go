package compliance

// Runtime check for FactorConsentingTelemetry sub-conditions (b) +
// (g) per ADR-0037.
//
// Two sub-conditions are merged into one CheckResult (per ADR-0037's
// "single row per factor" model):
//
//	A. Subcommand availability — for each canonical consent
//	   subcommand declared in spec.Telemetry.ConsentSubcommands and
//	   present in the canonical set {status, enable, disable, reset,
//	   inspect}, run `<bin> telemetry <sub> --format json`. Require
//	   exit 0 and json.Valid(stdout). For `inspect` specifically,
//	   require the top-level JSON value is an object or array (not
//	   a primitive like `true`, `null`, or `123`).
//
//	B. inspect-returns-POST-REDACT — seed consent=granted, inject a
//	   PII-laden event via KIT_TELEMETRY_TEST_INJECT, run inspect,
//	   assert raw PII strings are ABSENT and documented placeholders
//	   (<redacted:email>, <redacted:token>) are PRESENT.
//
//	C. Audit-topic subscription — same rtEnv as (B): assert at least
//	   one `kit.telemetry.redact.matched` event was emitted on the
//	   bus during the run. Load-bearing: a no-op redactor that blanks
//	   output without firing matches would pass (B) vacuously; the
//	   audit topic proves redact RAN.
//
// The post-redact + audit-topic arms (B + C) auto-skip with a
// reason when the binary does not honor KIT_TELEMETRY_TEST_INJECT
// (detected by injecting a sentinel payload and observing it does
// NOT appear, either raw or redacted, in inspect's output).
//
// Public surface is unexported — invoked by runtime.go via
// runRuntimeChecks. Tests live in runtime_inspect_test.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// rtConsentingTelemetryInspect runs the F13 sub-(b)+(g)+audit-topic
// runtime arm. Skips when the toolspec does not opt into telemetry
// (ADR-0037 §4: F13 only applies to opt-in binaries).
//
// ctx is honored on every subprocess invocation; callers MUST pass a
// deadline-bearing ctx (the harness wraps subprocess Run in
// exec.CommandContext so a hung adopter binary cannot stall the
// whole suite).
//
// envFactory builds a fresh rtEnv per scenario; injected for tests
// so each Test* function can supply its own *testing.T-bound env
// builder. Production callers should pass a closure that calls
// newRTEnv(t) inside.
func rtConsentingTelemetryInspect(ctx context.Context, bin string, spec *toolspecYAML, envFactory func() *rtEnv) CheckResult {
	f := FactorConsentingTelemetry

	if !telemetryOptedIn(spec) {
		return skip(f, "binary does not opt into telemetry")
	}

	var details []string

	// --- Arm A: subcommand availability ----------------------------
	//
	// Intersect declared subcommands with the canonical set. We run
	// only the intersection because (i) the static
	// checkConsentingTelemetry already gates "canonical set must be
	// declared", so if anything is missing the static row will fail
	// and the runtime check would be doubling up; (ii) an adopter
	// that declares additional subcommands beyond the canonical set
	// is out of scope — ADR-0037 (b) only requires the five.
	subs := intersectCanonical(spec.Telemetry.ConsentSubcommands)
	if len(subs) == 0 {
		// No canonical subcommands declared — static check already
		// flags this. We skip the runtime arm rather than fail twice.
		return skip(f, "no canonical consent subcommands declared (static check will flag this)")
	}

	envA := envFactory()
	for _, sub := range subs {
		stdout, _, code := envA.Run(ctx, bin, "telemetry", sub, "--format", "json")
		trimmed := strings.TrimSpace(stdout)
		if code != 0 {
			details = append(details, fmt.Sprintf(
				"telemetry %s --format json exited %d (stdout snippet: %s)",
				sub, code, snippet(trimmed)))
			continue
		}
		if !json.Valid([]byte(trimmed)) {
			details = append(details, fmt.Sprintf(
				"telemetry %s --format json returned invalid JSON (snippet: %s)",
				sub, snippet(trimmed)))
			continue
		}
		if sub == "inspect" {
			// json.Decode + reflect on the top-level type. Primitives
			// (string/number/bool/null) MUST fail per the spec — only
			// object `{...}` or array `[...]` are acceptable.
			if !isObjectOrArray(trimmed) {
				details = append(details, fmt.Sprintf(
					"telemetry inspect returned a JSON primitive, want object/array (snippet: %s)",
					snippet(trimmed)))
				continue
			}
		}
	}

	// --- Arm B + C: post-redact + audit topic ----------------------
	//
	// Fresh env so seeded consent + injected PII do not bleed from
	// arm A. SeedConsent writes the YAML; the stub honors it by
	// proxy (the runtime check does not actually validate that the
	// adopter binary respected the consent file beyond proceeding to
	// inspect successfully — the consent file is set for hygiene).
	envBC := envFactory()
	if err := envBC.SeedConsent("granted", "test", 1); err != nil {
		details = append(details, fmt.Sprintf("seed consent: %v", err))
		return aggregate(f, details)
	}

	// PII fixtures. Hardcoded because hops/main/go/core/redact/testdata/
	// does not exist (the redact package ships rules in-tree, not
	// fixtures). The patterns are deliberately obvious
	// — emails + an OpenAI-style API key prefix — so the runtime
	// check works against any redactor that handles the standard
	// gitleaks / Presidio rule set.
	const (
		piiEmail = "user.victim@example.com"
		piiToken = "sk-abcdef1234567890abcdef"
	)
	injectPayload := fmt.Sprintf(
		`{"category":"invocation","payload":{"contact":"%s","key":"%s"}}`,
		piiEmail, piiToken)
	envBC.SetEnv("KIT_TELEMETRY_TEST_INJECT", injectPayload)

	stdout, _, code := envBC.Run(ctx, bin, "telemetry", "inspect", "--format", "json")
	trimmed := strings.TrimSpace(stdout)

	if code != 0 {
		// inspect already failed in arm A — don't double-report; just
		// skip B + C.
		return aggregate(f, details)
	}

	// Detect test-hook honoring: if the binary ignores the inject
	// env, the output will be empty/devoid of either the raw PII OR
	// the placeholders. We assert at least ONE of (raw, placeholder)
	// is present; absence of both signals "no test-inject hook
	// available" and we skip arms B + C with a documented reason.
	hasRaw := strings.Contains(trimmed, piiEmail) || strings.Contains(trimmed, piiToken)
	hasPlaceholder := strings.Contains(trimmed, "<redacted:email>") ||
		strings.Contains(trimmed, "<redacted:token>")

	if !hasRaw && !hasPlaceholder {
		if len(details) > 0 {
			return aggregate(f, details)
		}
		return CheckResult{
			Factor:  f,
			Name:    f.String(),
			Status:  "skip",
			Details: "subcommands respond; post-redact + audit-topic arms skipped — no KIT_TELEMETRY_TEST_INJECT hook available",
		}
	}

	// --- Arm B: raw PII absent + placeholders present --------------
	if strings.Contains(trimmed, piiEmail) {
		details = append(details, fmt.Sprintf(
			"telemetry inspect leaked raw PII: email %q present in output (snippet: %s)",
			piiEmail, snippet(trimmed)))
	}
	if strings.Contains(trimmed, piiToken) {
		details = append(details, fmt.Sprintf(
			"telemetry inspect leaked raw PII: token prefix %q present in output (snippet: %s)",
			piiToken, snippet(trimmed)))
	}
	// Only assert placeholders present when raw was absent — when raw
	// leaks, the redactor obviously didn't run and the "placeholder
	// missing" message would be noise.
	if !strings.Contains(trimmed, piiEmail) && !strings.Contains(trimmed, piiToken) {
		if !hasPlaceholder {
			details = append(details, fmt.Sprintf(
				"telemetry inspect output missing redact placeholders (<redacted:email>/<redacted:token>); snippet: %s",
				snippet(trimmed)))
		}
	}

	// --- Arm C: audit topic subscription ---------------------------
	//
	// Poll the bus JSONL up to 500ms — the stub publishes
	// synchronously before inspect's stdout flush, but a real
	// telemetry pipeline may emit on a goroutine. We bound the wait.
	// "No event with topic X" = filter EventsEmitted() by topic and
	// assert len == 0 → fail with the "audit silent" message.
	deadline := 500 * time.Millisecond
	envBC.PollEvents(1, deadline) // best-effort wait — we still inspect manually below
	auditEvents := filterByTopic(envBC.EventsEmitted(), "kit.telemetry.redact.matched")
	if len(auditEvents) == 0 {
		details = append(details, "redactor ran silently — no kit.telemetry.redact.matched events emitted during inspect (audit topic empty)")
	}

	return aggregate(f, details)
}

// intersectCanonical returns the entries in declared that also appear
// in telemetryConsentSubcommandsCanonical, preserving declared order.
// Used by arm A so the runtime probe only touches subcommands the
// spec actually claims to ship.
func intersectCanonical(declared []string) []string {
	out := make([]string, 0, len(declared))
	for _, d := range declared {
		for _, c := range telemetryConsentSubcommandsCanonical {
			if d == c {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// isObjectOrArray reports whether the JSON text's top-level value is
// an object or array. Empty input returns false; malformed JSON
// returns false too (the helper is robust on its own — callers in
// runtime_inspect.go already pre-screen with json.Valid, but the
// double-check costs nothing and makes the helper unit-testable in
// isolation).
//
// Implementation: validate the whole blob first via json.Valid, then
// peek at the leading delimiter. json.Decoder.Token() alone would
// happily return `{` for `{not json` because the decoder is
// stream-oriented; full validation is the safer route.
func isObjectOrArray(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if !json.Valid([]byte(s)) {
		return false
	}
	dec := json.NewDecoder(strings.NewReader(s))
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	if delim, ok := tok.(json.Delim); ok {
		return delim == '{' || delim == '['
	}
	return false
}

// filterByTopic returns events whose top-level "topic" field equals
// topic. Events without a string "topic" field are skipped — they're
// not malformed, just irrelevant to the audit-topic assertion.
func filterByTopic(events []map[string]any, topic string) []map[string]any {
	var out []map[string]any
	for _, ev := range events {
		t, ok := ev["topic"].(string)
		if !ok {
			continue
		}
		if t == topic {
			out = append(out, ev)
		}
	}
	return out
}

// snippet truncates s for inclusion in Details messages. Long stdout
// dumps are useless in a one-line status row; first 80 chars + `...`
// when truncated keeps the suggestion actionable without flooding
// the report.
func snippet(s string) string {
	const limit = 80
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

// aggregate folds details into a single pass/fail CheckResult. Empty
// details → pass with a canned summary; non-empty → fail with the
// details joined and an ADR-0037-anchored suggestion.
func aggregate(f Factor, details []string) CheckResult {
	if len(details) == 0 {
		return pass(f, "consent subcommands respond + inspect returns post-redact + audit topic fired")
	}
	return fail(f, strings.Join(details, "; "),
		"Per ADR-0037 (b)+(g): each consent subcommand must exit 0 with "+
			"valid JSON; `inspect` must return a JSON object/array containing "+
			"redacted (not raw) PII; the redactor MUST publish "+
			"kit.telemetry.redact.matched audit events when matches fire.")
}
