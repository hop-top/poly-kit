package pkl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func completionSchema() *Schema {
	return &Schema{
		ModuleName: "TestConfig",
		Fields: []FieldDef{
			{Path: "name", Type: TypeString,
				Description: "application name"},
			{Path: "lang", Type: TypeStringEnum,
				Enum:        []string{"go", "py", "ts"},
				Description: "primary language"},
			{Path: "debug", Type: TypeBool},
			{Path: "derived", Type: TypeString, Computed: true},
		},
	}
}

func TestCompletionKeys_AllFields(t *testing.T) {
	items := CompletionKeys(completionSchema())
	keys := make([]string, len(items))
	for i, it := range items {
		keys[i] = it.Value
	}
	assert.Contains(t, keys, "name")
	assert.Contains(t, keys, "lang")
	assert.Contains(t, keys, "debug")
}

func TestCompletionKeys_SkipsComputed(t *testing.T) {
	items := CompletionKeys(completionSchema())
	for _, it := range items {
		assert.NotEqual(t, "derived", it.Value,
			"computed field must not appear in completions")
	}
}

func TestCompletionKeys_HasDescriptions(t *testing.T) {
	items := CompletionKeys(completionSchema())
	found := false
	for _, it := range items {
		if it.Value == "name" {
			assert.Equal(t, "application name", it.Description)
			found = true
		}
	}
	require.True(t, found, "expected to find 'name' key")
}

func TestCompletionValues_Enum(t *testing.T) {
	items := CompletionValues(completionSchema(), "lang")
	require.Len(t, items, 3)
	vals := make([]string, len(items))
	for i, it := range items {
		vals[i] = it.Value
	}
	assert.Equal(t, []string{"go", "py", "ts"}, vals)
}

func TestCompletionValues_Bool(t *testing.T) {
	items := CompletionValues(completionSchema(), "debug")
	require.Len(t, items, 2)
	assert.Equal(t, "true", items[0].Value)
	assert.Equal(t, "false", items[1].Value)
}

func TestCompletionValues_NoValues(t *testing.T) {
	items := CompletionValues(completionSchema(), "name")
	assert.Nil(t, items)
}

func TestCompletionKeys_NilSchema(t *testing.T) {
	items := CompletionKeys(nil)
	assert.Nil(t, items)
}

func TestCompletionValues_NilSchema(t *testing.T) {
	items := CompletionValues(nil, "name")
	assert.Nil(t, items)
}
