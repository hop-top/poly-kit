package verbs

import (
	"context"
	"fmt"
	"strings"

	"hop.top/kit/go/console/cli/conformance/harness"
)

// exit_code_equals: { value: int }
// exit_code_in: { values: []int (≥1) }
// exit_code_class: { classes: []string }

func init() {
	register(&Entry{
		Kind:     KindExitCodeEquals,
		Validate: validateExitCodeEquals,
		Evaluate: evalExitCodeEquals,
	})
	register(&Entry{
		Kind:     KindExitCodeIn,
		Validate: validateExitCodeIn,
		Evaluate: evalExitCodeIn,
	})
	register(&Entry{
		Kind:     KindExitCodeClass,
		Validate: validateExitCodeClass,
		Evaluate: evalExitCodeClass,
	})
}

func validateExitCodeEquals(args map[string]any) []string {
	v, ok := args["value"]
	if !ok {
		return []string{"missing required key value"}
	}
	if _, ok := asInt(v); !ok {
		return []string{"value must be an integer"}
	}
	return nil
}

func evalExitCodeEquals(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	want, ok := asInt(spec.Args["value"])
	if !ok {
		return Ungradable("exit_code_equals: value arg malformed")
	}
	got := vctx.Capture.ExitCode
	if got == want {
		return EvalResult{Status: StatusPass, Observed: got, Expected: want}
	}
	return Fail(got, want, fmt.Sprintf("exit code %d != %d", got, want))
}

func validateExitCodeIn(args map[string]any) []string {
	raw, ok := args["values"]
	if !ok {
		return []string{"missing required key values"}
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return []string{"values must be a non-empty list of integers"}
	}
	for i, v := range list {
		if _, ok := asInt(v); !ok {
			return []string{fmt.Sprintf("values[%d] is not an integer", i)}
		}
	}
	return nil
}

func evalExitCodeIn(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	raw, _ := spec.Args["values"].([]any)
	want := make([]int, 0, len(raw))
	for _, v := range raw {
		if iv, ok := asInt(v); ok {
			want = append(want, iv)
		}
	}
	got := vctx.Capture.ExitCode
	for _, w := range want {
		if got == w {
			return EvalResult{Status: StatusPass, Observed: got, Expected: want}
		}
	}
	return Fail(got, want, fmt.Sprintf("exit code %d not in %v", got, want))
}

func validateExitCodeClass(args map[string]any) []string {
	raw, ok := args["classes"]
	if !ok {
		return []string{"missing required key classes"}
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return []string{"classes must be a non-empty list of strings"}
	}
	for i, v := range list {
		s, ok := v.(string)
		if !ok || s == "" {
			return []string{fmt.Sprintf("classes[%d] is not a non-empty string", i)}
		}
	}
	return nil
}

func evalExitCodeClass(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	raw, _ := spec.Args["classes"].([]any)
	classes := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			classes = append(classes, strings.ToUpper(strings.TrimSpace(s)))
		}
	}
	got := vctx.Capture.ExitCode
	for _, cl := range classes {
		if harness.ClassToExitCode(cl) == got {
			return EvalResult{Status: StatusPass, Observed: got, Expected: classes}
		}
	}
	return Fail(got, classes, fmt.Sprintf("exit code %d not in class set %v", got, classes))
}

// asInt accepts int, int64, float64 (yaml.v3 decodes numbers
// permissively when target type is any).
func asInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case int32:
		return int(x), true
	case uint64:
		return int(x), true
	case float64:
		return int(x), true
	case float32:
		return int(x), true
	}
	return 0, false
}
