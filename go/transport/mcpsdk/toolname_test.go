package mcpsdk

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
)

func TestToolNamesJoinWithADotByDefault(t *testing.T) {
	srv, _, _ := newSurfaceHarness(t, newTestTree(), nil)
	names := toolNames(t, connect(t, srv.URL+"/mcp", nil))
	assert.Contains(t, names, "widget.add")
	assert.Contains(t, names, "widget.delete")
	assert.Contains(t, names, "ping")
}

func TestToolNameSeparatorRenamesListedTools(t *testing.T) {
	for _, sep := range []string{"_", "-", "__"} {
		t.Run(sep, func(t *testing.T) {
			srv, _, _ := newSurfaceHarness(t, newTestTree(), nil, WithToolNameSeparator(sep))
			names := toolNames(t, connect(t, srv.URL+"/mcp", nil))
			assert.Contains(t, names, "widget"+sep+"add")
			assert.Contains(t, names, "widget"+sep+"delete")
			assert.Contains(t, names, "ping", "a single-segment leaf has no separator")
			assert.NotContains(t, names, "widget.add")
		})
	}
}

func TestToolNameSeparatorRoundTripsACall(t *testing.T) {
	srv, _, _ := newSurfaceHarness(t, newTestTree(), nil, WithToolNameSeparator("_"))
	sess := connect(t, srv.URL+"/mcp", nil)

	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "widget_add",
		Arguments: map[string]any{"name": "bolt"},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, textOf(res))
	assert.Contains(t, textOf(res), "added")

	_, err = sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "widget.add",
		Arguments: map[string]any{"name": "bolt"},
	})
	require.Error(t, err, "the dotted name is not served under another separator")
}

func TestToolNameSeparatorEmptyIsRefused(t *testing.T) {
	_, err := New(cmdsurface.New(newTestTree()), WithToolNameSeparator(""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WithToolNameSeparator")
	assert.Contains(t, err.Error(), "empty")
}

// collidingTree has a leaf whose own name contains "_": joined with
// "_", "data_sync" and a "data" group's "sync" leaf read the same.
func collidingTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	run := func(cmd *cobra.Command, _ []string) error { return nil }
	root.AddCommand(&cobra.Command{Use: "data_sync", RunE: run})
	group := &cobra.Command{Use: "data"}
	group.AddCommand(&cobra.Command{Use: "sync", RunE: run})
	root.AddCommand(group)
	return root
}

func TestToolNameSeparatorInsideACommandNameIsRefused(t *testing.T) {
	_, err := New(cmdsurface.New(collidingTree()), WithToolNameSeparator("_"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WithToolNameSeparator")
	assert.Contains(t, err.Error(), `"data_sync"`)
	assert.Contains(t, err.Error(), `"_"`)
}

func TestToolNameSeparatorCollisionIgnoresOtherSeparators(t *testing.T) {
	_, err := New(cmdsurface.New(collidingTree()), WithToolNameSeparator("-"))
	require.NoError(t, err)
}

func TestToolNameSeparatorNamesTaskEligibleTools(t *testing.T) {
	b := cmdsurface.New(newTestTree())
	_, err := New(b, WithToolNameSeparator("_"),
		WithTasks(TasksConfig{Tools: []string{"widget_add"}}))
	require.NoError(t, err)

	_, err = New(b, WithToolNameSeparator("_"),
		WithTasks(TasksConfig{Tools: []string{"widget.add"}}))
	require.Error(t, err, "task-eligible tools are named under the separator")
}
