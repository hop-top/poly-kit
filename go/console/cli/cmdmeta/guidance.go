package cmdmeta

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

// Example is one entry in a leaf command's kit/examples annotation.
// Title is a short human label; Command is the literal invocation
// (e.g. "kit foo create --name=bar"); Output is an optional embedded
// snippet illustrating expected output.
type Example struct {
	Title   string `json:"title" yaml:"title"`
	Command string `json:"command" yaml:"command"`
	Output  string `json:"output,omitempty" yaml:"output,omitempty"`
}

// NextStep is one entry in a leaf command's kit/next-steps
// annotation. Surfaced to agents post-invocation to chain follow-up
// commands. When is a free-form condition string ("on success",
// "when no results"); Suggest is the literal next invocation;
// Reason explains why the suggestion fits.
type NextStep struct {
	When    string `json:"when,omitempty" yaml:"when,omitempty"`
	Suggest string `json:"suggest" yaml:"suggest"`
	Reason  string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// GetExamples decodes the kit/examples annotation into []Example.
// Returns (nil, false) when the annotation is absent or malformed;
// callers that want decode errors should use json.Unmarshal directly
// on the annotation bytes.
func GetExamples(cmd *cobra.Command) ([]Example, bool) {
	return decodeList[Example](cmd, KeyExamples)
}

// GetNextSteps decodes the kit/next-steps annotation into []NextStep.
// Same semantics as GetExamples.
func GetNextSteps(cmd *cobra.Command) ([]NextStep, bool) {
	return decodeList[NextStep](cmd, KeyNextSteps)
}

// decodeList unmarshals the JSON list stored under key. A missing,
// empty, or malformed value is reported as (nil, false) rather than
// as an error: guidance is advisory metadata, and a decode failure
// must not break the command it decorates.
func decodeList[T any](cmd *cobra.Command, key string) ([]T, bool) {
	raw := read(cmd, key)
	if raw == "" {
		return nil, false
	}
	var out []T
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, false
	}
	return out, true
}

// GetOutputSchemaJSON returns the raw JSON Schema bytes stored on cmd
// and the declared version. Returns (nil, "", false) when no schema
// is declared.
func GetOutputSchemaJSON(cmd *cobra.Command) (raw json.RawMessage, version string, ok bool) {
	v := read(cmd, KeyOutputSchema)
	if v == "" {
		return nil, "", false
	}
	return json.RawMessage(v), read(cmd, KeyOutputSchemaVersion), true
}
