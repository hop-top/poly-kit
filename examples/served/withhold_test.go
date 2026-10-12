package main

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
)

// readyAddrRE reads each service's bound address from the binary's
// readiness log lines.
var readyAddrRE = regexp.MustCompile(`ready_reported .*service=(\w+) address=(\S+)`)

// readyAddrs is every service's address the run has logged as ready.
func (r *binRun) readyAddrs() map[string]string {
	out := map[string]string{}
	for _, m := range readyAddrRE.FindAllStringSubmatch(r.output(), -1) {
		out[m[1]] = m[2]
	}
	return out
}

// TestBinaryWithheldCommandIsServedOverTheSocketOnly pins
// kit/withhold-from end to end on the built binary, all four services
// running: `item follow` names rest, sse, rpc and mcp, so REST
// describes it non-invocable with the reason withheld-by-command and
// mounts neither its route nor its stream route nor an OpenAPI path,
// MCP lists no tool for it and refuses a call, RPC answers NotFound on
// both methods — and the socket, which it does not name, runs it. The
// commands beside it are served as before.
func TestBinaryWithheldCommandIsServedOverTheSocketOnly(t *testing.T) {
	sock := shortSocketPath(t)
	r := runBinary(t, newHome(t, ""), nil,
		"service", "start",
		"--enable", "socket", "--enable", "mcp", "--enable", "rpc",
		"--addr", "127.0.0.1:0", "--mcp-addr", "127.0.0.1:0", "--rpc-addr", "127.0.0.1:0",
		"--socket", sock)
	require.True(t, r.ready, r.output())
	addrs := r.readyAddrs()
	for _, svc := range []string{"api", "mcp", "rpc", "socket"} {
		require.NotEmpty(t, addrs[svc], "%s never reported ready:\n%s", svc, r.output())
	}

	// REST: described, not invocable, no route of any kind.
	rest := "http://" + addrs["api"]
	got := discover(t, rest)
	assert.Equal(t, verdict{Reason: cmdsurface.ReasonWithheldByCommand}, got["item follow"])
	assert.Equal(t, verdict{Invocable: true}, got["item watch"], "the default is unchanged")
	for _, url := range []string{"/v1/commands/item/follow", "/v1/commands/item/follow/stream"} {
		status, body := httpDo(t, http.MethodGet, rest+url, "")
		assert.Equal(t, http.StatusNotFound, status, "%s: %s", url, body)
	}
	status, spec := httpDo(t, http.MethodGet, rest+"/openapi.json", "")
	require.Equal(t, http.StatusOK, status, string(spec))
	assert.NotContains(t, string(spec), "item/follow")
	assert.Contains(t, string(spec), "item/watch")

	// MCP: not a tool, and a call by name is refused.
	sess := mcpDial(t, addrs["mcp"], nil)
	tools := mcpTools(t, sess)
	assert.NotContains(t, tools, "item.follow")
	assert.Contains(t, tools, "item.watch")
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "item.follow", Arguments: map[string]any{"count": 1, "interval": "10ms"},
	})
	if err == nil {
		assert.True(t, res.IsError, "item.follow ran over MCP")
	}
	assert.Error(t, err, "an unlisted tool is a protocol error, not a result")

	// RPC: NotFound on both methods, every wire protocol.
	for name, c := range rpcClients("http://" + strings.TrimPrefix(addrs["rpc"], "http://")) {
		_, err := c.Invoke(t.Context(), rpcCall("item follow", map[string]any{"count": 1, "interval": "10ms"}))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "%s Invoke: %v", name, err)
		stream, err := c.InvokeStream(t.Context(), rpcCall("item follow", map[string]any{"count": 1, "interval": "10ms"}))
		if err == nil {
			for stream.Receive() {
				t.Errorf("%s InvokeStream delivered %v", name, stream.Msg())
			}
			err = stream.Err()
			_ = stream.Close()
		}
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "%s InvokeStream: %v", name, err)
	}

	// The socket: not named, so it runs.
	resp := callSocket(t, addrs["socket"], socket.Request{
		Path:  []string{"item", "follow"},
		Flags: map[string]any{"count": 2, "interval": "10ms"},
	})
	require.True(t, resp.Ok, "%+v", resp.Error)
	assert.Equal(t, 0, resp.Result.ExitCode, resp.Result.Stderr)
	assert.Equal(t, "follow 1: 2 items\nfollow 2: 2 items\n", resp.Result.Stdout)
}

// TestWithholdingTheSocketToo pins that the socket loses the command
// once kit/withhold-from names it, answering NOT_ENABLED, while a
// command that does not name it keeps running there.
func TestWithholdingTheSocketToo(t *testing.T) {
	path := shortSocketPath(t)
	run := startServe(t, options{edit: func(root *cli.Root) {
		cli.SetWithholdFrom(findCmd(t, root, "item", "follow"),
			cmdsurface.SurfaceREST, cmdsurface.SurfaceSocket)
	}}, "socket", "--socket", path)
	run.waitReady(t, "socket")

	resp := callSocket(t, path, socket.Request{Path: []string{"item", "follow"}, Flags: map[string]any{"count": 1}})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeNotEnabled, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, cmdsurface.AnnotationWithholdFrom)

	resp = callSocket(t, path, socket.Request{Path: []string{"item", "watch"}, Flags: map[string]any{"count": 1}})
	require.True(t, resp.Ok, "%+v", resp.Error)
}

// TestUnknownSurfaceInWithholdFromRefusesToStart pins that a
// kit/withhold-from naming no surface is a usage error, exit 2, before
// any service binds, naming the command and the value.
func TestUnknownSurfaceInWithholdFromRefusesToStart(t *testing.T) {
	for _, svc := range []string{"api", "socket", "mcp", "rpc"} {
		t.Run(svc, func(t *testing.T) {
			root := newRoot(options{})
			findCmd(t, root, "item", "follow").Annotations[cmdsurface.AnnotationWithholdFrom] = "rest,mpc"
			args := []string{"service", "start", svc}
			switch svc {
			case "api":
				args = append(args, "--addr", "127.0.0.1:0")
			case "socket":
				args = append(args, "--socket", shortSocketPath(t))
			case "mcp":
				args = append(args, "--mcp-addr", "127.0.0.1:0")
			case "rpc":
				args = append(args, "--rpc-addr", "127.0.0.1:0")
			}
			err := runToCompletion(t, root, args, 5*time.Second)
			require.Error(t, err)
			var kitErr *output.Error
			require.ErrorAs(t, err, &kitErr)
			assert.Equal(t, 2, kitErr.ExitCode, err.Error())
			assert.Contains(t, err.Error(), "served item follow")
			assert.Contains(t, err.Error(), `unknown surface "mpc"`)
		})
	}
}

func findCmd(t *testing.T, root *cli.Root, path ...string) *cobra.Command {
	t.Helper()
	cmd, rest, err := root.Cmd.Find(path)
	require.NoError(t, err)
	require.Empty(t, rest)
	return cmd
}
