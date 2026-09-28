package rpcserve_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/rpcserve"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
)

// setKeys is a Root option setting configuration keys.
func setKeys(kv map[string]any) func(*cli.Root) {
	return func(r *cli.Root) {
		for k, v := range kv {
			r.Viper.Set(k, v)
		}
	}
}

// TestRPCServiceCommandDeadline pins that services.rpc.timeouts.command
// bounds a call, unary and streaming, and the call is answered
// DeadlineExceeded on every protocol.
func TestRPCServiceCommandDeadline(t *testing.T) {
	base := startDefault(t, rpcserve.Config{},
		setKeys(map[string]any{"services.rpc.timeouts.command": "150ms"}))

	for _, p := range protocols {
		t.Run(p.name, func(t *testing.T) {
			start := time.Now()
			_, err := client(base, p).Invoke(t.Context(), call("forever", nil))
			require.Error(t, err)
			assert.Equal(t, connect.CodeDeadlineExceeded, connect.CodeOf(err), "%v", err)
			assert.Contains(t, err.Error(), "forever ran past its 150ms deadline")
			assert.Less(t, time.Since(start), 5*time.Second)

			_, _, err = streamAll(t.Context(), client(base, p), call("forever", nil))
			require.Error(t, err)
			assert.Equal(t, connect.CodeDeadlineExceeded, connect.CodeOf(err), "%v", err)

			_, err = client(base, p).Invoke(t.Context(), call("ping", nil))
			require.NoError(t, err, "a quick call is untouched")
		})
	}
}

// TestRPCServiceReadsTheTimeoutsBlock pins that the rpc listener's
// write timeout comes from services.rpc.timeouts: a unary call that
// outlives it fails, and a stream is still exempt.
func TestRPCServiceReadsTheTimeoutsBlock(t *testing.T) {
	const timeout = 300 * time.Millisecond
	base := startDefault(t, rpcserve.Config{},
		setKeys(map[string]any{"services.all.timeouts.write": timeout.String()}))

	flags, err := structpb.NewStruct(map[string]any{"count": 6, "every": "150ms"})
	require.NoError(t, err)
	p := protocols[0]

	req := connect.NewRequest(&cmdsurfacev1.Invocation{Path: []string{"tick"}, Flags: flags})
	_, err = client(base, p).Invoke(t.Context(), req)
	require.Error(t, err, "a unary call past the configured write timeout is cut")

	req = connect.NewRequest(&cmdsurfacev1.Invocation{Path: []string{"tick"}, Flags: flags})
	lines, res, err := streamAll(t.Context(), client(base, p), req)
	require.NoError(t, err)
	assert.Len(t, lines, 6)
	require.NotNil(t, res, "the stream outlived the write timeout")
}

// TestRPCServiceRefusesABadTimeoutsBlock pins that a timeouts key that
// does not parse is a usage error before the listener binds.
func TestRPCServiceRefusesABadTimeoutsBlock(t *testing.T) {
	oe := serveErr(t, rpcserve.Config{}, []string{"rpc", "--rpc-addr", "127.0.0.1:0"},
		setKeys(map[string]any{"services.rpc.timeouts.read": "soon"}))
	assert.Equal(t, 2, oe.ExitCode)
	assert.Contains(t, oe.Error(), "services.rpc.timeouts.read")
}
