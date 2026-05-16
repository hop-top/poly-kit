package verbs

import (
	"context"
	"encoding/json"
	"strings"
)

// stream_discipline_pass: { } — captured stdout is JSON-parseable;
// captured stderr is not (or is empty). The combined invariant
// expresses kit's stream-discipline contract: structured data on
// stdout, free-form diagnostics on stderr.

func init() {
	register(&Entry{
		Kind:     KindStreamDisciplinePass,
		Validate: nil,
		Evaluate: evalStreamDisciplinePass,
	})
}

func evalStreamDisciplinePass(_ context.Context, _ AssertionSpec, vctx VerbContext) EvalResult {
	out := vctx.Capture.Stdout
	errOut := vctx.Capture.Stderr
	if len(out) == 0 {
		// Empty stdout is acceptable iff the leaf produces no data
		// (e.g., a write-side command). We let it pass — the stricter
		// requirement is "stderr is not JSON when stdout is".
	} else {
		var dec any
		if err := json.Unmarshal(out, &dec); err != nil {
			return Fail(string(out), "stdout=JSON",
				"stdout is non-empty but not JSON-parseable")
		}
	}
	// Stderr should NOT be JSON-parseable if it has any content;
	// allow whitespace-only stderr through.
	trimmed := strings.TrimSpace(string(errOut))
	if trimmed == "" {
		return EvalResult{Status: StatusPass, Expected: "stream discipline OK"}
	}
	var sderr any
	if err := json.Unmarshal(errOut, &sderr); err == nil {
		return Fail(trimmed, "stderr=non-JSON",
			"stderr is JSON-parseable; stream-discipline expects free-form diagnostics on stderr")
	}
	return EvalResult{Status: StatusPass, Expected: "stream discipline OK"}
}
