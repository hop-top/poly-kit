package grade

import (
	"fmt"
	"io"

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
	if r.ScoredAt != "" {
		fmt.Fprintf(w, "  scored at:   %s\n", r.ScoredAt)
	}
	if r.Reason != "" {
		fmt.Fprintf(w, "  reason:      %s\n", r.Reason)
	}
	if len(r.Facets) > 0 {
		fmt.Fprintln(w, "  factor coverage:")
		for _, f := range r.Facets {
			fmt.Fprintf(w, "    [%d]  %-12s  %s\n", f.Factor, f.Status, f.Description)
		}
	}
	if len(r.Findings) > 0 {
		fmt.Fprintln(w, "  failing assertions:")
		for _, fi := range r.Findings {
			fmt.Fprintf(w, "    [%s]  %s  expected %s, observed %s\n",
				fi.ID, fi.Kind, fi.Expected, fi.Observed)
		}
	}
	return nil
}

// verdictMark returns a unicode glyph for the verdict that does not
// rely on ANSI color. The bare glyph is enough signal in any TTY.
func verdictMark(v string) string {
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
