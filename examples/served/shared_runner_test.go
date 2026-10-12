package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/transport/socket"
)

// echoLines is how many lines `item echo` writes, with a pause
// between each so a second call has every chance to overlap it.
const echoLines = 6

// addEcho mounts `item echo --tag T`: it writes T on echoLines lines,
// pausing between them, and exits 1 when T ends in "!" so a caller's
// exit status is distinguishable too. Everything it touches — the
// output writer — lives on the command tree the services share.
func addEcho(root *cli.Root) {
	echo := &cobra.Command{
		Use:   "echo",
		Short: "Echo a tag",
		Long:  "Write the tag on several lines, slowly.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tag, _ := cmd.Flags().GetString("tag")
			for i := 0; i < echoLines; i++ {
				fmt.Fprintln(cmd.OutOrStdout(), tag)
				time.Sleep(2 * time.Millisecond)
			}
			if strings.HasSuffix(tag, "!") {
				return fmt.Errorf("refused %s", tag)
			}
			return nil
		},
	}
	echo.Flags().String("tag", "", "What to echo")
	cli.SetSideEffect(echo, cli.SideEffectWriteLocal)
	cli.SetIdempotency(echo, cli.IdempotencyNo)
	findCmd2(root, "item").AddCommand(echo)
}

func findCmd2(root *cli.Root, name string) *cobra.Command {
	for _, c := range root.Cmd.Commands() {
		if c.Name() == name {
			return c
		}
	}
	panic("no command " + name)
}

// answer is what a caller got back for one `item echo`.
type answer struct {
	stdout string
	exit   int
}

// caller runs one `item echo` for tag over one service.
type caller func(t *testing.T, tag string) answer

// sharedRunnerCallers starts every served service on one instance and
// returns a caller per service, REST, socket, RPC and MCP.
func sharedRunnerCallers(t *testing.T) map[string]caller {
	t.Helper()
	sock := shortSocketPath(t)
	run := startServe(t, options{edit: addEcho},
		"--enable", "socket", "--enable", "mcp", "--enable", "rpc",
		"--addr", "127.0.0.1:0", "--mcp-addr", "127.0.0.1:0", "--rpc-addr", "127.0.0.1:0",
		"--socket", sock)
	addrs := map[string]string{}
	for len(addrs) < 4 {
		p := run.waitReady(t, "")
		addrs[p.Service] = p.Address
	}

	rest := "http://" + addrs["api"]
	rpc := rpcClients(addrs["rpc"])["connect"]
	sess := mcpDial(t, addrs["mcp"], nil)

	return map[string]caller{
		"rest": func(t *testing.T, tag string) answer {
			status, body := httpDo(t, http.MethodPost, rest+"/v1/commands/item/echo",
				fmt.Sprintf(`{"flags":{"tag":%q}}`, tag))
			// A refused call is a 5xx carrying the same result body.
			require.Contains(t, []int{http.StatusOK, http.StatusInternalServerError}, status, "%s: %s", tag, body)
			res := decodeResult(t, body)
			return answer{res.Stdout, res.ExitCode}
		},
		"socket": func(t *testing.T, tag string) answer {
			resp := callSocket(t, sock, socket.Request{
				Path: []string{"item", "echo"}, Flags: map[string]any{"tag": tag}})
			require.True(t, resp.Ok, "%s: %+v", tag, resp.Error)
			return answer{resp.Result.Stdout, resp.Result.ExitCode}
		},
		"rpc": func(t *testing.T, tag string) answer {
			resp, err := rpc.Invoke(context.Background(), rpcCall("item echo", map[string]any{"tag": tag}))
			require.NoError(t, err, tag)
			return answer{resp.Msg.GetStdout(), int(resp.Msg.GetExitCode())}
		},
		"mcp": func(t *testing.T, tag string) answer {
			res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "item.echo", Arguments: map[string]any{"tag": tag}})
			require.NoError(t, err, tag)
			var b strings.Builder
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					b.WriteString(tc.Text)
				}
			}
			// A failed call carries its stderr after the stdout.
			out, _, _ := strings.Cut(b.String(), "[stderr]")
			exit := 0
			if res.IsError {
				exit = 1
			}
			return answer{out, exit}
		},
	}
}

var _ = connect.CodeOf

// TestServedCallsNeverInterleaveAcrossServices pins that services of
// one instance share one command tree and therefore one runner: calls
// overlapping across every pair of services each get back exactly
// their own output and exit status. Per-service runner locks let one
// call's writer capture another's lines.
func TestServedCallsNeverInterleaveAcrossServices(t *testing.T) {
	callers := sharedRunnerCallers(t)

	pairs := [][2]string{
		{"socket", "rest"}, {"socket", "rpc"}, {"socket", "mcp"},
		{"rest", "rpc"}, {"rest", "mcp"}, {"rpc", "mcp"},
	}
	for _, p := range pairs {
		t.Run(p[0]+"+"+p[1], func(t *testing.T) {
			const rounds = 12
			var wg sync.WaitGroup
			for _, svc := range p {
				call := callers[svc]
				for i := 0; i < rounds; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						tag := fmt.Sprintf("%s-%d", svc, i)
						if i%4 == 3 {
							tag += "!"
						}
						got := call(t, tag)
						if !assert.Equal(t, strings.Repeat(tag+"\n", echoLines), got.stdout,
							"%s call %q read another call's output", svc, tag) {
							return
						}
						want := 0
						if strings.HasSuffix(tag, "!") {
							want = 1
						}
						assert.Equal(t, want, got.exit, "%s call %q", svc, tag)
					}()
				}
			}
			wg.Wait()
		})
	}
}
