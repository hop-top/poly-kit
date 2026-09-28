package cmdsurface

import (
	"context"
	"fmt"
	"io"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
)

// WithRPCAdmittedInterceptors installs interceptors that see only calls
// every gate admitted. They run after the server's and
// WithRPCInterceptors' interceptors, after the handler's own gates
// (kit/auth-required, confirmation) and [Bridge.Admit] (exposure,
// invocability, the destructive ceiling, the permission gate), and
// around the run. A call a gate refuses never reaches them. A call one
// of them refuses does not run, and is audited with its error. They
// apply in the order given, the first outermost, as
// connect.WithInterceptors applies its own.
//
// The request they see is the one the client sent: its Meta is the
// client's claim, not the identity the host verified, and changing it
// does not change the admitted invocation. A streaming interceptor's
// conn replays the request message on its first Receive, then reports
// io.EOF.
func WithRPCAdmittedInterceptors(ic ...connect.Interceptor) RPCOption {
	return func(c *rpcConfig) { c.admitted = append(c.admitted, ic...) }
}

// runAdmittedUnary runs run inside the admitted interceptors and audits
// a call they refused before it ran.
func (s *rpcServer) runAdmittedUnary(
	ctx context.Context,
	req *connect.Request[cmdsurfacev1.Invocation],
	adm *Admission,
	run connect.UnaryFunc,
) (*connect.Response[cmdsurfacev1.Result], error) {
	ran := false
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ran = true
		return run(ctx, req)
	})
	for i := len(s.cfg.admitted) - 1; i >= 0; i-- {
		next = s.cfg.admitted[i].WrapUnary(next)
	}
	resp, err := next(ctx, req)
	if err != nil {
		if !ran {
			adm.Abandon()
			s.b.Audit(ctx, adm.Invocation(), Result{}, err)
		}
		return nil, err
	}
	typed, ok := resp.(*connect.Response[cmdsurfacev1.Result])
	if !ok {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("cmdsurface: interceptor returned %T, want *connect.Response[cmdsurfacev1.Result]", resp))
	}
	return typed, nil
}

// runAdmittedStream runs run inside the admitted interceptors on a
// conn replaying msg, and audits a call they refused before it ran.
func (s *rpcServer) runAdmittedStream(
	ctx context.Context,
	conn connect.StreamingHandlerConn,
	msg *cmdsurfacev1.Invocation,
	adm *Admission,
	run connect.StreamingHandlerFunc,
) error {
	ran := false
	next := connect.StreamingHandlerFunc(func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ran = true
		return run(ctx, conn)
	})
	for i := len(s.cfg.admitted) - 1; i >= 0; i-- {
		next = s.cfg.admitted[i].WrapStreamingHandler(next)
	}
	err := next(ctx, &replayConn{StreamingHandlerConn: conn, msg: msg})
	if err != nil && !ran {
		adm.Abandon()
		s.b.Audit(ctx, adm.Invocation(), Result{}, err)
	}
	return err
}

// replayConn hands an admitted interceptor the request message the
// handler already read: once, then io.EOF, as a client that sent one
// message and closed.
type replayConn struct {
	connect.StreamingHandlerConn
	msg  *cmdsurfacev1.Invocation
	read bool
}

func (c *replayConn) Receive(m any) error {
	if c.read {
		return io.EOF
	}
	dst, ok := m.(*cmdsurfacev1.Invocation)
	if !ok {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("cmdsurface: receive into %T, want *cmdsurfacev1.Invocation", m))
	}
	c.read = true
	proto.Reset(dst)
	proto.Merge(dst, c.msg)
	return nil
}
