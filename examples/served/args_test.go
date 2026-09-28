package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/mcpserve"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
	"hop.top/kit/go/transport/socket"
)

// A positional argument is an argument on every surface, whatever it
// starts with. Each name below is one cobra would otherwise take for a
// flag: an unknown shorthand cluster, and --help, which would answer
// with help text and exit 0 without adding anything.
var dashNames = []string{"-washer", "--help"}

func TestDashLeadingArgIsPositionalOverREST(t *testing.T) {
	run := startServe(t, options{}, "api", "--addr", "127.0.0.1:0")
	base := "http://" + run.waitReady(t, "api").Address

	for _, name := range dashNames {
		body, err := json.Marshal(map[string]any{"args": []string{name}})
		require.NoError(t, err)
		status, raw := httpDo(t, http.MethodPost, base+"/v1/commands/item/add", string(body))
		require.Equal(t, http.StatusOK, status, string(raw))
		res := decodeResult(t, raw)
		assert.Equal(t, 0, res.ExitCode, res.Stderr)
		assert.Equal(t, "added "+name+"\n", res.Stdout)
	}

	status, raw := httpDo(t, http.MethodGet, base+"/v1/commands/item/list", "")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, []string{"--help", "-washer", "bolt", "nut"}, items(t, decodeResult(t, raw).Data))
}

func TestDashLeadingArgIsPositionalOverTheSocket(t *testing.T) {
	path := shortSocketPath(t)
	run := startServe(t, options{}, "socket", "--socket", path)
	run.waitReady(t, "socket")

	for _, name := range dashNames {
		resp := callSocket(t, path, socket.Request{Path: []string{"item", "add"}, Args: []string{name}})
		require.True(t, resp.Ok, "%+v", resp.Error)
		assert.Equal(t, 0, resp.Result.ExitCode, resp.Result.Stderr)
		assert.Equal(t, "added "+name+"\n", resp.Result.Stdout)
	}
}

func TestDashLeadingArgIsPositionalOverMCP(t *testing.T) {
	run := startServe(t, options{}, "mcp", "--mcp-addr", "127.0.0.1:0")
	sess := mcpDial(t, run.waitReady(t, mcpserve.ServiceName).Address, nil)

	for _, name := range dashNames {
		text, _, isErr := mcpCall(t, sess, "item.add", map[string]any{"args": []any{name}})
		assert.False(t, isErr, text)
		assert.Equal(t, "added "+name+"\n", text)
	}
}

func TestDashLeadingArgIsPositionalOverRPC(t *testing.T) {
	c := rpcClients(startRPC(t, options{}))["connect"]

	for _, name := range dashNames {
		resp, err := c.Invoke(t.Context(), connect.NewRequest(&cmdsurfacev1.Invocation{
			Path: []string{"item", "add"},
			Args: []string{name},
		}))
		require.NoError(t, err)
		assert.Equal(t, int32(0), resp.Msg.GetExitCode(), resp.Msg.GetStderr())
		assert.Equal(t, "added "+name+"\n", resp.Msg.GetStdout())
	}
}
