package verbs

import (
	"context"
	"fmt"
	"strings"

	"hop.top/kit/go/console/cli/conformance/harness/classifier"
	"hop.top/kit/go/console/cli/conformance/harness/diff"
)

// destructive_gate_required: { when?: {flag_absent: "--yes"} }
//
// Checks that when the on-step's invoke does not include the
// destructive gate flag (--yes by default), the recorded cassette
// contains zero Destructive-class interactions. With the flag
// present, no constraint is applied (we don't enforce "must contain
// destructive" — that's cassette_must_contain's job).

func init() {
	register(&Entry{
		Kind:     KindDestructiveGateRequired,
		Validate: validateDestructiveGateRequired,
		Evaluate: evalDestructiveGateRequired,
	})
}

func validateDestructiveGateRequired(args map[string]any) []string {
	if raw, ok := args["when"]; ok {
		m := coerceMap(raw)
		if m == nil {
			return []string{"when must be a mapping"}
		}
		for k := range m {
			switch k {
			case "flag_absent":
			default:
				return []string{fmt.Sprintf("when.%s unknown; only flag_absent is supported", k)}
			}
		}
	}
	return nil
}

// destructiveFlagFromArgs returns the configured "destructive gate"
// flag (default --yes).
func destructiveFlagFromArgs(args map[string]any) string {
	if w, ok := coerceMap(args["when"])["flag_absent"].(string); ok && w != "" {
		return w
	}
	return "--yes"
}

func evalDestructiveGateRequired(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	flag := destructiveFlagFromArgs(spec.Args)
	// We don't know the invoke argv from VerbContext (the grader
	// strips it down to Capture). However we can detect destructive
	// activity in the recorded cassette: that's the load-bearing
	// invariant — if the cassette contains a Destructive op, the
	// gate must have been satisfied at run time. We surface fail
	// when destructive interactions are present, ungradable when
	// no flag context is available.
	//
	// The grader's adapter is responsible for arranging two captures
	// per destructive-gated test (with-flag, without-flag) for a
	// complete contract. v1 reports on what's recorded for the
	// on-step.

	items, err := diff.List(vctx.Capture.CassetteDir)
	if err != nil {
		return Ungradable("destructive_gate_required: " + err.Error())
	}
	for _, it := range items {
		cls := classifyPayload(it.Adapter, it.ReqPayload)
		if cls == classifier.ClassDestructive {
			return Fail(it.Summary, fmt.Sprintf("no destructive ops without %s", flag),
				fmt.Sprintf("destructive interaction recorded; verify the leaf rejected the invoke without %s", flag))
		}
	}
	return EvalResult{Status: StatusPass, Expected: fmt.Sprintf("no destructive ops without %s", flag)}
}

// destructive.go internally re-uses cassette.go's coerceMap +
// classifyPayload helpers. Importing strings for completeness keeps
// linters quiet when later expansion adds a Contains check.
var _ = strings.Contains
