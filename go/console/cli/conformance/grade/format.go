package grade

import (
	"fmt"
	"io"
	"time"

	"hop.top/kit/go/conformance/client"
)

// gradeReport is the output.Dispatch payload wrapper around a
// client.Result. The Result type lives in an external package so we
// cannot attach methods to it directly; gradeReport bridges the gap
// while preserving the JSON wire shape (the wrapper marshals as the
// inner Result via the unnamed embedded field).
//
// JSON / YAML encoders read the inner Result's struct tags directly.
// RenderHuman implements output.HumanRenderer for --format=human.
type gradeReport struct {
	*client.Result
}

// RenderHuman writes the terminal-friendly verdict view used by
// grade's human format. Mirrors the pre-consolidation renderHuman.
// No ANSI escapes — the bare glyph carries the signal.
func (r *gradeReport) RenderHuman(w io.Writer) error {
	if r == nil || r.Result == nil {
		return nil
	}
	mark := verdictMark(r.Verdict)
	fmt.Fprintf(w, "verdict: %s %s\n", r.Verdict, mark)
	if r.ScenarioID != "" {
		fmt.Fprintf(w, "  scenario:    %s\n", r.ScenarioID)
	}
	if r.Tier > 0 {
		fmt.Fprintf(w, "  tier:        %d\n", r.Tier)
	}
	if r.GraderVersion != "" {
		fmt.Fprintf(w, "  grader:      %s\n", r.GraderVersion)
	}
	if r.RulesVersion != "" {
		fmt.Fprintf(w, "  rules:       %s\n", r.RulesVersion)
	}
	if !r.ScoredAt.IsZero() {
		fmt.Fprintf(w, "  scored at:   %s\n", r.ScoredAt.Format(time.RFC3339))
	}
	if r.Reason != "" {
		fmt.Fprintf(w, "  reason:      %s\n", r.Reason)
	}
	if len(r.Facets) > 0 {
		fmt.Fprintln(w, "  factor coverage:")
		for _, f := range r.Facets {
			fmt.Fprintf(w, "    [%d]  %s\n", f.Factor, f.Status)
		}
	}
	if failed := failedAssertions(r.Assertions); len(failed) > 0 {
		fmt.Fprintln(w, "  failing assertions:")
		for _, a := range failed {
			fmt.Fprintf(w, "    [%s]  %s  %s", a.ID, a.Kind, a.Status)
			if a.Expected != nil || a.Observed != nil {
				fmt.Fprintf(w, "  expected %v, observed %v", a.Expected, a.Observed)
			}
			if a.Message != "" {
				fmt.Fprintf(w, "  (%s)", a.Message)
			}
			fmt.Fprintln(w)
		}
	}
	return nil
}

// failedAssertions filters the tier-3 trace down to entries that did
// not pass. fail, ungradable, and not_implemented all warrant a line:
// each explains a facet that is not green.
func failedAssertions(as []client.Assertion) []client.Assertion {
	var out []client.Assertion
	for _, a := range as {
		if a.Status != client.StatusPass {
			out = append(out, a)
		}
	}
	return out
}

// verdictMark returns a unicode glyph for the verdict that does not
// rely on ANSI color. The bare glyph is enough signal in any TTY.
func verdictMark(v client.Verdict) string {
	switch v {
	case client.VerdictPass:
		return "OK"
	case client.VerdictFail:
		return "FAIL"
	case client.VerdictUngradable:
		return "UNGRADABLE"
	}
	return "?"
}
