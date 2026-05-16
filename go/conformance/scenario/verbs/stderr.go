package verbs

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// stderr_contains: { value: string, regex?: bool }
// stderr_does_not_contain: same shape

func init() {
	register(&Entry{
		Kind:     KindStderrContains,
		Validate: validateStderrMatch,
		Evaluate: evalStderrContains,
	})
	register(&Entry{
		Kind:     KindStderrDoesNotContain,
		Validate: validateStderrMatch,
		Evaluate: evalStderrDoesNotContain,
	})
}

func validateStderrMatch(args map[string]any) []string {
	var out []string
	if s, ok := args["value"].(string); !ok || s == "" {
		out = append(out, "value must be a non-empty string")
	}
	if raw, ok := args["regex"]; ok {
		if _, ok := raw.(bool); !ok {
			out = append(out, "regex must be boolean")
		}
	}
	return out
}

func stderrMatch(stderr []byte, value string, useRegex bool) (bool, error) {
	s := string(stderr)
	if !useRegex {
		return strings.Contains(s, value), nil
	}
	re, err := regexp.Compile(value)
	if err != nil {
		return false, fmt.Errorf("regex compile: %w", err)
	}
	return re.MatchString(s), nil
}

func evalStderrContains(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	want, _ := spec.Args["value"].(string)
	useRegex, _ := spec.Args["regex"].(bool)
	hit, err := stderrMatch(vctx.Capture.Stderr, want, useRegex)
	if err != nil {
		return Ungradable("stderr_contains: " + err.Error())
	}
	if hit {
		return EvalResult{Status: StatusPass, Expected: want}
	}
	return Fail(string(vctx.Capture.Stderr), want, "stderr does not contain expected value")
}

func evalStderrDoesNotContain(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	want, _ := spec.Args["value"].(string)
	useRegex, _ := spec.Args["regex"].(bool)
	hit, err := stderrMatch(vctx.Capture.Stderr, want, useRegex)
	if err != nil {
		return Ungradable("stderr_does_not_contain: " + err.Error())
	}
	if hit {
		return Fail(string(vctx.Capture.Stderr), fmt.Sprintf("absent: %q", want),
			"stderr contains forbidden value")
	}
	return EvalResult{Status: StatusPass, Expected: fmt.Sprintf("absent: %q", want)}
}
