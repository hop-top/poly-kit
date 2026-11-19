package prompt

import (
	"fmt"
	"strings"
)

// dslTypes maps a DSL type to its JSON Schema; schemaFor returns a copy.
var dslTypes = map[string]func() map[string]any{
	"str":   func() map[string]any { return map[string]any{"type": "string"} },
	"int":   func() map[string]any { return map[string]any{"type": "integer"} },
	"float": func() map[string]any { return map[string]any{"type": "number"} },
	"bool":  func() map[string]any { return map[string]any{"type": "boolean"} },
	"[]str": func() map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	},
	"[]int": func() map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}
	},
}

// CompileDSL compiles a field list into a JSON Schema object.
//
// Format: "field1, field2 type, ...". The default type is str; the types
// are str, int, float, bool, []str and []int. Every field is required and
// no other property is allowed. Empty segments are skipped; a repeated
// field, a segment with more than a name and a type, or an unknown type is
// an error.
//
//	"name, age int, email"     three required fields: string, integer, string
//	"tags []str, score float"  an array of strings and a number
func CompileDSL(dsl string) (map[string]any, error) {
	dsl = strings.TrimSpace(dsl)
	if dsl == "" {
		return nil, fmt.Errorf("%w: empty DSL", ErrInvalid)
	}
	properties := map[string]any{}
	var required []string
	for _, part := range strings.Split(dsl, ",") {
		tokens := strings.Fields(part)
		if len(tokens) == 0 {
			continue
		}
		if len(tokens) > 2 {
			return nil, fmt.Errorf("%w: field %q: want \"name [type]\"", ErrInvalid, strings.TrimSpace(part))
		}
		name, typeName := tokens[0], "str"
		if len(tokens) == 2 {
			typeName = tokens[1]
		}
		mk, ok := dslTypes[typeName]
		if !ok {
			return nil, fmt.Errorf("%w: unknown type %q for field %q", ErrInvalid, typeName, name)
		}
		if _, dup := properties[name]; dup {
			return nil, fmt.Errorf("%w: field %q repeated", ErrInvalid, name)
		}
		properties[name] = mk()
		required = append(required, name)
	}
	if len(properties) == 0 {
		return nil, fmt.Errorf("%w: no fields in DSL", ErrInvalid)
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}, nil
}
