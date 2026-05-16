package cli

import (
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
	"github.com/spf13/cobra"
)

// OutputSchema declares the structured-output schema for a leaf
// command. Adopters opt in via SetOutputSchema; the validator only
// checks the annotation when present.
//
// Type is a zero-value pointer to a struct (with json tags) whose
// shape kit reflects into JSON Schema (Draft 2020-12) at every
// cli.New boot. Because reflection runs each registration, the
// annotation cannot drift from the running binary.
//
// Version is the adopter-declared semver MAJOR.MINOR for the schema.
// Required when Type != nil. Stable across reflection of the same
// Type — the setter warns (does not reject) when reflected schema
// bytes change while Version stays constant.
//
// Example is an optional sample of Type used by `<tool> spec` and by
// help renderers.
type OutputSchema struct {
	Type    any    // pointer to a zero-value struct with json tags; nil disables
	Version string // semver MAJOR.MINOR; required when Type != nil
	Example any    // optional sample of Type
}

// SetOutputSchema attaches a JSON-encoded JSON Schema produced from
// the reflection of s.Type. The schema is stored in the
// kit/output-schema annotation; the version goes into a sibling
// annotation kit/output-schema-version. Nil Type clears the
// annotations.
//
// Returns an error if Type is non-nil and Version is empty, or if
// reflection produces an empty schema (which should not happen for
// well-formed struct types but is detected defensively).
func SetOutputSchema(cmd *cobra.Command, s OutputSchema) error {
	if cmd == nil {
		return fmt.Errorf("SetOutputSchema: nil command")
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	if s.Type == nil {
		delete(cmd.Annotations, kitOutputSchema)
		delete(cmd.Annotations, kitOutputSchemaVersion)
		return nil
	}
	if s.Version == "" {
		return fmt.Errorf("SetOutputSchema: Version required when Type != nil")
	}
	schema := jsonschema.Reflect(s.Type)
	if schema == nil {
		return fmt.Errorf("SetOutputSchema: reflection produced no schema for %T", s.Type)
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("SetOutputSchema: marshal: %w", err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("SetOutputSchema: marshaled schema is empty for %T", s.Type)
	}
	cmd.Annotations[kitOutputSchema] = string(raw)
	cmd.Annotations[kitOutputSchemaVersion] = s.Version
	return nil
}

// GetOutputSchemaJSON returns the raw JSON Schema bytes stored on cmd
// and the declared version. Returns (nil, "", false) when no schema
// is declared.
func GetOutputSchemaJSON(cmd *cobra.Command) (raw json.RawMessage, version string, ok bool) {
	if cmd == nil || cmd.Annotations == nil {
		return nil, "", false
	}
	v, has := cmd.Annotations[kitOutputSchema]
	if !has || v == "" {
		return nil, "", false
	}
	return json.RawMessage(v), cmd.Annotations[kitOutputSchemaVersion], true
}
