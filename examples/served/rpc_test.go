package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	"hop.top/kit/go/console/cli/rpcserve"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
)

// --- RPC clients ---------------------------------------------------------------

// rpcClients returns a generated Commands client per wire protocol the
// service speaks. The HTTP client speaks unencrypted HTTP/2 with prior
// knowledge beside HTTP/1.1, which is what native gRPC needs without
// TLS.
func rpcClients(base string) map[string]cmdsurfacev1connect.CommandsClient {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	hc := &http.Client{Transport: &http.Transport{Protocols: p}}
	return map[string]cmdsurfacev1connect.CommandsClient{
		"connect":  cmdsurfacev1connect.NewCommandsClient(hc, base),
		"grpc":     cmdsurfacev1connect.NewCommandsClient(hc, base, connect.WithGRPC()),
		"grpc-web": cmdsurfacev1connect.NewCommandsClient(hc, base, connect.WithGRPCWeb()),
	}
}

func rpcCall(path string, flags map[string]any) *connect.Request[cmdsurfacev1.Invocation] {
	inv := &cmdsurfacev1.Invocation{Path: strings.Fields(path)}
	if flags != nil {
		s, err := structpb.NewStruct(flags)
		if err != nil {
			panic(err)
		}
		inv.Flags = s
	}
	return connect.NewRequest(inv)
}

func startRPC(t *testing.T, opts options) string {
	t.Helper()
	run := startServe(t, opts, "rpc", "--rpc-addr", "127.0.0.1:0")
	base := run.waitReady(t, rpcserve.ServiceName).Address
	require.True(t, strings.HasPrefix(base, "http://127.0.0.1:"), base)
	return base
}

// --- The rpc service -------------------------------------------------------------

// TestReadAnswersOverEveryRPCProtocol pins that `item list`
// answers in data_json with exit code 0 over Connect, gRPC (h2c) and
// gRPC-Web.
func TestReadAnswersOverEveryRPCProtocol(t *testing.T) {
	base := startRPC(t, options{})
	for name, c := range rpcClients(base) {
		t.Run(name, func(t *testing.T) {
			resp, err := c.Invoke(t.Context(), rpcCall("item list", nil))
			require.NoError(t, err)
			assert.Equal(t, int32(0), resp.Msg.GetExitCode())
			assert.Contains(t, resp.Msg.GetDataJson(), `"bolt"`, "structured output rides in data")
		})
	}
}

// TestLongRunningReadStreamsOverRPC pins that InvokeStream of
// `item watch` delivers one stdout event per line, then done with the
// result.
func TestLongRunningReadStreamsOverRPC(t *testing.T) {
	base := startRPC(t, options{})
	for name, c := range rpcClients(base) {
		t.Run(name, func(t *testing.T) {
			stream, err := c.InvokeStream(t.Context(), rpcCall("item watch", map[string]any{"count": 2, "interval": "20ms"}))
			require.NoError(t, err)
			defer func() { _ = stream.Close() }()
			var lines []string
			var done *cmdsurfacev1.Result
			for stream.Receive() {
				switch ev := stream.Msg(); ev.GetKind() {
				case "stdout":
					lines = append(lines, ev.GetData().GetStringValue())
				case "done":
					done = ev.GetResult()
				}
			}
			require.NoError(t, stream.Err())
			assert.Equal(t, []string{"tick 1: 2 items", "tick 2: 2 items"}, lines)
			require.NotNil(t, done)
			assert.Equal(t, int32(0), done.GetExitCode())
		})
	}
}

// TestDestructiveRunsOverRPCOnceNamedAndConfirmed pins
// permission_denied until Policy.AllowDestructiveOn names rpc; then the
// command's own gate refuses without confirm and runs with it.
func TestDestructiveRunsOverRPCOnceNamedAndConfirmed(t *testing.T) {
	c := rpcClients(startRPC(t, options{}))["connect"]
	_, err := c.Invoke(t.Context(), rpcCall("item purge", nil))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "withheld until the policy names rpc")

	c = rpcClients(startRPC(t, options{allowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceRPC}}))["connect"]
	resp, err := c.Invoke(t.Context(), rpcCall("item purge", nil))
	require.NoError(t, err)
	assert.NotEqual(t, int32(0), resp.Msg.GetExitCode(), "the command's own confirmation still applies")

	resp, err = c.Invoke(t.Context(), rpcCall("item purge", map[string]any{"confirm": "yes"}))
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Msg.GetExitCode(), resp.Msg.GetStderr())
	assert.Contains(t, resp.Msg.GetStdout(), "purged 2 items")
}

// TestConfirmationAndAuthGatesOverRPC pins that `item tag` needs
// X-Confirm-Token and `item sync` is unauthenticated without Auth, bare
// Authorization header or not.
func TestConfirmationAndAuthGatesOverRPC(t *testing.T) {
	c := rpcClients(startRPC(t, options{}))["connect"]

	_, err := c.Invoke(t.Context(), rpcCall("item tag", map[string]any{"name": "bolt"}))
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	req := rpcCall("item tag", map[string]any{"name": "bolt"})
	req.Header().Set("X-Confirm-Token", "yes")
	resp, err := c.Invoke(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Msg.GetExitCode(), resp.Msg.GetStderr())

	// No Auth configured: a bare header is not authentication.
	req = rpcCall("item sync", nil)
	req.Header().Set("Authorization", "Bearer made-up")
	_, err = c.Invoke(t.Context(), req)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

// TestRPCAndRESTWithholdTheSameCommands pins that nothing REST
// discovery marks non-invocable runs over RPC: `shell`, `upgrade`,
// `serve` and `status` are not_found.
func TestRPCAndRESTWithholdTheSameCommands(t *testing.T) {
	run := startServe(t, options{}, "--enable", "rpc", "--addr", "127.0.0.1:0", "--rpc-addr", "127.0.0.1:0")
	rest := discover(t, "http://"+run.waitReady(t, "api").Address)
	c := rpcClients(run.waitReady(t, rpcserve.ServiceName).Address)["connect"]

	require.Contains(t, rest, "status")
	for name, v := range rest {
		if v.Invocable {
			continue
		}
		_, err := c.Invoke(t.Context(), rpcCall(name, nil))
		require.Error(t, err, "%s is withheld over REST (%s) but ran over RPC", name, v.Reason)
		assert.Contains(t, []connect.Code{connect.CodeNotFound, connect.CodePermissionDenied},
			connect.CodeOf(err), name)
	}
	for _, never := range []string{"shell", "upgrade", "serve", "status"} {
		_, err := c.Invoke(t.Context(), rpcCall(never, nil))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), never)
	}
}

// TestUnauthenticatedRemoteRPCIsRefused pins that
// --rpc-addr 0.0.0.0:0 exits 2 naming services.rpc.insecure_remote.
func TestUnauthenticatedRemoteRPCIsRefused(t *testing.T) {
	root := newRoot(options{})
	var stderr safeBuffer
	root.Cmd.SetErr(&stderr)
	err := runToCompletion(t, root, []string{"serve", "rpc", "--rpc-addr", "0.0.0.0:0"}, 5*time.Second)
	require.Error(t, err)
	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, 2, kitErr.ExitCode)
	assert.Contains(t, err.Error(), "services.rpc.insecure_remote")
}
