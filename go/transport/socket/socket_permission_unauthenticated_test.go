package socket_test

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
	"hop.top/kit/go/transport/transportsvc"
)

// A permission refusal that says it is an authentication failure is
// UNAUTHENTICATED on the wire, the answer a refused authenticator
// gets; an ordinary refusal stays DENIED. Both are audited with their
// class and the host's reason.
func TestPermissionUnauthenticatedIsUnauthenticatedOnTheWire(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	runner := newRecordingRunner(false)
	rec := &sinkRecorder{}
	startSocketWith(t, path,
		func(_ context.Context, _ net.Conn, req socket.Request) (socket.Identity, error) {
			return socket.Identity{Principal: req.Caller}, nil
		},
		transportsvc.WithBridgeOptions(
			cmdsurface.WithRunner(runner),
			cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: rec, OnError: true, OnOK: true}),
			cmdsurface.WithPermission(func(_ context.Context, meta cmdsurface.Meta, _ *cmdsurface.Leaf) cmdsurface.PermissionDecision {
				switch meta.Caller {
				case "ghost":
					return cmdsurface.PermissionDecision{Reason: "unknown_principal", Unauthenticated: true}
				case "mallory":
					return cmdsurface.PermissionDecision{Reason: "no_grant"}
				}
				return cmdsurface.PermissionDecision{Allowed: true}
			}),
		))

	resp := call(t, path, socket.Request{Path: []string{"ping"}, Caller: "ghost"})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeUnauthenticated, resp.Error.Code)
	assert.Equal(t,
		"cmdsurface: authentication refused: ping on socket: unknown_principal",
		resp.Error.Message)

	resp = call(t, path, socket.Request{Path: []string{"ping"}, Caller: "mallory"})
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeDenied, resp.Error.Code)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.errs, 2)
	assert.ErrorIs(t, rec.errs[0], cmdsurface.ErrAuthRefused)
	assert.NotErrorIs(t, rec.errs[0], cmdsurface.ErrPermissionDenied)
	assert.Contains(t, rec.errs[0].Error(), "unknown_principal")
	assert.ErrorIs(t, rec.errs[1], cmdsurface.ErrPermissionDenied)
	assert.NotErrorIs(t, rec.errs[1], cmdsurface.ErrAuthRefused)
	assert.Empty(t, runner.invocation().Path, "no refused call may run")
}
