package verbs

import (
	"context"
	"encoding/json"
	"fmt"
)

// capability_roundtrip: { leaves?: []string } — verifies that the
// on-step's stdout (assumed to be `<bin> capabilities --json`
// output) declares leaves that the adopter can `<leaf> --help`
// without surprises. Library-side we check the structural
// roundtrip: parses as JSON, has a "leaves" array, and (when
// leaves: is set) every listed leaf is present in the array.
//
// Full re-invocation roundtrip (each leaf --help exits clean) is
// out of scope for the v1 grader; that's harness territory and
// requires running the binary, which the grader is decoupled from.

func init() {
	register(&Entry{
		Kind:     KindCapabilityRoundtrip,
		Validate: validateCapabilityRoundtrip,
		Evaluate: evalCapabilityRoundtrip,
	})
}

func validateCapabilityRoundtrip(args map[string]any) []string {
	if raw, ok := args["leaves"]; ok {
		list, ok := raw.([]any)
		if !ok {
			return []string{"leaves must be a list of strings"}
		}
		for i, v := range list {
			if _, ok := v.(string); !ok {
				return []string{fmt.Sprintf("leaves[%d] is not a string", i)}
			}
		}
	}
	return nil
}

func evalCapabilityRoundtrip(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	if len(vctx.Capture.Stdout) == 0 {
		return Fail(nil, "capabilities JSON", "stdout empty")
	}
	var doc struct {
		Leaves []struct {
			Path string `json:"path"`
			Name string `json:"name"`
		} `json:"leaves"`
	}
	if err := json.Unmarshal(vctx.Capture.Stdout, &doc); err != nil {
		return Fail(nil, "capabilities JSON", fmt.Sprintf("stdout not parseable as capabilities JSON: %v", err))
	}
	present := map[string]struct{}{}
	for _, lv := range doc.Leaves {
		if lv.Path != "" {
			present[lv.Path] = struct{}{}
		}
		if lv.Name != "" {
			present[lv.Name] = struct{}{}
		}
	}
	wantRaw, _ := spec.Args["leaves"].([]any)
	missing := []string{}
	for _, w := range wantRaw {
		s, _ := w.(string)
		if s == "" {
			continue
		}
		if _, ok := present[s]; !ok {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		return Fail(missing, "all leaves present", fmt.Sprintf("leaves missing from capabilities: %v", missing))
	}
	return EvalResult{Status: StatusPass, Expected: "capabilities roundtrip OK", Observed: len(doc.Leaves)}
}
