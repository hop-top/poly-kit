package cmdmeta

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// SecretOutputAll is the kit/secret-output value that declares a
// command's whole output secret, for a command that prints the value
// as text and has no fields to name.
const SecretOutputAll = "*"

// SecretOutput is a command's declaration of which part of its output
// is secret. The zero value declares nothing.
type SecretOutput struct {
	// All marks the whole output secret.
	All bool
	// Fields are keys of the command's structured output, matched at
	// any depth, whose values are secret.
	Fields []string
}

// Declared reports whether the command declares any secret output.
func (s SecretOutput) Declared() bool { return s.All || len(s.Fields) > 0 }

// GetSecretOutput decodes the kit/secret-output annotation: "*" for
// the whole output, else a JSON array of output field names. An absent
// annotation is the zero value and a nil error. A value that is
// neither is an error: the declaration is unusable, and the caller
// must treat the output as secret rather than as undeclared.
func GetSecretOutput(cmd *cobra.Command) (SecretOutput, error) {
	if cmd == nil || cmd.Annotations == nil {
		return SecretOutput{}, nil
	}
	v, present := cmd.Annotations[KeySecretOutput]
	if !present {
		return SecretOutput{}, nil
	}
	raw := strings.TrimSpace(v)
	if raw == SecretOutputAll {
		return SecretOutput{All: true}, nil
	}
	var fields []string
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return SecretOutput{}, fmt.Errorf("%s is neither %q nor a JSON array of field names: %w",
			KeySecretOutput, SecretOutputAll, err)
	}
	if len(fields) == 0 {
		return SecretOutput{}, fmt.Errorf("%s names no field", KeySecretOutput)
	}
	for _, f := range fields {
		if f == "" {
			return SecretOutput{}, fmt.Errorf("%s names an empty field", KeySecretOutput)
		}
	}
	return SecretOutput{Fields: fields}, nil
}

// EncodeSecretOutput is the annotation value for fields: "*" when
// there are none (the whole output), else a JSON array.
func EncodeSecretOutput(fields ...string) string {
	if len(fields) == 0 {
		return SecretOutputAll
	}
	raw, _ := json.Marshal(fields)
	return string(raw)
}

// CheckSecretOutput validates cmd's kit/secret-output declaration
// against its output schema and returns what is wrong, or nil. A
// declaration that is unreadable, or that names a field the declared
// output schema has no property for, is an error: a typo must not
// leave a value unredacted without a sound. A declaration naming
// fields without any output schema is an error too, since the output
// then has no structure to find a field in. "*" needs no schema.
func CheckSecretOutput(cmd *cobra.Command) error {
	s, err := GetSecretOutput(cmd)
	if err != nil || len(s.Fields) == 0 {
		return err
	}
	raw, _, ok := GetOutputSchemaJSON(cmd)
	if !ok {
		return fmt.Errorf("%s names fields but the command declares no %s",
			KeySecretOutput, KeyOutputSchema)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		// A malformed schema is reported as such elsewhere.
		return nil
	}
	known := map[string]bool{}
	schemaProperties(doc, known)
	var missing []string
	for _, f := range s.Fields {
		if !known[f] {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	missing = slices.Compact(missing)
	have := make([]string, 0, len(known))
	for k := range known {
		have = append(have, k)
	}
	sort.Strings(have)
	return fmt.Errorf("%s names %q, which the %s does not declare (it declares %s)",
		KeySecretOutput, missing, KeyOutputSchema, strings.Join(have, ", "))
}

// schemaProperties collects the property names of every object schema
// in doc, wherever it sits: the root, items, $defs, composition
// keywords.
func schemaProperties(doc any, out map[string]bool) {
	switch t := doc.(type) {
	case map[string]any:
		if props, ok := t["properties"].(map[string]any); ok {
			for k := range props {
				out[k] = true
			}
		}
		for _, v := range t {
			schemaProperties(v, out)
		}
	case []any:
		for _, v := range t {
			schemaProperties(v, out)
		}
	}
}
