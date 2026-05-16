// Package predicates is the testing-T-free kernel of the harness
// assertions. The kernel returns (ok bool, summary string); the
// testing wrapper in the parent package consumes (ok, summary) to
// drive t.Errorf / t.Fatalf.
//
// The scenario engine (the scenario engine) re-uses this kernel
// without depending on testing — it dispatches the same predicates
// at scenario-evaluation time.
package predicates

import (
	"fmt"

	"hop.top/kit/go/console/cli/conformance/harness/classifier"
	"hop.top/kit/go/console/cli/conformance/harness/diff"
)

// NoMutation returns (ok, summary) where ok is true iff every
// interaction in dir classifies as Read under the per-adapter
// classifier (with the supplied overrides). The summary lists the
// violations on failure.
func NoMutation(dir string, ov classifier.Overrides, reqFromPayload func(adapter string, payload map[string]any) any) (bool, string) {
	interactions, err := diff.List(dir)
	if err != nil {
		return false, fmt.Sprintf("predicates.NoMutation: list cassettes: %v", err)
	}
	violations := 0
	out := ""
	for _, it := range interactions {
		req := reqFromPayload(it.Adapter, it.ReqPayload)
		class := classifier.Classify(it.Adapter, req, ov)
		if class == classifier.ClassRead {
			continue
		}
		violations++
		out += fmt.Sprintf("\n  ✗ %-5s %s   (classified: %s)",
			it.Adapter, it.Summary, class)
	}
	if violations == 0 {
		return true, ""
	}
	return false, fmt.Sprintf("%d mutating interaction(s)%s", violations, out)
}

// CassetteEqual returns (ok, summary) where ok is true iff the two
// cassette dirs compare equal under the multiset-of-(adapter,fp)
// rule, modulo recorded_at noise. The summary is the human-readable
// diff body on failure.
func CassetteEqual(a, b string) (bool, string) {
	d, err := diff.Cassettes(a, b)
	if err != nil {
		return false, fmt.Sprintf("predicates.CassetteEqual: %v", err)
	}
	if d.Empty() {
		return true, ""
	}
	return false, d.Format(nil)
}
