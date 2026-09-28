package mcpserve_test

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/mcpserve"
)

// withKeys is a Root option setting configuration keys.
func withKeys(kv map[string]any) func(*cli.Root) {
	return func(r *cli.Root) {
		for k, v := range kv {
			r.Viper.Set(k, v)
		}
	}
}

// TestMCPServiceCommandDeadline pins that services.mcp.timeouts.command
// bounds a tool call, answered as an isError result whose text starts
// with deadline_exceeded and whose _meta carries the code.
func TestMCPServiceCommandDeadline(t *testing.T) {
	_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		withKeys(map[string]any{"services.mcp.timeouts.command": "150ms"}))
	sess := dialMCP(t, endpoint, nil, nil)

	start := time.Now()
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "linger", Arguments: map[string]any{}})
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
	require.True(t, res.IsError)
	require.NotEmpty(t, res.Content)
	text := res.Content[0].(*mcp.TextContent).Text
	assert.True(t, strings.HasPrefix(text, "deadline_exceeded: "), text)
	assert.Contains(t, text, "linger ran past its 150ms deadline")
	refusal, ok := res.Meta["hop.top/refusal"].(map[string]any)
	require.True(t, ok, "_meta = %v", res.Meta)
	assert.Equal(t, "deadline_exceeded", refusal["code"])

	out, isErr := callTool(t, sess, "linger", map[string]any{"for": "10ms"})
	assert.False(t, isErr, out)
	assert.Equal(t, "waited", out)
}

// TestMCPServiceEndpointIsExemptFromTheWriteTimeout pins that the mcp
// listener reads services.mcp.timeouts and that its endpoint, a stream
// route, is exempt from the write deadline: a session outlives it, and
// a call that takes longer than it still answers.
func TestMCPServiceEndpointIsExemptFromTheWriteTimeout(t *testing.T) {
	_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		withKeys(map[string]any{"services.all.timeouts.write": "200ms"}))
	sess := dialMCP(t, endpoint, nil, nil)

	time.Sleep(400 * time.Millisecond)
	out, isErr := callTool(t, sess, "linger", map[string]any{"for": "500ms"})
	assert.False(t, isErr, out)
	assert.Equal(t, "waited", out)
	out, isErr = callTool(t, sess, "ping", nil)
	assert.False(t, isErr, out)
}

// TestMCPServiceRefusesABadTimeoutsBlock pins that a timeouts key the
// block does not accept is a usage error before the listener binds.
func TestMCPServiceRefusesABadTimeoutsBlock(t *testing.T) {
	oe := serveErr(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		withKeys(map[string]any{"services.mcp.timeouts.stall": "5s"}))
	assert.Equal(t, 2, oe.ExitCode)
	assert.Contains(t, oe.Error(), "services.mcp.timeouts.stall")
}

// TestMCPServiceStopIsNotHeldByStalledClients pins that stopping the
// mcp service does not wait on clients stalled mid-header or mid-body,
// even with read timeouts longer than the stop budget.
func TestMCPServiceStopIsNotHeldByStalledClients(t *testing.T) {
	run, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"},
		withKeys(map[string]any{"services.mcp.timeouts.read": "1m", "services.mcp.timeouts.read_header": "1m"}))
	host := strings.TrimSuffix(strings.TrimPrefix(endpoint, "http://"), "/mcp")
	for _, raw := range []string{
		"POST /mcp HTTP/1.1\r\nHost: " + host,
		"POST /mcp HTTP/1.1\r\nHost: " + host +
			"\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"js",
	} {
		c, err := net.Dial("tcp", host)
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close() })
		_, err = io.WriteString(c, raw)
		require.NoError(t, err)
	}
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	run.stop()
	select {
	case err := <-run.errCh:
		run.errCh <- err
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return with stalled clients open")
	}
	assert.Less(t, time.Since(start), 3*time.Second)
}
