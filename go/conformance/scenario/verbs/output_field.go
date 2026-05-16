package verbs

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/tidwall/gjson"
	"gopkg.in/yaml.v3"
)

// output_field_equals: { path: string, value: any, parse?: "json"|"yaml" }
// output_field_present: { path: string, parse?: "json"|"yaml" }
// output_field_count: { path: string, equals: int }

func init() {
	register(&Entry{
		Kind:     KindOutputFieldEquals,
		Validate: validateOutputFieldEquals,
		Evaluate: evalOutputFieldEquals,
	})
	register(&Entry{
		Kind:     KindOutputFieldPresent,
		Validate: validateOutputFieldPresent,
		Evaluate: evalOutputFieldPresent,
	})
	register(&Entry{
		Kind:     KindOutputFieldCount,
		Validate: validateOutputFieldCount,
		Evaluate: evalOutputFieldCount,
	})
}

func validateOutputFieldEquals(args map[string]any) []string {
	var out []string
	if s, ok := args["path"].(string); !ok || s == "" {
		out = append(out, "path must be a non-empty string")
	}
	if _, ok := args["value"]; !ok {
		out = append(out, "missing required key value")
	}
	out = append(out, validateParse(args)...)
	return out
}

func validateOutputFieldPresent(args map[string]any) []string {
	var out []string
	if s, ok := args["path"].(string); !ok || s == "" {
		out = append(out, "path must be a non-empty string")
	}
	out = append(out, validateParse(args)...)
	return out
}

func validateOutputFieldCount(args map[string]any) []string {
	var out []string
	if s, ok := args["path"].(string); !ok || s == "" {
		out = append(out, "path must be a non-empty string")
	}
	if _, ok := args["equals"]; !ok {
		out = append(out, "missing required key equals")
	} else if _, ok := asInt(args["equals"]); !ok {
		out = append(out, "equals must be an integer")
	}
	out = append(out, validateParse(args)...)
	return out
}

func validateParse(args map[string]any) []string {
	if raw, ok := args["parse"]; ok {
		s, ok := raw.(string)
		if !ok || (s != "json" && s != "yaml") {
			return []string{"parse must be \"json\" or \"yaml\""}
		}
	}
	return nil
}

// parseStdout decodes vctx.Capture.Stdout per args["parse"] (default
// "json"). Returns (rawJSON, ok). For yaml, the bytes are decoded
// to map[string]any and re-marshaled to JSON so gjson can walk
// them. Empty stdout returns "" with ok=true (gjson returns Null on
// "").
func parseStdoutAsJSON(args map[string]any, stdout []byte) (string, error) {
	parse, _ := args["parse"].(string)
	if parse == "" {
		parse = "json"
	}
	if len(stdout) == 0 {
		return "", nil
	}
	switch parse {
	case "json":
		// Validate it's parseable.
		if !gjson.ValidBytes(stdout) {
			return "", fmt.Errorf("stdout is not valid JSON")
		}
		return string(stdout), nil
	case "yaml":
		var v any
		if err := yaml.Unmarshal(stdout, &v); err != nil {
			return "", fmt.Errorf("yaml parse: %w", err)
		}
		// gjson needs JSON bytes; convert yaml.MapSlice → map → JSON.
		v = ymlToAny(v)
		j, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("yaml→json: %w", err)
		}
		return string(j), nil
	default:
		return "", fmt.Errorf("unsupported parse mode %q", parse)
	}
}

// ymlToAny normalises yaml.v3-decoded values (which may contain
// map[any]any for nested maps) into JSON-friendly map[string]any.
func ymlToAny(v any) any {
	switch x := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[fmt.Sprintf("%v", k)] = ymlToAny(vv)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = ymlToAny(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = ymlToAny(vv)
		}
		return out
	}
	return v
}

func evalOutputFieldEquals(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	path, _ := spec.Args["path"].(string)
	want := spec.Args["value"]
	raw, err := parseStdoutAsJSON(spec.Args, vctx.Capture.Stdout)
	if err != nil {
		return Ungradable("output_field_equals: " + err.Error())
	}
	if raw == "" {
		return Fail(nil, want, "stdout empty")
	}
	got := gjson.Get(raw, normalizeGJSONPath(path))
	if !got.Exists() {
		return Fail(nil, want, fmt.Sprintf("path %q not present", path))
	}
	gotVal := got.Value()
	if jsonEqual(gotVal, want) {
		return EvalResult{Status: StatusPass, Observed: gotVal, Expected: want}
	}
	return Fail(gotVal, want, fmt.Sprintf("path %q: %v != %v", path, gotVal, want))
}

func evalOutputFieldPresent(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	path, _ := spec.Args["path"].(string)
	raw, err := parseStdoutAsJSON(spec.Args, vctx.Capture.Stdout)
	if err != nil {
		return Ungradable("output_field_present: " + err.Error())
	}
	if raw == "" {
		return Fail(nil, "present", "stdout empty")
	}
	if gjson.Get(raw, normalizeGJSONPath(path)).Exists() {
		return EvalResult{Status: StatusPass, Observed: path, Expected: "present"}
	}
	return Fail(nil, "present", fmt.Sprintf("path %q not present", path))
}

func evalOutputFieldCount(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	path, _ := spec.Args["path"].(string)
	want, _ := asInt(spec.Args["equals"])
	raw, err := parseStdoutAsJSON(spec.Args, vctx.Capture.Stdout)
	if err != nil {
		return Ungradable("output_field_count: " + err.Error())
	}
	if raw == "" {
		return Fail(0, want, "stdout empty")
	}
	r := gjson.Get(raw, normalizeGJSONPath(path))
	if !r.Exists() {
		return Fail(0, want, fmt.Sprintf("path %q not present", path))
	}
	count := 0
	if r.IsArray() {
		count = len(r.Array())
	} else if r.IsObject() {
		count = len(r.Map())
	} else {
		count = 1
	}
	if count == want {
		return EvalResult{Status: StatusPass, Observed: count, Expected: want}
	}
	return Fail(count, want, fmt.Sprintf("path %q count %d != %d", path, count, want))
}

// normalizeGJSONPath accepts both JSONPath-style "$.foo.bar" and
// gjson-native "foo.bar" by stripping a leading "$." prefix. gjson
// uses dotted paths natively; the leading $ is JSONPath syntax users
// reasonably expect.
func normalizeGJSONPath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "$.") {
		return p[2:]
	}
	if strings.HasPrefix(p, "$") {
		return strings.TrimPrefix(p, "$")
	}
	return p
}

// jsonEqual compares two values for equality with YAML number
// normalization: yaml.v3 decodes integers as int; JSON decodes them
// as float64. We coerce both sides to float64 when either is numeric
// to avoid spurious mismatches.
func jsonEqual(a, b any) bool {
	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			return af == bf
		}
	}
	return reflect.DeepEqual(a, b)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	}
	return 0, false
}
