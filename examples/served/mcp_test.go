package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/mcpserve"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/cmdsurface"
)

// --- MCP clients -------------------------------------------------------------

// mcpDial connects the official SDK client to an HTTP endpoint.
func mcpDial(t *testing.T, endpoint string, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "served-test", Version: "0"}, opts)
	sess, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func mcpTools(t *testing.T, sess *mcp.ClientSession) []string {
	t.Helper()
	res, err := sess.ListTools(t.Context(), nil)
	require.NoError(t, err)
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// mcpCall calls one tool and returns its text, its structured content,
// and whether it is an error result.
func mcpCall(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (string, any, bool) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err, name)
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.StructuredContent, res.IsError
}

// accept is a client whose user approves every question.
func accept(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	return &mcp.ElicitResult{Action: "accept"}, nil
}

// --- The mcp service over HTTP ------------------------------------------------

func TestMCPToolsListMirrorsWhatMayRun(t *testing.T) {
	run := startServe(t, options{}, "mcp", "--mcp-addr", "127.0.0.1:0")
	endpoint := run.waitReady(t, mcpserve.ServiceName).Address
	require.True(t, strings.HasPrefix(endpoint, "http://127.0.0.1:"), endpoint)
	require.True(t, strings.HasSuffix(endpoint, "/mcp"), endpoint)

	got := mcpTools(t, mcpDial(t, endpoint, nil))
	assert.Subset(t, got, []string{"item.list", "item.add", "item.tag", "item.sync"})
	for _, withheld := range []string{"item.purge", "shell", "upgrade", "serve"} {
		assert.NotContains(t, got, withheld)
	}
}

func TestReadAnswersInStructuredContentOverMCP(t *testing.T) {
	run := startServe(t, options{}, "mcp", "--mcp-addr", "127.0.0.1:0")
	sess := mcpDial(t, run.waitReady(t, mcpserve.ServiceName).Address, nil)

	// A read that declares a schema answers in structured content.
	_, data, isErr := mcpCall(t, sess, "item.list", nil)
	require.False(t, isErr)
	require.NotNil(t, data, "structured output rides in structuredContent")
	assert.Contains(t, toJSON(t, data), `"bolt"`)
}

func TestDestructiveIsWithheldOverMCPByDefault(t *testing.T) {
	run := startServe(t, options{}, "mcp", "--mcp-addr", "127.0.0.1:0")
	sess := mcpDial(t, run.waitReady(t, mcpserve.ServiceName).Address, nil)

	_, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "item.purge", Arguments: map[string]any{}})
	require.Error(t, err, "a withheld destructive tool is not callable")
}

func TestDestructiveRunsOverMCPOnceNamedAndConfirmed(t *testing.T) {
	run := startServe(t, options{allowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceMCP}},
		"mcp", "--mcp-addr", "127.0.0.1:0")
	sess := mcpDial(t, run.waitReady(t, mcpserve.ServiceName).Address, nil)

	text, _, isErr := mcpCall(t, sess, "item.purge", nil)
	assert.True(t, isErr)
	assert.Contains(t, text, "--confirm")

	text, _, isErr = mcpCall(t, sess, "item.purge", map[string]any{"confirm": "yes"})
	assert.False(t, isErr, text)
	assert.Contains(t, text, "purged 2 items")
}

func TestConfirmationRequiredIsApprovedByAPersonOverMCP(t *testing.T) {
	run := startServe(t, options{}, "mcp", "--mcp-addr", "127.0.0.1:0")
	endpoint := run.waitReady(t, mcpserve.ServiceName).Address

	text, _, isErr := mcpCall(t, mcpDial(t, endpoint, &mcp.ClientOptions{ElicitationHandler: accept}),
		"item.tag", map[string]any{"name": "bolt"})
	assert.False(t, isErr, text)
	assert.Equal(t, "tagged bolt\n", text)

	text, _, isErr = mcpCall(t, mcpDial(t, endpoint, nil), "item.tag", map[string]any{"name": "bolt"})
	assert.True(t, isErr)
	assert.Contains(t, text, "confirmation required")
}

func TestAuthRequiredIsRefusedOverUnauthenticatedHTTP(t *testing.T) {
	run := startServe(t, options{}, "mcp", "--mcp-addr", "127.0.0.1:0")
	sess := mcpDial(t, run.waitReady(t, mcpserve.ServiceName).Address, nil)

	text, _, isErr := mcpCall(t, sess, "item.sync", nil)
	assert.True(t, isErr)
	assert.Equal(t, "authentication required", text)
}

func TestUnauthenticatedRemoteMCPIsRefused(t *testing.T) {
	root := newRoot(options{})
	var stderr safeBuffer
	root.Cmd.SetErr(&stderr)
	err := runToCompletion(t, root, []string{"serve", "mcp", "--mcp-addr", "0.0.0.0:0"}, 5*time.Second)
	require.Error(t, err)
	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, 2, kitErr.ExitCode)
	assert.Contains(t, err.Error(), "services.mcp.insecure_remote")
}

func TestMCPServesBesideTheOthersUnderTheSupervisor(t *testing.T) {
	run := startServe(t, options{}, "--enable", "mcp", "--mcp-addr", "127.0.0.1:0")
	run.waitReady(t, "api")
	endpoint := run.waitReady(t, mcpserve.ServiceName).Address
	run.waitSupervisorReady(t)

	resp, err := http.Get(strings.TrimSuffix(endpoint, "/mcp") + "/v1/commands")
	if err == nil {
		_ = resp.Body.Close()
		assert.NotEqual(t, http.StatusOK, resp.StatusCode,
			"the mcp listener is its own: REST is not served there")
	}
	assert.Contains(t, mcpTools(t, mcpDial(t, endpoint, nil)), "item.list")
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
