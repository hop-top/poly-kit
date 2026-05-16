// Package thefuck extracts error patterns from thefuck Python rule files.
package thefuck

import (
	"regexp"
	"strings"

	"hop.top/kit/go/ai/toolspec"
)

// matchInRe captures 'string literal' from `in command.script` or
// `in command.output` patterns inside a match function.
var matchInRe = regexp.MustCompile(
	`'([^']+)'\s+in\s+command\.(script|output)`,
)

// matchStrRe captures "string" variants with double quotes.
var matchStrRe = regexp.MustCompile(
	`"([^"]+)"\s+in\s+command\.(script|output)`,
)

// getNewCmdReturnRe captures the return expression from get_new_command.
var getNewCmdReturnRe = regexp.MustCompile(
	`(?m)^\s+return\s+(.+)$`,
)

// complexIndicators signal that a rule is too dynamic for static extraction.
var complexIndicators = []string{
	"import re",
	"get_close_matches",
	"_get_patterns",
	"for ",
	"any(",
	"all(",
	"re.search",
	"re.match",
	"re.findall",
}

// ParseRule attempts to statically extract an ErrorPattern from a thefuck
// Python rule. Returns nil, nil for rules that are too complex to parse.
func ParseRule(name, pythonSource string) (*toolspec.ErrorPattern, error) {
	// Bail on complex rules.
	for _, ind := range complexIndicators {
		if strings.Contains(pythonSource, ind) {
			return nil, nil
		}
	}

	// Extract match patterns.
	var patterns []string
	for _, m := range matchInRe.FindAllStringSubmatch(pythonSource, -1) {
		patterns = append(patterns, m[1])
	}
	for _, m := range matchStrRe.FindAllStringSubmatch(pythonSource, -1) {
		patterns = append(patterns, m[1])
	}

	if len(patterns) == 0 {
		return nil, nil
	}

	// Extract fix from get_new_command return value.
	fix := ""
	// Isolate get_new_command body.
	if idx := strings.Index(pythonSource, "def get_new_command"); idx >= 0 {
		body := pythonSource[idx:]
		if m := getNewCmdReturnRe.FindStringSubmatch(body); m != nil {
			fix = strings.TrimSpace(m[1])
		}
	}

	if fix == "" {
		return nil, nil // no actionable fix extracted; skip rule
	}

	return &toolspec.ErrorPattern{
		Pattern:    strings.Join(patterns, " && "),
		Fix:        fix,
		Source:     "thefuck:" + name,
		Cause:      classifyCause(name, patterns),
		Fixes:      []string{fix},
		Confidence: inferConfidence(patterns),
	}, nil
}

// classifyCause maps rule name and patterns to a cause category.
func classifyCause(name string, patterns []string) string {
	joined := strings.Join(patterns, " ")
	lower := strings.ToLower(joined)

	// Name-based classification.
	for _, kw := range []string{"no_such_file", "cd_", "python_module"} {
		if strings.Contains(name, kw) {
			return "missing_dep"
		}
	}
	for _, kw := range []string{"permission", "sudo"} {
		if strings.Contains(name, kw) {
			return "permission"
		}
	}
	for _, kw := range []string{"git_push", "git_pull"} {
		if strings.Contains(name, kw) {
			return "conflict"
		}
	}

	// Pattern-based classification.
	for _, kw := range []string{"permission denied", "access denied"} {
		if strings.Contains(lower, kw) {
			return "permission"
		}
	}
	for _, kw := range []string{"not found", "no such file", "command not found"} {
		if strings.Contains(lower, kw) {
			return "missing_dep"
		}
	}
	for _, kw := range []string{"already exists", "conflict"} {
		if strings.Contains(lower, kw) {
			return "conflict"
		}
	}

	return "bad_input"
}

// inferConfidence returns a confidence score based on pattern count.
func inferConfidence(patterns []string) float32 {
	switch len(patterns) {
	case 1:
		return 0.9
	case 0:
		return 0.7
	default:
		return 0.8
	}
}

// ParseRules processes a map of rule_name → python_source and returns
// a ToolSpec with all successfully extracted ErrorPatterns.
func ParseRules(toolName string, rules map[string]string) *toolspec.ToolSpec {
	ts := &toolspec.ToolSpec{Name: toolName}

	for name, src := range rules {
		ep, _ := ParseRule(name, src)
		if ep != nil {
			ts.ErrorPatterns = append(ts.ErrorPatterns, *ep)
		}
	}

	return ts
}
