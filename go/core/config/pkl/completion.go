package pkl

// CompletionItem represents a single completion candidate.
type CompletionItem struct {
	Value       string
	Description string
}

// CompletionKeys returns all config key paths from the schema as
// completion items with descriptions. Computed fields are skipped.
func CompletionKeys(schema *Schema) []CompletionItem {
	if schema == nil {
		return nil
	}
	items := make([]CompletionItem, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		if f.Computed {
			continue
		}
		desc := f.Description
		if desc == "" {
			desc = typeLabel(f.Type)
		}
		items = append(items, CompletionItem{
			Value:       f.Path,
			Description: desc,
		})
	}
	return items
}

// CompletionValues returns valid values for a specific key.
// For enum types, returns the enum options.
// For bool types, returns ["true", "false"].
// For other types, returns nil (no value completion).
func CompletionValues(schema *Schema, key string) []CompletionItem {
	if schema == nil {
		return nil
	}
	field := findField(schema, key)
	if field == nil {
		return nil
	}

	switch field.Type {
	case TypeStringEnum:
		items := make([]CompletionItem, 0, len(field.Enum))
		for _, v := range field.Enum {
			items = append(items, CompletionItem{Value: v})
		}
		return items
	case TypeBool:
		return []CompletionItem{
			{Value: "true"},
			{Value: "false"},
		}
	}

	return nil
}

func typeLabel(t FieldType) string {
	switch t {
	case TypeString:
		return "string"
	case TypeInt:
		return "integer"
	case TypeFloat:
		return "float"
	case TypeBool:
		return "boolean"
	case TypeDuration:
		return "duration"
	case TypeStringEnum:
		return "enum"
	case TypeStringList:
		return "string list"
	default:
		return "value"
	}
}
