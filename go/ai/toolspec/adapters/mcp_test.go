package adapters

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
)

// --- Adapter metadata --------------------------------------------

func TestMCP_Metadata(t *testing.T) {
	a := MCP()
	assert.Equal(t, "mcp", a.Name())
	assert.Equal(t, []string{"prompt"}, a.Aliases())
	assert.Equal(t, "application/json", a.ContentType())
	assert.NotEmpty(t, a.Description())
}

func TestMCP_RegistersInRegistry(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(MCP()))
	assert.Equal(t, MCP(), r.Lookup("mcp"))
	assert.Equal(t, MCP(), r.Lookup("prompt"), "prompt alias resolves")
}

// --- Render: envelope shape --------------------------------------

func TestMCP_Render_NilSpec(t *testing.T) {
	var buf bytes.Buffer
	err := MCP().Render(&buf, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil spec")
}

func TestMCP_Render_EnvelopeShape(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec))

	var env map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))

	assert.Equal(t, "mytool", env["name"])
	assert.NotEmpty(t, env["description"])

	schema, ok := env["inputSchema"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", schema["type"])

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "action", "action property always present")

	required, ok := schema["required"].([]any)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(required), 1)
	assert.Equal(t, "action", required[0], "action is always required")
}

func TestMCP_Render_ActionEnumFromCommands(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec))

	var env map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	props := env["inputSchema"].(map[string]any)["properties"].(map[string]any)
	action := props["action"].(map[string]any)

	enum, ok := action["enum"].([]any)
	require.True(t, ok, "action enum present")
	require.Len(t, enum, 3)
	values := []string{enum[0].(string), enum[1].(string), enum[2].(string)}
	assert.ElementsMatch(t, []string{"list", "create", "delete"}, values)
}

func TestMCP_Render_NoCommandsNoEnum(t *testing.T) {
	// A spec with zero commands shouldn't crash; the action enum
	// should be omitted (just type+description).
	spec := &toolspec.ToolSpec{Name: "mytool"}
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec))

	var env map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	props := env["inputSchema"].(map[string]any)["properties"].(map[string]any)
	action := props["action"].(map[string]any)
	_, hasEnum := action["enum"]
	assert.False(t, hasEnum, "no commands → no enum field")
}

// --- Render: flag projection -------------------------------------

func TestMCP_Render_FlagPropertiesPresent(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec))

	var env map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	props := env["inputSchema"].(map[string]any)["properties"].(map[string]any)
	assert.Contains(t, props, "config")
	assert.Contains(t, props, "verbose")

	configProp := props["config"].(map[string]any)
	assert.Equal(t, "string", configProp["type"])
	assert.Equal(t, "config path", configProp["description"])

	verboseProp := props["verbose"].(map[string]any)
	assert.Equal(t, "boolean", verboseProp["type"], "bool → boolean in JSON Schema")
}

func TestMCP_Render_TypeMapping(t *testing.T) {
	cases := map[string]string{
		"bool":        "boolean",
		"int":         "integer",
		"int64":       "integer",
		"uint":        "integer",
		"count":       "integer",
		"float64":     "number",
		"stringSlice": "array",
		"stringArray": "array",
		"string":      "string",
		"":            "string", // empty default
		"unknown":     "string", // fallthrough
	}
	for pflagType, jsonType := range cases {
		t.Run(pflagType, func(t *testing.T) {
			spec := &toolspec.ToolSpec{
				Name:  "mytool",
				Flags: []toolspec.Flag{{Name: "x", Type: pflagType}},
			}
			var buf bytes.Buffer
			require.NoError(t, MCP().Render(&buf, spec))

			var env map[string]any
			require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
			props := env["inputSchema"].(map[string]any)["properties"].(map[string]any)
			x := props["x"].(map[string]any)
			assert.Equal(t, jsonType, x["type"])
		})
	}
}

func TestMCP_Render_ArrayHasItemsType(t *testing.T) {
	// Array-typed flags need an "items" sub-schema for MCP-side
	// validators to accept them.
	spec := &toolspec.ToolSpec{
		Name:  "mytool",
		Flags: []toolspec.Flag{{Name: "tags", Type: "stringSlice"}},
	}
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec))

	var env map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	props := env["inputSchema"].(map[string]any)["properties"].(map[string]any)
	tags := props["tags"].(map[string]any)
	items, ok := tags["items"].(map[string]any)
	require.True(t, ok, "array flag has items field")
	assert.Equal(t, "string", items["type"])
}

// --- Render: deprecation ------------------------------------------

func TestMCP_Render_DeprecatedCommandIncludedByDefault(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Commands: []toolspec.Command{
			{Name: "current"},
			{Name: "old", Deprecated: true},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec))

	var env map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	props := env["inputSchema"].(map[string]any)["properties"].(map[string]any)
	enum := props["action"].(map[string]any)["enum"].([]any)
	require.Len(t, enum, 2)
}

func TestMCP_Render_DeprecatedCommandFiltered(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Commands: []toolspec.Command{
			{Name: "current"},
			{Name: "old", Deprecated: true},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec, WithIncludeDeprecated(false)))

	var env map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	props := env["inputSchema"].(map[string]any)["properties"].(map[string]any)
	enum := props["action"].(map[string]any)["enum"].([]any)
	require.Len(t, enum, 1)
	assert.Equal(t, "current", enum[0])
}

func TestMCP_Render_DeprecatedFlagFiltered(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Flags: []toolspec.Flag{
			{Name: "current"},
			{Name: "old", Deprecated: true},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec, WithIncludeDeprecated(false)))

	var env map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	props := env["inputSchema"].(map[string]any)["properties"].(map[string]any)
	assert.Contains(t, props, "current")
	assert.NotContains(t, props, "old")
}

// --- Custom: description override --------------------------------

func TestMCP_Render_DescriptionOverride(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec,
		WithCustom(CustomKeyMCPDescription, "My amazing tool")))

	var env map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	assert.Equal(t, "My amazing tool", env["description"])
}

func TestMCP_Render_DescriptionFallback(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec))

	var env map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	assert.Equal(t, "mytool CLI tool", env["description"])
}

// --- Custom: required flags --------------------------------------

func TestMCP_Render_AdditionalRequiredFlags(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name:     "mytool",
		Commands: []toolspec.Command{{Name: "do"}},
		Flags:    []toolspec.Flag{{Name: "task_id", Type: "string"}},
	}
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec,
		WithCustom(CustomKeyMCPRequiredFlags, []string{"task_id"})))

	var env map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	required := env["inputSchema"].(map[string]any)["required"].([]any)
	require.Len(t, required, 2)
	assert.Equal(t, "action", required[0])
	assert.Equal(t, "task_id", required[1])
}

// --- Pretty-printing ---------------------------------------------

func TestMCP_Render_PrettyDefault(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec))
	// Indented output contains newlines and 2-space indentation.
	out := buf.String()
	assert.Contains(t, out, "\n  ", "default pretty-print uses 2-space indent")
}

func TestMCP_Render_Compact(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec, WithPretty(false)))
	out := buf.String()
	// Compact JSON has no leading-space indentation; just one
	// trailing newline from json.Encoder.
	assert.NotContains(t, out, "\n  ", "compact mode has no indent")
}
