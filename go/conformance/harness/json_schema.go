package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/spf13/cobra"
	kitcli "hop.top/kit/go/console/cli"
)

// AssertJSONSchema runs cmd with the leaf's format-flag annotation
// (default --format=json) appended to args, parses stdout as JSON,
// and validates it against the schema declared via
// cli.SetOutputSchema (kit/output-schema annotation).
//
// Failure paths:
//
//   - parse error            stdout was not valid JSON
//   - validation error       JSON did not satisfy the schema
//   - version mismatch       schema $id / version did not match
//     the kit/output-schema-version annotation
func AssertJSONSchema(t TB, cmd *cobra.Command, opts ...Option) {
	t.Helper()
	if cmd == nil {
		t.Fatalf("AssertJSONSchema: cmd is nil")
		return
	}
	c := apply(opts)
	leaf := resolveLeaf(cmd, c.args)

	// Locate the schema. WithSchema(...) wins; else read the
	// annotation off the leaf; else surface a programmer error.
	schemaJSON := c.schemaJSON
	declaredVersion := ""
	if len(schemaJSON) == 0 && leaf != nil {
		raw, ver, ok := kitcli.GetOutputSchemaJSON(leaf)
		if ok {
			schemaJSON = []byte(raw)
			declaredVersion = ver
		}
	}
	if len(schemaJSON) == 0 {
		t.Fatalf(
			"AssertJSONSchema: no schema available\n\n" +
				"  leaf has no kit/output-schema annotation and no\n" +
				"  harness.WithSchema(...) override was supplied.\n\n" +
				"  fix: cli.SetOutputSchema(leaf, cli.OutputSchema{...}) at\n" +
				"       registration time, or pass harness.WithSchema(json)",
		)
		return
	}

	// Build the compiled schema.
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	if err := compiler.AddResource("schema.json", bytes.NewReader(schemaJSON)); err != nil {
		t.Errorf("AssertJSONSchema: load schema: %v", err)
		return
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		t.Errorf("AssertJSONSchema: compile schema: %v", err)
		return
	}

	// Version-drift check.
	if declaredVersion != "" {
		var probe map[string]any
		if err := json.Unmarshal(schemaJSON, &probe); err == nil {
			schemaID, _ := probe["$id"].(string)
			schemaVer, _ := probe["version"].(string)
			if schemaID != "" && !strings.Contains(schemaID, declaredVersion) &&
				schemaVer != "" && schemaVer != declaredVersion {
				t.Errorf(
					"AssertJSONSchema: schema version mismatch\n\n"+
						"  kit/output-schema-version annotation: %s\n"+
						"  schema document $id/version:          %s / %s\n"+
						"  fix: bump the annotation or refresh the schema",
					declaredVersion, schemaID, schemaVer)
				return
			}
		}
	}

	// Append the format flag.
	formatFlag := "--format=json"
	if leaf != nil {
		formatFlag = kitcli.GetFormatFlag(leaf)
	}
	c.args = appendUnique(c.args, formatFlag)

	res := runCaptured(c, cmd)
	if res.runErr != nil {
		t.Errorf(
			"AssertJSONSchema: invocation failed: %v\n  stderr: %s",
			res.runErr, truncate(res.stderr.String(), 500))
		return
	}
	stdoutBytes := res.stdout.Bytes()

	var doc any
	if err := json.Unmarshal(stdoutBytes, &doc); err != nil {
		t.Errorf(
			"AssertJSONSchema: stdout is not valid JSON\n\n  parse error: %v\n  stdout (first 500 bytes): %s",
			err, truncate(string(stdoutBytes), 500))
		return
	}

	if err := schema.Validate(doc); err != nil {
		// Pull validation errors out for a structured message.
		var ve *jsonschema.ValidationError
		var lines []string
		if asValidationError(err, &ve) {
			for _, c := range flattenValidationErrors(ve) {
				lines = append(lines, "  ✗ "+c)
			}
		} else {
			lines = append(lines, "  ✗ "+err.Error())
		}
		t.Errorf(
			"AssertJSONSchema: %d validation error(s)\n\n  stdout: %s\n\n%s",
			len(lines), truncate(string(stdoutBytes), 500),
			strings.Join(lines, "\n"))
	}
}

// asValidationError mirrors errors.As but works around the fact
// that jsonschema's ValidationError implements Unwrap chains we
// already walked.
func asValidationError(err error, target **jsonschema.ValidationError) bool {
	if v, ok := err.(*jsonschema.ValidationError); ok {
		*target = v
		return true
	}
	return false
}

// flattenValidationErrors walks ve.Causes recursively and returns
// one "path: message" line per leaf error. Top-level errors with no
// causes are surfaced directly.
func flattenValidationErrors(ve *jsonschema.ValidationError) []string {
	if ve == nil {
		return nil
	}
	if len(ve.Causes) == 0 {
		path := ve.InstanceLocation
		if path == "" {
			path = "/"
		}
		return []string{fmt.Sprintf("%s: %s", path, ve.Message)}
	}
	var out []string
	for _, c := range ve.Causes {
		out = append(out, flattenValidationErrors(c)...)
	}
	return out
}
