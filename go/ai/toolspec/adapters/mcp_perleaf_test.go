package adapters

// Per-leaf MCP shape — the adapter's default rendering.
//
// mcp_test.go covers the opt-in action-enum back-compat path; this
// file covers the shape `<tool> spec --format mcp` emits when nobody
// asks for anything special, which is the one that has to agree with
// the live MCP server. Cross-package agreement with that server is
// asserted in go/transport/cmdsurface's static/live parity test; the
// tests here pin this side's own behavior.

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
)

// nestedSpec is a tree with depth, a required flag, a persistent
// root flag and a deprecated leaf — enough surface to exercise every
// per-leaf decision the adapter makes.
func nestedSpec() *toolspec.ToolSpec {
	return &toolspec.ToolSpec{
		Name:          "mytool",
		SchemaVersion: "1.0",
		Commands: []toolspec.Command{
			{
				Name:  "widget",
				Short: "Manage widgets",
				Children: []toolspec.Command{
					{
						Name:  "add",
						Short: "Add a widget",
						Flags: []toolspec.Flag{
							{Name: "name", Type: "string", Description: "widget name", Required: true},
							{Name: "tag", Type: "stringSlice", Description: "tag list"},
						},
					},
					{
						Name:  "remove",
						Short: "Remove a widget",
					},
				},
			},
			{
				Name:  "ping",
				Short: "Ping the server",
			},
			{
				Name:       "legacy",
				Short:      "Old command",
				Deprecated: true,
			},
		},
		Flags: []toolspec.Flag{
			{Name: "verbose", Type: "bool", Description: "verbose"},
		},
	}
}

func renderPerLeaf(t *testing.T, spec *toolspec.ToolSpec, opts ...RenderOption) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec, opts...))

	var payload struct {
		Tools []map[string]any `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &payload))
	return payload.Tools
}

func toolNamesOf(tools []map[string]any) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		out = append(out, name)
	}
	return out
}

func findTool(tools []map[string]any, name string) map[string]any {
	for _, tool := range tools {
		if n, _ := tool["name"].(string); n == name {
			return tool
		}
	}
	return nil
}

// TestMCPPerLeaf_IsTheDefaultShape is the statement of intent: a
// render with no options produces the tools array, not the historical
// single envelope.
func TestMCPPerLeaf_IsTheDefaultShape(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, nestedSpec()))

	var env map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))
	assert.Contains(t, env, "tools", "default render is a tools array")
	assert.NotContains(t, env, "inputSchema",
		"default render is not the single action-enum envelope")
}

// TestMCPPerLeaf_OneToolPerLeaf pins the leaf selection: intermediate
// nodes (widget) are not tools, their children are, and names are the
// dotted path below the root.
func TestMCPPerLeaf_OneToolPerLeaf(t *testing.T) {
	tools := renderPerLeaf(t, nestedSpec())
	assert.ElementsMatch(t,
		[]string{"widget.add", "widget.remove", "ping", "legacy"},
		toolNamesOf(tools),
		"every leaf is a tool; the group command is not")
}

// TestMCPPerLeaf_NameOmitsRootPrefix guards the wire contract the
// live server sets: tool names start below the root, so a static
// name is callable against a live server unchanged.
func TestMCPPerLeaf_NameOmitsRootPrefix(t *testing.T) {
	tools := renderPerLeaf(t, nestedSpec())
	for _, name := range toolNamesOf(tools) {
		assert.NotContains(t, name, "mytool.",
			"tool name %q must not carry the root prefix", name)
	}
}

// TestMCPPerLeaf_DescriptionIsLeafShort pins per-command
// descriptions. Under the action-enum shape every command shared one
// tool-wide description; here each leaf describes itself.
func TestMCPPerLeaf_DescriptionIsLeafShort(t *testing.T) {
	tools := renderPerLeaf(t, nestedSpec())

	assert.Equal(t, "Add a widget", findTool(tools, "widget.add")["description"])
	assert.Equal(t, "Ping the server", findTool(tools, "ping")["description"])
}

// TestMCPPerLeaf_DescriptionFallsBackToCustomKey covers a leaf with
// no Short of its own: the adopter's configured description is used
// rather than a synthesized string.
func TestMCPPerLeaf_DescriptionFallsBackToCustomKey(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name:     "mytool",
		Commands: []toolspec.Command{{Name: "bare"}},
	}
	tools := renderPerLeaf(t, spec,
		WithCustom(CustomKeyMCPDescription, "My amazing tool"))
	assert.Equal(t, "My amazing tool", findTool(tools, "bare")["description"])
}

// TestMCPPerLeaf_RequiredFlagPublished is the shape's reason to
// exist: a per-verb required list, which the action-enum shape could
// not express.
func TestMCPPerLeaf_RequiredFlagPublished(t *testing.T) {
	tools := renderPerLeaf(t, nestedSpec())

	add := findTool(tools, "widget.add")
	require.NotNil(t, add)
	schema := add["inputSchema"].(map[string]any)
	required, ok := schema["required"].([]any)
	require.True(t, ok, "widget.add publishes a required list")
	assert.Equal(t, []any{"name"}, required)

	// A leaf with no required flags omits the key entirely rather
	// than publishing an empty list — same as the live server.
	ping := findTool(tools, "ping")
	require.NotNil(t, ping)
	_, hasRequired := ping["inputSchema"].(map[string]any)["required"]
	assert.False(t, hasRequired, "no required flags → no required key")
}

// TestMCPPerLeaf_InheritsPersistentFlags pins flag merging: each leaf
// carries its own flags plus the tool's persistent ones.
func TestMCPPerLeaf_InheritsPersistentFlags(t *testing.T) {
	tools := renderPerLeaf(t, nestedSpec())

	add := findTool(tools, "widget.add")
	props := add["inputSchema"].(map[string]any)["properties"].(map[string]any)
	assert.Contains(t, props, "name", "leaf-local flag")
	assert.Contains(t, props, "tag", "leaf-local flag")
	assert.Contains(t, props, "verbose", "root persistent flag")

	ping := findTool(tools, "ping")
	pingProps := ping["inputSchema"].(map[string]any)["properties"].(map[string]any)
	assert.Contains(t, pingProps, "verbose",
		"a flagless leaf still inherits persistent flags")
}

// TestMCPPerLeaf_LocalFlagWinsOverPersistent pins the precedence the
// live server applies: a leaf redeclaring a persistent flag name
// publishes its own declaration.
func TestMCPPerLeaf_LocalFlagWinsOverPersistent(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Commands: []toolspec.Command{{
			Name:  "run",
			Flags: []toolspec.Flag{{Name: "verbose", Type: "int", Description: "local"}},
		}},
		Flags: []toolspec.Flag{{Name: "verbose", Type: "bool", Description: "persistent"}},
	}
	tools := renderPerLeaf(t, spec)
	props := findTool(tools, "run")["inputSchema"].(map[string]any)["properties"].(map[string]any)
	verbose := props["verbose"].(map[string]any)
	assert.Equal(t, "integer", verbose["type"], "leaf-local declaration wins")
	assert.Equal(t, "local", verbose["description"])
}

// TestMCPPerLeaf_DeprecatedFiltering covers both directions of the
// IncludeDeprecated knob.
func TestMCPPerLeaf_DeprecatedFiltering(t *testing.T) {
	included := renderPerLeaf(t, nestedSpec())
	assert.Contains(t, toolNamesOf(included), "legacy",
		"deprecated commands published by default")

	filtered := renderPerLeaf(t, nestedSpec(), WithIncludeDeprecated(false))
	assert.NotContains(t, toolNamesOf(filtered), "legacy",
		"WithIncludeDeprecated(false) drops them")
}

// TestMCPPerLeaf_GroupWithAllChildrenFilteredBecomesLeaf pins the
// edge case: filtering every child of a group must not make the
// group silently vanish.
func TestMCPPerLeaf_GroupWithAllChildrenFilteredBecomesLeaf(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Commands: []toolspec.Command{{
			Name:  "group",
			Short: "A group",
			Children: []toolspec.Command{
				{Name: "old", Deprecated: true},
			},
		}},
	}
	tools := renderPerLeaf(t, spec, WithIncludeDeprecated(false))
	assert.Equal(t, []string{"group"}, toolNamesOf(tools),
		"a group whose children all filtered out still surfaces")
}

// TestMCPPerLeaf_RequiredFlagsCustomKeyAppliesToDeclaringLeaves pins
// the adopter-declared always-required list: it marks the flag
// required on leaves that actually carry it, and does not invent a
// requirement on leaves that do not.
func TestMCPPerLeaf_RequiredFlagsCustomKeyAppliesToDeclaringLeaves(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Commands: []toolspec.Command{
			{Name: "withflag", Flags: []toolspec.Flag{{Name: "task_id", Type: "string"}}},
			{Name: "withoutflag"},
		},
	}
	tools := renderPerLeaf(t, spec,
		WithCustom(CustomKeyMCPRequiredFlags, []string{"task_id"}))

	withSchema := findTool(tools, "withflag")["inputSchema"].(map[string]any)
	assert.Equal(t, []any{"task_id"}, withSchema["required"])

	withoutSchema := findTool(tools, "withoutflag")["inputSchema"].(map[string]any)
	_, hasRequired := withoutSchema["required"]
	assert.False(t, hasRequired,
		"a leaf that does not declare the flag publishes no requirement for it")
}

// TestMCPPerLeaf_EmptySpec covers the degenerate input: a tool with
// no commands renders an empty tools array, not null and not an
// error.
func TestMCPPerLeaf_EmptySpec(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, &toolspec.ToolSpec{Name: "mytool"}))
	assert.Equal(t, "{\n  \"tools\": []\n}\n", buf.String())
}

// --- Regression lock: byte-identical output ------------------------
//
// The structural assertions above tolerate extra keys; this one does
// not. It pins the exact serialized bytes so a future edit that adds,
// renames or reorders a key is caught.

func TestMCPPerLeaf_ByteIdentical(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Commands: []toolspec.Command{{
			Name:  "widget",
			Short: "Manage widgets",
			Children: []toolspec.Command{{
				Name:  "add",
				Short: "Add a widget",
				Flags: []toolspec.Flag{
					{Name: "name", Type: "string", Description: "widget name", Required: true},
					{Name: "tag", Type: "stringSlice", Description: "tag list"},
				},
			}},
		}},
		Flags: []toolspec.Flag{
			{Name: "verbose", Type: "bool", Description: "verbose"},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec))

	const want = `{
  "tools": [
    {
      "description": "Add a widget",
      "inputSchema": {
        "properties": {
          "name": {
            "description": "widget name",
            "type": "string"
          },
          "tag": {
            "description": "tag list",
            "items": {
              "type": "string"
            },
            "type": "array"
          },
          "verbose": {
            "description": "verbose",
            "type": "boolean"
          }
        },
        "required": [
          "name"
        ],
        "type": "object"
      },
      "name": "widget.add"
    }
  ]
}
`
	assert.Equal(t, want, buf.String(),
		"per-leaf MCP bytes must stay stable; the live server's tools/list shape depends on it")
}

func TestMCPPerLeaf_ByteIdentical_Compact(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Commands: []toolspec.Command{
			{Name: "ping", Short: "Ping the server"},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, MCP().Render(&buf, spec, WithPretty(false)))

	const want = `{"tools":[{"description":"Ping the server","inputSchema":{"properties":{},"type":"object"},"name":"ping"}]}
`
	assert.Equal(t, want, buf.String())
}
