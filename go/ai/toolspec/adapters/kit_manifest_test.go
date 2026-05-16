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

func TestKitManifest_Metadata(t *testing.T) {
	a := KitManifest()
	assert.Equal(t, "kit-manifest", a.Name())
	assert.Equal(t, []string{"kit", "manifest"}, a.Aliases())
	assert.Equal(t, "application/json", a.ContentType())
	assert.NotEmpty(t, a.Description())
}

func TestKitManifest_RegistersInRegistry(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(KitManifest()))
	assert.Equal(t, KitManifest(), r.Lookup("kit-manifest"))
	assert.Equal(t, KitManifest(), r.Lookup("kit"), "alias resolves")
	assert.Equal(t, KitManifest(), r.Lookup("manifest"), "alias resolves")
}

// --- Render: shape ------------------------------------------------

func TestKitManifest_Render_NilSpec(t *testing.T) {
	var buf bytes.Buffer
	err := KitManifest().Render(&buf, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil spec")
}

func TestKitManifest_Render_TopLevelFields(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, KitManifest().Render(&buf, spec))

	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))

	assert.Equal(t, "mytool", m.Tool)
	assert.Equal(t, "1.0", m.SchemaVersion)
}

func TestKitManifest_Render_FlatLeaves(t *testing.T) {
	// The kit-manifest shape is leaf-flat. minimalSpec has 3 top-
	// level commands with no children — all three should appear.
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, KitManifest().Render(&buf, spec))

	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	require.Len(t, m.Commands, 3)
}

func TestKitManifest_Render_RecursiveFlattening(t *testing.T) {
	// task → list / create / delete; each leaf should appear in the
	// flat output with its full path.
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Commands: []toolspec.Command{
			{
				Name: "task",
				Children: []toolspec.Command{
					{Name: "list"},
					{Name: "create"},
					{Name: "delete"},
				},
			},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, KitManifest().Render(&buf, spec))

	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	require.Len(t, m.Commands, 3, "flat leaves only — task itself not emitted")
	for _, c := range m.Commands {
		assert.Equal(t, []string{"mytool", "task", c.Path[2]}, c.Path,
			"path includes tool name + parent + leaf")
	}
}

func TestKitManifest_Render_LeafWithoutChildrenIsLeaf(t *testing.T) {
	// A top-level command with no children IS a leaf and should
	// surface, with path = [tool, leaf].
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Commands: []toolspec.Command{
			{Name: "ping"},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, KitManifest().Render(&buf, spec))

	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	require.Len(t, m.Commands, 1)
	assert.Equal(t, []string{"mytool", "ping"}, m.Commands[0].Path)
}

// --- Render: contract + deprecation ------------------------------

func TestKitManifest_Render_ContractFlows(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, KitManifest().Render(&buf, spec))

	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))

	var create *toolspec.ManifestCommand
	for i := range m.Commands {
		if m.Commands[i].Path[len(m.Commands[i].Path)-1] == "create" {
			create = &m.Commands[i]
		}
	}
	require.NotNil(t, create)
	assert.Equal(t, "write", create.SideEffect)
	assert.Equal(t, "", create.Idempotent, "create.Idempotent=false → empty manifest field")
}

func TestKitManifest_Render_DeprecationIncludedByDefault(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Commands: []toolspec.Command{
			{Name: "old", Deprecated: true, DeprecatedSince: "1.2"},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, KitManifest().Render(&buf, spec))

	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	require.Len(t, m.Commands, 1)
	assert.True(t, m.Commands[0].Deprecated)
	assert.Equal(t, "1.2", m.Commands[0].DeprecatedSince)
}

func TestKitManifest_Render_DeprecationFiltered(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Commands: []toolspec.Command{
			{Name: "current"},
			{Name: "old", Deprecated: true},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, KitManifest().Render(&buf, spec, WithIncludeDeprecated(false)))

	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	require.Len(t, m.Commands, 1)
	assert.Equal(t, "current", m.Commands[0].Path[len(m.Commands[0].Path)-1])
}

// --- Render: schema version override -----------------------------

func TestKitManifest_Render_SchemaVersionOverride(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, KitManifest().Render(&buf, spec, WithSchemaVersion("99.0")))

	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	assert.Equal(t, "99.0", m.SchemaVersion, "SchemaVersion option wins over spec value")
}

// --- Render: flag projection -------------------------------------

func TestKitManifest_Render_FlagsProjected(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, KitManifest().Render(&buf, spec))

	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))

	var create *toolspec.ManifestCommand
	for i := range m.Commands {
		if m.Commands[i].Path[len(m.Commands[i].Path)-1] == "create" {
			create = &m.Commands[i]
		}
	}
	require.NotNil(t, create)
	require.Len(t, create.Flags, 1)
	assert.Equal(t, "name", create.Flags[0].Name)
	assert.Equal(t, "string", create.Flags[0].Type)
	assert.Equal(t, "thing name", create.Flags[0].Description)
}

// --- Render: output format selection -----------------------------

func TestKitManifest_Render_DefaultFormatJSON(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, KitManifest().Render(&buf, spec))
	// Sanity: parses as JSON, doesn't look like YAML.
	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
}

func TestKitManifest_Render_YAMLFormat(t *testing.T) {
	spec := minimalSpec()
	var buf bytes.Buffer
	require.NoError(t, KitManifest().Render(&buf, spec,
		WithCustom(CustomKeyOutputFormat, "yaml")))

	out := buf.String()
	mustContain(t, out, "tool: mytool")
}
