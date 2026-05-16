package verbs

import (
	"context"
	"fmt"
	"strings"

	"hop.top/kit/go/console/cli/conformance/harness/classifier"
	"hop.top/kit/go/console/cli/conformance/harness/diff"
)

// cassette_must_contain: { op_class, adapter?, match? }
// cassette_must_not_contain: same shape
// cassette_diff_equals: { against: step-id, expect: "empty"|"subset" }
// cassette_diff_empty: -

func init() {
	register(&Entry{
		Kind:     KindCassetteMustContain,
		Validate: validateCassetteContain,
		Evaluate: evalCassetteMustContain,
	})
	register(&Entry{
		Kind:     KindCassetteMustNotContain,
		Validate: validateCassetteContain,
		Evaluate: evalCassetteMustNotContain,
	})
	register(&Entry{
		Kind:     KindCassetteDiffEquals,
		Validate: validateCassetteDiffEquals,
		Evaluate: evalCassetteDiffEquals,
	})
	register(&Entry{
		Kind:     KindCassetteDiffEmpty,
		Validate: nil,
		Evaluate: evalCassetteDiffEmpty,
	})
}

func validateCassetteContain(args map[string]any) []string {
	var out []string
	raw, ok := args["op_class"]
	if !ok {
		out = append(out, "missing required key op_class")
	} else {
		s, ok := raw.(string)
		if !ok {
			out = append(out, "op_class must be a string")
		} else {
			switch s {
			case "any", "mutating", "reading", "destructive":
			default:
				out = append(out, fmt.Sprintf("op_class %q not in {any,mutating,reading,destructive}", s))
			}
		}
	}
	if raw, ok := args["adapter"]; ok {
		s, ok := raw.(string)
		if !ok {
			out = append(out, "adapter must be a string")
		} else {
			switch s {
			case "", "http", "sql", "redis", "grpc", "exec", "fs":
			default:
				out = append(out, fmt.Sprintf("adapter %q unknown", s))
			}
		}
	}
	if raw, ok := args["match"]; ok {
		if _, mok := raw.(map[string]any); !mok {
			// yaml may decode as map[any]any
			if _, mok2 := raw.(map[any]any); !mok2 {
				out = append(out, "match must be a mapping")
			}
		}
	}
	return out
}

func validateCassetteDiffEquals(args map[string]any) []string {
	var out []string
	if s, ok := args["against"].(string); !ok || s == "" {
		out = append(out, "against must be a non-empty step id")
	}
	expect, ok := args["expect"].(string)
	if !ok {
		out = append(out, "expect required (string)")
	} else {
		switch expect {
		case "empty":
		case "subset":
			out = append(out, "expect=subset is reserved; v1 implements expect=empty only")
		default:
			out = append(out, fmt.Sprintf("expect %q unknown; v1 supports \"empty\" only", expect))
		}
	}
	return out
}

// matchesOpClass reports whether class fits the op_class predicate.
func matchesOpClass(class classifier.Class, op string) bool {
	switch op {
	case "any":
		return true
	case "reading":
		return class == classifier.ClassRead
	case "mutating":
		return class.IsMutating()
	case "destructive":
		return class == classifier.ClassDestructive
	}
	return false
}

// matchesPredicate inspects the recorded request payload against the
// closed-key match predicate from design §4. Returns true on a hit.
func matchesPredicate(adapter string, payload map[string]any, match map[string]any) bool {
	if len(match) == 0 {
		return true
	}
	get := func(k string) (string, bool) {
		v, ok := payload[k]
		if !ok {
			return "", false
		}
		s, ok := v.(string)
		return s, ok
	}
	for k, raw := range match {
		want, ok := raw.(string)
		if !ok {
			return false
		}
		switch k {
		case "query_substring":
			if adapter != "sql" {
				return false
			}
			q, _ := get("query")
			if !strings.Contains(q, want) {
				return false
			}
		case "url_substring":
			if adapter != "http" {
				return false
			}
			u, _ := get("url")
			if !strings.Contains(u, want) {
				return false
			}
		case "method":
			if adapter != "http" {
				return false
			}
			m, _ := get("method")
			if !strings.EqualFold(m, want) {
				return false
			}
		case "command":
			if adapter != "redis" {
				return false
			}
			cmd, _ := get("command")
			if !strings.EqualFold(cmd, want) {
				return false
			}
		case "argv_substring":
			if adapter != "exec" {
				return false
			}
			argv, _ := payload["argv"].([]any)
			joined := joinArgv(argv)
			if !strings.Contains(joined, want) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func joinArgv(argv []any) string {
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		parts = append(parts, fmt.Sprintf("%v", a))
	}
	return strings.Join(parts, " ")
}

// cassetteSearch walks the on-step's cassette dir applying the
// op_class + adapter + match predicate. Returns the first matching
// interaction summary (for diagnostics) or "" if none matched.
func cassetteSearch(dir string, args map[string]any) (summary string, hit bool, err error) {
	if dir == "" {
		return "", false, nil
	}
	items, err := diff.List(dir)
	if err != nil {
		return "", false, err
	}
	op, _ := args["op_class"].(string)
	wantAdapter, _ := args["adapter"].(string)
	match := coerceMap(args["match"])
	for _, it := range items {
		if wantAdapter != "" && it.Adapter != wantAdapter {
			continue
		}
		// Classify via the dispatcher's payload-fallback path; we
		// don't have typed xrr.Request objects at this layer, just
		// the YAML-decoded map. The dispatcher only knows the
		// fs-payload fallback by adapter — we re-implement adapter-
		// specific classification at the payload level here.
		cls := classifyPayload(it.Adapter, it.ReqPayload)
		if !matchesOpClass(cls, op) {
			continue
		}
		if !matchesPredicate(it.Adapter, it.ReqPayload, match) {
			continue
		}
		return it.Summary, true, nil
	}
	return "", false, nil
}

// classifyPayload mirrors classifier dispatch logic but operates on
// the YAML-decoded request payload rather than the typed xrr
// request. We reconstruct just enough to drive each adapter's
// classifier.
func classifyPayload(adapter string, payload map[string]any) classifier.Class {
	if payload == nil {
		return classifier.ClassUnknown
	}
	switch adapter {
	case "sql":
		q, _ := payload["query"].(string)
		return classifier.ClassifySQLQuery(q)
	case "redis":
		cmd, _ := payload["command"].(string)
		var args []string
		if a, ok := payload["args"].([]any); ok {
			for _, x := range a {
				args = append(args, fmt.Sprintf("%v", x))
			}
		}
		return classifier.ClassifyRedisCmd(cmd, args)
	case "http":
		// Heuristic: GET/HEAD = read, everything else = write.
		method, _ := payload["method"].(string)
		switch strings.ToUpper(method) {
		case "GET", "HEAD", "OPTIONS":
			return classifier.ClassRead
		case "DELETE":
			return classifier.ClassDestructive
		case "":
			return classifier.ClassUnknown
		default:
			return classifier.ClassWrite
		}
	case "grpc":
		method, _ := payload["method"].(string)
		return classifier.ClassifyGRPCMethod("", method)
	case "exec":
		argv, _ := payload["argv"].([]any)
		parts := make([]string, 0, len(argv))
		for _, a := range argv {
			parts = append(parts, fmt.Sprintf("%v", a))
		}
		return classifier.DefaultExecClassifier(parts)
	case "fs":
		op, _ := payload["op"].(string)
		return classifier.ClassifyFSOp(op)
	}
	return classifier.ClassUnknown
}

func coerceMap(raw any) map[string]any {
	if raw == nil {
		return nil
	}
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	if m, ok := raw.(map[any]any); ok {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[fmt.Sprintf("%v", k)] = v
		}
		return out
	}
	return nil
}

func evalCassetteMustContain(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	summary, hit, err := cassetteSearch(vctx.Capture.CassetteDir, spec.Args)
	if err != nil {
		return Ungradable("cassette_must_contain: " + err.Error())
	}
	if hit {
		return EvalResult{Status: StatusPass, Observed: summary, Expected: spec.Args}
	}
	return Fail(nil, spec.Args, "no cassette interaction matched")
}

func evalCassetteMustNotContain(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	summary, hit, err := cassetteSearch(vctx.Capture.CassetteDir, spec.Args)
	if err != nil {
		return Ungradable("cassette_must_not_contain: " + err.Error())
	}
	if hit {
		return Fail(summary, "no match", fmt.Sprintf("unexpected cassette interaction: %s", summary))
	}
	return EvalResult{Status: StatusPass, Expected: spec.Args}
}

func evalCassetteDiffEquals(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	against, _ := spec.Args["against"].(string)
	other, ok := vctx.OtherCaptures[against]
	if !ok {
		return Ungradable(fmt.Sprintf("cassette_diff_equals: against=%q has no recorded capture", against))
	}
	d, err := diff.Cassettes(other.CassetteDir, vctx.Capture.CassetteDir)
	if err != nil {
		return Ungradable("cassette_diff_equals: " + err.Error())
	}
	if d.Empty() {
		return EvalResult{Status: StatusPass, Expected: "empty"}
	}
	return Fail(d.Format(nil), "empty", fmt.Sprintf("%d cassette diff entries", len(d.Entries)))
}

func evalCassetteDiffEmpty(_ context.Context, _ AssertionSpec, vctx VerbContext) EvalResult {
	// Best-effort: the on-step's CassetteDir must itself diff cleanly
	// against itself. In practice cassette_diff_empty is configured
	// by the adopter to compare apply vs replay runs and v1 has no
	// dedicated channel for that beyond cassette_diff_equals. We
	// surface that the on-step has no recorded interactions when its
	// cassette dir is empty; otherwise pass (interactions present,
	// no diff against self).
	items, err := diff.List(vctx.Capture.CassetteDir)
	if err != nil {
		return Ungradable("cassette_diff_empty: " + err.Error())
	}
	if len(items) == 0 {
		return EvalResult{Status: StatusPass, Expected: "empty", Observed: 0}
	}
	// If the adopter recorded interactions, the only way "diff is
	// empty" makes sense without an against-step is to surface
	// ungradable so the caller upgrades to cassette_diff_equals
	// with explicit against:.
	return Ungradable("cassette_diff_empty without against: requires apply+replay cassette pairs; use cassette_diff_equals")
}
