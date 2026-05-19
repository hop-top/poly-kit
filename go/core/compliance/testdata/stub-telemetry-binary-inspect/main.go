// Stub binary for compliance runtime checks of F13 sub-conditions
// (b) consent subcommands and (g) inspect-returns-post-redact.
//
// A sibling stub for the kill-switch check lives at
// ../stub-telemetry-binary/; this stub has a different behavioral
// shape (subcommand routing, regex redact loop, audit publish) so we
// keep them separate per the runtime_inspect.go header.
//
// Build via TestMain in runtime_inspect_test.go — never go-install'd.
// Tests live under hop.top/kit/hops/main/go/core/compliance.
//
// Modes are selected via the STUB_INSPECT env:
//
//	normal          subcommands respond; inspect parses
//	                KIT_TELEMETRY_TEST_INJECT, redacts emails +
//	                sk-... tokens to <redacted:email> /
//	                <redacted:token>, emits the redacted JSONL on
//	                stdout, and publishes a
//	                kit.telemetry.redact.matched event per match to
//	                KIT_BUS_SINK_PATH. PASS path for all asserts.
//	no-redact       inspect echoes the inject payload UN-REDACTED.
//	                Negative test for sub-condition (g) — raw PII
//	                present, audit topic silent.
//	silent-redact   inspect redacts (so raw PII absent) BUT does NOT
//	                publish kit.telemetry.redact.matched events. The
//	                load-bearing negative test for the audit-topic
//	                assertion: a no-op redactor that ALSO blanks
//	                output passes (g)'s "raw PII absent" arm
//	                vacuously without the audit-topic arm.
//	missing-inspect inspect subcommand returns "unknown command",
//	                exit 1. Negative test for (b) — inspect missing.
//	primitive       inspect returns JSON-valid primitive `true`.
//	                Negative test for (b)'s "not object/array" arm.
//	no-test-hook    inspect ignores KIT_TELEMETRY_TEST_INJECT and
//	                returns `{}`. Triggers the post-redact arm's
//	                skip-with-reason behavior.
//
// The default mode (empty STUB_INSPECT) is "normal" so the binary
// behaves correctly out of the box when invoked without env mucking.
//
// All emitted JSON is single-line. Subcommands other than "inspect"
// emit `{}` or `{"state":"granted"}` as documented in the test plan.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// emailRE matches RFC-5322-ish emails. Loose on purpose — this stub
// is for testing the compliance check's behavior, not for production
// redaction.
var emailRE = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

// tokenRE matches the OpenAI-style `sk-` prefix plus a payload of
// alphanumerics. Picked because it's the canonical "API key prefix"
// the test plan calls out (emails + sk-... API key prefixes).
var tokenRE = regexp.MustCompile(`sk-[A-Za-z0-9]{4,}`)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "telemetry" {
		// Outside telemetry tree the stub behaves as a no-op.
		// runtime_inspect.go only ever invokes `<bin> telemetry ...`
		// so this branch is mostly for `<bin> --help`-style probes
		// we may want to add later.
		fmt.Println("{}")
		return
	}

	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "missing subcommand")
		os.Exit(1)
	}

	sub := os.Args[2]
	mode := os.Getenv("STUB_INSPECT")
	if mode == "" {
		mode = "normal"
	}

	switch sub {
	case "status":
		// Always JSON object so the runtime check's "valid JSON +
		// not-a-primitive" assertion for `status` would pass too
		// (the inspect-only assertion is the strict one, but `status`
		// failing primitive would be silly).
		fmt.Println(`{"state":"granted"}`)
		return
	case "enable", "disable", "reset":
		// Per the test plan: no-op, exit 0, emit empty JSON object.
		fmt.Println(`{}`)
		return
	case "inspect":
		runInspect(mode)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", sub)
		os.Exit(1)
	}
}

// runInspect dispatches inspect behavior off the mode env. Split out
// so the main switch stays readable; all exit calls happen here.
func runInspect(mode string) {
	switch mode {
	case "missing-inspect":
		fmt.Fprintln(os.Stderr, "unknown command: telemetry inspect")
		os.Exit(1)
	case "primitive":
		// JSON-valid but a literal — the negative test for
		// rtConsentingTelemetryInspect's "must be object/array"
		// assertion on inspect output.
		fmt.Println("true")
		return
	}

	inject := os.Getenv("KIT_TELEMETRY_TEST_INJECT")
	if inject == "" || mode == "no-test-hook" {
		// No-test-hook signal: inspect returns an empty object, no
		// audit, no redact. The runtime check sees `{}` and decides
		// to skip the post-redact arm with reason "no test-inject
		// hook available".
		fmt.Println(`{}`)
		return
	}

	switch mode {
	case "no-redact":
		// Echo the inject payload verbatim. Negative test for (g).
		fmt.Println(strings.TrimSpace(inject))
		return
	case "silent-redact":
		// Apply redaction but skip the audit publish. Load-bearing
		// negative test: without the audit-topic assertion this
		// would pass vacuously.
		redacted, _ := redactJSON(inject)
		fmt.Println(redacted)
		return
	default: // "normal" or any unrecognized value
		redacted, matches := redactJSON(inject)
		// Publish audit events to KIT_BUS_SINK_PATH BEFORE printing
		// the redacted payload — order doesn't matter for the
		// assertion (the test reads the JSONL after the process
		// exits) but doing it first keeps the audit honest if the
		// stub later grows side effects between regex pass and
		// publish.
		publishAuditEvents(matches)
		fmt.Println(redacted)
		return
	}
}

// redactJSON treats inject as a string blob (it MAY parse as JSON; we
// don't care for stub purposes). Applies the email + token regexes
// and returns (redactedText, slice-of-match-records). Each match
// record is published as a kit.telemetry.redact.matched event by
// publishAuditEvents.
//
// Returning the matches separately (rather than re-scanning the
// pre-redact text from publishAuditEvents) avoids a second pass and
// keeps the "what got matched" record perfectly aligned with what
// was actually replaced.
func redactJSON(input string) (string, []match) {
	var matches []match
	out := emailRE.ReplaceAllStringFunc(input, func(s string) string {
		matches = append(matches, match{RuleID: "email", Original: s})
		return "<redacted:email>"
	})
	out = tokenRE.ReplaceAllStringFunc(out, func(s string) string {
		matches = append(matches, match{RuleID: "token", Original: s})
		return "<redacted:token>"
	})
	return out, matches
}

// match is the audit-event payload shape published per redacted
// match. Mirrors hops/main/go/core/redact.Match's public fields
// (rule_id, original) enough for the test to identify which rule
// fired; the harness only inspects the `topic` field, so the inner
// shape is documentary.
type match struct {
	RuleID   string `json:"rule_id"`
	Original string `json:"original"`
}

// publishAuditEvents writes one JSONL line per match to
// KIT_BUS_SINK_PATH. The runtime check's rtEnv reads the same path
// via EventsEmitted() and filters by topic == kit.telemetry.redact.matched.
//
// Errors are silently swallowed — this is a TEST stub, and failing
// to publish IS the silent-redact mode anyway. If the sink path is
// not set (which it should be — rtEnv always sets it) the function
// returns without writing.
func publishAuditEvents(matches []match) {
	sinkPath := os.Getenv("KIT_BUS_SINK_PATH")
	if sinkPath == "" || len(matches) == 0 {
		return
	}
	f, err := os.OpenFile(sinkPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	for _, m := range matches {
		ev := map[string]any{
			"topic":   "kit.telemetry.redact.matched",
			"rule_id": m.RuleID,
			// Intentionally do NOT include the original PII string in
			// the audit event — the audit topic exists to PROVE the
			// redactor ran, not to re-leak the secret.
		}
		b, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return
		}
	}
}
