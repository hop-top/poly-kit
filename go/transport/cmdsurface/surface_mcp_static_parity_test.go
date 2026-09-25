package cmdsurface_test

// Static/live MCP shape parity.
//
// kit renders MCP tools from two places:
//
//   - go/transport/cmdsurface, for the LIVE server (buildToolEnvelope,
//     reached from tools/list on both protocol eras), and
//   - go/ai/toolspec/adapters, for the STATIC descriptor that
//     `<tool> spec --format mcp` prints.
//
// They are separate projections by necessity, not by preference:
// the live one reads a *cobra.Command straight out of the mounted
// Bridge, the static one reads a toolspec.ToolSpec that has already
// been walked and curated. They cannot share code — console/cli
// imports cmdsurface, so a cmdsurface projection that reached for
// toolspec/cli would close an import cycle. What CAN be shared is
// the shape, and this test is what holds the two to it.
//
// A client that reads `<tool> spec --format mcp` and a client that
// connects to the same tool's MCP server must see the same tool
// surface. Before this test existed they did not: the static
// adapter collapsed the whole tree into ONE envelope with an
// "action" enum, so a consumer that built against the static
// rendering broke on the live one. Re-shaping the adapter to
// per-leaf closed the gap; this test keeps it closed by driving
// both projections over the SAME cobra tree and diffing the tool
// sets.
//
// This file lives in the external test package so it may import
// toolspec/cli. It therefore drives the live side over real HTTP
// (MountMCP + tools/list) rather than calling the unexported
// buildToolEnvelope — which is the truer contract anyway: the wire
// is what a client actually sees.
//
// If this test fails, the two renderings have drifted apart again.
// Fix the projection that moved; do not relax the assertion.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec/adapters"
	speccli "hop.top/kit/go/ai/toolspec/cli"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// parityTree is the shared fixture both projections render. It
// mirrors the frozen legacyLockTree (surface_mcp_legacy_lock_test.go)
// — nested subcommand, a required flag, a hidden flag, a deprecated
// flag, an array flag, and several bare leaves — but is declared
// here because that file is frozen and lives in the internal test
// package.
func parityTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

	widget := &cobra.Command{Use: "widget"}
	add := &cobra.Command{
		Use:         "add",
		Short:       "Add a widget",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "write"},
	}
	add.Flags().String("name", "", "widget name")
	add.Flags().Int("count", 0, "widget count")
	add.Flags().Bool("force", false, "force flag")
	add.Flags().StringSlice("tag", nil, "tag list")
	add.Flags().String("hidden-flag", "", "should be hidden")
	_ = add.Flags().MarkHidden("hidden-flag")
	add.Flags().String("deprecated-flag", "", "should be dropped")
	_ = add.Flags().MarkDeprecated("deprecated-flag", "old")
	_ = add.MarkFlagRequired("name")
	widget.AddCommand(add)

	del := &cobra.Command{
		Use:         "delete",
		Short:       "Delete a widget",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	}
	widget.AddCommand(del)
	root.AddCommand(widget)

	ping := &cobra.Command{
		Use:         "ping",
		Short:       "Ping the server",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	root.AddCommand(ping)

	return root
}

// liveToolSet drives tools/list against a real mounted MCP surface
// and returns the descriptors keyed by tool name.
func liveToolSet(t *testing.T) map[string]map[string]any {
	t.Helper()

	r := api.NewRouter()
	require.NoError(t, cmdsurface.MountMCP(cmdsurface.New(parityTree()), r))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	req := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	resp, err := srv.Client().Post(srv.URL+"/mcp", "application/json",
		bytes.NewBufferString(req))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var envelope struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
	require.NotEmpty(t, envelope.Result.Tools, "live projection produced tools")

	return byToolName(t, envelope.Result.Tools)
}

// staticToolSet renders the SAME tree through the STATIC projection
// (WalkCobra → adapters.MCP()) and returns the descriptors keyed by
// tool name.
func staticToolSet(t *testing.T) map[string]map[string]any {
	t.Helper()

	spec := speccli.WalkCobra(parityTree())

	// WithIncludeDeprecated(false) is what makes this comparison
	// apples-to-apples. The static adapter can publish deprecated
	// commands and flags — a spec is where a migrating agent learns
	// a command is going away — while the live server always drops
	// them, having no such knob. Parity is asserted under the
	// setting the live surface is fixed at; the adapter's
	// deprecation behavior is covered in its own package.
	var buf bytes.Buffer
	require.NoError(t, adapters.MCP().Render(&buf, spec,
		adapters.WithIncludeDeprecated(false)))

	var payload struct {
		Tools []map[string]any `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &payload),
		`static render is a {"tools": [...]} document`)
	require.NotEmpty(t, payload.Tools, "static projection produced tools")

	return byToolName(t, payload.Tools)
}

// TestStaticLiveParity_SameToolNames is the headline assertion: both
// projections advertise the same set of callable tools. A client
// that discovered a tool statically can call it live, and vice
// versa.
func TestStaticLiveParity_SameToolNames(t *testing.T) {
	live := liveToolSet(t)
	static := staticToolSet(t)

	assert.ElementsMatch(t, keysOf(live), keysOf(static),
		"static `spec --format mcp` and live tools/list advertise the same tools")
}

// TestStaticLiveParity_SameInputSchemas pins the per-tool schema:
// same property names, same property types and same required list on
// both sides. Types are compared because the two mcpJSONType tables
// are duplicated across the package boundary and would otherwise be
// free to drift.
func TestStaticLiveParity_SameInputSchemas(t *testing.T) {
	live := liveToolSet(t)
	static := staticToolSet(t)

	for name, liveTool := range live {
		staticTool, ok := static[name]
		if !ok {
			continue // name-set drift is TestStaticLiveParity_SameToolNames' job
		}
		t.Run(name, func(t *testing.T) {
			liveSchema := schemaOf(t, liveTool)
			staticSchema := schemaOf(t, staticTool)

			assert.Equal(t, "object", liveSchema["type"])
			assert.Equal(t, liveSchema["type"], staticSchema["type"])

			liveProps := propsOf(t, liveSchema)
			staticProps := propsOf(t, staticSchema)
			assert.ElementsMatch(t, keysOfAny(liveProps), keysOfAny(staticProps),
				"same flag properties on both sides")

			for prop, liveVal := range liveProps {
				staticVal, ok := staticProps[prop]
				if !ok {
					continue
				}
				lm, _ := liveVal.(map[string]any)
				sm, _ := staticVal.(map[string]any)
				require.NotNil(t, lm)
				require.NotNil(t, sm)
				assert.Equal(t, lm["type"], sm["type"],
					"property %q: duplicated mcpJSONType tables agree", prop)
				assert.Equal(t, lm["items"], sm["items"],
					"property %q: array items sub-schema agrees", prop)
			}

			assert.ElementsMatch(t,
				requiredOf(liveSchema), requiredOf(staticSchema),
				"same required list on both sides")
		})
	}
}

// TestStaticLiveParity_RequiredFlagSurvives is the regression guard
// with teeth. parityTree marks widget.add's --name required; the
// action-enum shape could not express that (its only required field
// was "action" by construction), which is precisely why per-leaf is
// the default. Both projections must now say so.
func TestStaticLiveParity_RequiredFlagSurvives(t *testing.T) {
	live := liveToolSet(t)
	static := staticToolSet(t)

	const tool = "widget.add"
	liveTool, ok := live[tool]
	require.True(t, ok, "live projection advertises %s", tool)
	staticTool, ok := static[tool]
	require.True(t, ok, "static projection advertises %s", tool)

	assert.Equal(t, []string{"name"}, requiredOf(schemaOf(t, liveTool)))
	assert.Equal(t, []string{"name"}, requiredOf(schemaOf(t, staticTool)),
		"a required flag reaches the static rendering — the whole point of per-leaf")
}

// TestStaticLiveParity_DescriptionsMatch pins the per-leaf
// description. The live side publishes cobra's Short; the static
// side must publish the same string rather than a tool-wide
// fallback, or an agent reading the static rendering cannot tell the
// commands apart.
func TestStaticLiveParity_DescriptionsMatch(t *testing.T) {
	live := liveToolSet(t)
	static := staticToolSet(t)

	for name, liveTool := range live {
		staticTool, ok := static[name]
		if !ok {
			continue
		}
		assert.Equal(t, liveTool["description"], staticTool["description"],
			"tool %q: same one-line description on both sides", name)
	}
}

// TestStaticLiveParity_HiddenAndDeprecatedFlagsExcluded pins the
// filtering both sides apply. parityTree's widget.add declares one
// hidden and one deprecated flag; neither belongs in a published
// tool schema, on either side.
func TestStaticLiveParity_HiddenAndDeprecatedFlagsExcluded(t *testing.T) {
	for label, set := range map[string]map[string]map[string]any{
		"live":   liveToolSet(t),
		"static": staticToolSet(t),
	} {
		t.Run(label, func(t *testing.T) {
			tool, ok := set["widget.add"]
			require.True(t, ok)
			props := propsOf(t, schemaOf(t, tool))
			assert.NotContains(t, props, "hidden-flag")
			assert.NotContains(t, props, "deprecated-flag")
			assert.Contains(t, props, "name")
			assert.Contains(t, props, "tag")
		})
	}
}

// --- helpers ------------------------------------------------------

func byToolName(t *testing.T, tools []map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		require.NotEmpty(t, name, "descriptor has a name")
		out[name] = tool
	}
	return out
}

func schemaOf(t *testing.T, tool map[string]any) map[string]any {
	t.Helper()
	s, ok := tool["inputSchema"].(map[string]any)
	require.True(t, ok, "descriptor carries an inputSchema object")
	return s
}

func propsOf(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	p, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "inputSchema carries a properties object")
	return p
}

// requiredOf reads the required list off a schema and sorts it so
// comparisons are order-independent. A schema with no required list
// yields nil, which ElementsMatch treats as empty.
func requiredOf(schema map[string]any) []string {
	var out []string
	if v, ok := schema["required"].([]any); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

func keysOf(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfAny(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
