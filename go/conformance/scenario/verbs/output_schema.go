package verbs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// output_schema_matches: { schema_ref?: string, schema?: object }
// Exactly one of schema_ref or schema must be set.
//
// schema_ref is treated as opaque by the library — production
// callers (the svc) resolve refs to schema bodies before invoking
// the grader. The library implements the inline-schema path only.

func init() {
	register(&Entry{
		Kind:     KindOutputSchemaMatches,
		Validate: validateOutputSchemaMatches,
		Evaluate: evalOutputSchemaMatches,
	})
}

func validateOutputSchemaMatches(args map[string]any) []string {
	_, hasRef := args["schema_ref"]
	_, hasInline := args["schema"]
	if !hasRef && !hasInline {
		return []string{"exactly one of schema_ref or schema required"}
	}
	if hasRef && hasInline {
		return []string{"schema_ref and schema are mutually exclusive"}
	}
	if hasRef {
		if s, ok := args["schema_ref"].(string); !ok || s == "" {
			return []string{"schema_ref must be a non-empty string"}
		}
	}
	return nil
}

func evalOutputSchemaMatches(_ context.Context, spec AssertionSpec, vctx VerbContext) EvalResult {
	if _, ok := spec.Args["schema_ref"]; ok {
		// schema_ref must be pre-resolved by the caller (the svc).
		// At library level we surface ungradable rather than fail.
		return Ungradable("output_schema_matches: schema_ref provided but library does not resolve refs; caller must pre-resolve")
	}
	schemaRaw, ok := spec.Args["schema"]
	if !ok {
		return Ungradable("output_schema_matches: no schema provided")
	}
	// Marshal the YAML-decoded schema map → JSON for the jsonschema
	// library.
	schemaRaw = ymlToAny(schemaRaw)
	schemaJSON, err := json.Marshal(schemaRaw)
	if err != nil {
		return Ungradable(fmt.Sprintf("output_schema_matches: marshal schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("inline.json", bytes.NewReader(schemaJSON)); err != nil {
		return Ungradable(fmt.Sprintf("output_schema_matches: add schema resource: %v", err))
	}
	schema, err := compiler.Compile("inline.json")
	if err != nil {
		return Ungradable(fmt.Sprintf("output_schema_matches: compile schema: %v", err))
	}
	if len(vctx.Capture.Stdout) == 0 {
		return Fail(nil, "schema-valid", "stdout empty")
	}
	var doc any
	if err := json.Unmarshal(vctx.Capture.Stdout, &doc); err != nil {
		return Fail(nil, "schema-valid", fmt.Sprintf("stdout is not JSON: %v", err))
	}
	if err := schema.Validate(doc); err != nil {
		return Fail(doc, "schema-valid", fmt.Sprintf("schema validation failed: %v", err))
	}
	return EvalResult{Status: StatusPass, Expected: "schema-valid"}
}
