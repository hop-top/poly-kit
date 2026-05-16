package verbs_test

import (
	"testing"

	"hop.top/kit/go/conformance/scenario/verbs"
	"hop.top/kit/go/conformance/scenariorules"
)

// TestVerbRegistryMatchesRulesJSON enforces the leak-rule
// consistency invariant from design §13: every verb declared in
// contracts/scenario-rules.json must be registered in the grader,
// and every registered verb must be in the JSON. Drift between the
// two surfaces is a CI failure rather than a silent run-time
// confusion.
func TestVerbRegistryMatchesRulesJSON(t *testing.T) {
	doc, err := scenariorules.LoadDefault()
	if err != nil {
		t.Fatalf("load embedded rules: %v", err)
	}
	rulesSet := map[string]struct{}{}
	for _, v := range doc.Verbs {
		rulesSet[v] = struct{}{}
	}
	registered := map[string]struct{}{}
	for _, k := range verbs.AllKinds() {
		registered[k] = struct{}{}
	}
	for v := range rulesSet {
		if _, ok := registered[v]; !ok {
			t.Errorf("verb %q in rules.json but not in grader registry", v)
		}
	}
	for v := range registered {
		if _, ok := rulesSet[v]; !ok {
			t.Errorf("verb %q in grader registry but not in rules.json", v)
		}
	}
}

func TestIsKnown(t *testing.T) {
	if !verbs.IsKnown("exit_code_equals") {
		t.Errorf("exit_code_equals should be known")
	}
	if verbs.IsKnown("nope_not_real") {
		t.Errorf("nope_not_real should not be known")
	}
}
