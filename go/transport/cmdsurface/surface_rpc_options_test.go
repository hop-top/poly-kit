package cmdsurface_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
)

// TestRPCCallMetaReplacesTheClaimedMeta pins that WithRPCCallMeta sees
// what the body claimed and that its answer, not the claim, reaches the
// runner — with Surface still pinned, whatever the hook returned.
func TestRPCCallMetaReplacesTheClaimedMeta(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		var claimed cmdsurface.Meta
		f.start(cmdsurface.WithRPCCallMeta(
			func(_ context.Context, req connect.AnyRequest, c cmdsurface.Meta) cmdsurface.Meta {
				claimed = c
				return cmdsurface.Meta{
					Caller:  "verified",
					Surface: cmdsurface.SurfaceREST,
					Extra:   map[string]string{"proto": req.Peer().Protocol},
				}
			}))

		req := connect.NewRequest(&cmdsurfacev1.Invocation{
			Path: []string{"echo"},
			Meta: &cmdsurfacev1.Meta{Caller: "root", Extra: map[string]string{"scopes": "admin"}},
		})
		_, err := f.client(p).Invoke(context.Background(), req)
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if claimed.Caller != "root" {
			t.Errorf("hook saw claimed caller %q, want root", claimed.Caller)
		}
		got := f.runner.LastInvocation.Meta
		if got.Caller != "verified" {
			t.Errorf("runner saw caller %q, want the hook's", got.Caller)
		}
		if got.Surface != cmdsurface.SurfaceRPC {
			t.Errorf("surface %q, want pinned rpc", got.Surface)
		}
		if _, ok := got.Extra["scopes"]; ok {
			t.Error("the claimed extra map survived the hook")
		}
		if got.Extra["proto"] == "" {
			t.Error("hook's extra lost")
		}
	})
}

// TestRPCAuthenticatedReplacesHeaderPresence pins that, with the
// predicate installed, an Authorization header alone no longer admits
// a kit/auth-required leaf, on unary and streaming calls alike.
func TestRPCAuthenticatedReplacesHeaderPresence(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		var admit atomic.Bool
		f.start(cmdsurface.WithRPCAuthenticated(func(context.Context, connect.AnyRequest) bool { return admit.Load() }))
		client := f.client(p)

		req := invocation("secret")
		req.Header().Set("Authorization", "Bearer made-up")
		_, err := client.Invoke(context.Background(), req)
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("unary code=%v want Unauthenticated", connect.CodeOf(err))
		}
		sreq := invocation("secret")
		sreq.Header().Set("Authorization", "Bearer made-up")
		stream, err := client.InvokeStream(context.Background(), sreq)
		if err == nil {
			for stream.Receive() {
			}
			err = stream.Err()
		}
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("stream code=%v want Unauthenticated", connect.CodeOf(err))
		}

		admit.Store(true)
		if _, err := client.Invoke(context.Background(), invocation("secret")); err != nil {
			t.Fatalf("admitted call: %v", err)
		}
	})
}

// TestRPCHandlerOptionsBoundTheMessage pins that a read bound passed
// through WithRPCHandlerOptions refuses an oversized message.
func TestRPCHandlerOptionsBoundTheMessage(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		f.start(cmdsurface.WithRPCHandlerOptions(connect.WithReadMaxBytes(1024)))
		req := connect.NewRequest(&cmdsurfacev1.Invocation{
			Path: []string{"echo"},
			Args: []string{strings.Repeat("x", 4096)},
		})
		_, err := f.client(p).Invoke(context.Background(), req)
		var ce *connect.Error
		if !errors.As(err, &ce) || ce.Code() != connect.CodeResourceExhausted {
			t.Fatalf("err=%v want ResourceExhausted", err)
		}
	})
}

// countingInterceptor counts the unary and streaming calls it sees.
type countingInterceptor struct{ unary, stream atomic.Int32 }

func (c *countingInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		c.unary.Add(1)
		return next(ctx, req)
	}
}

func (c *countingInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (c *countingInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		c.stream.Add(1)
		return next(ctx, conn)
	}
}

// TestRPCAdmittedInterceptorsSeeOnlyAdmittedCalls pins that an
// interceptor installed with WithRPCAdmittedInterceptors is skipped
// by every call a gate refuses — unknown, not enabled, destructive,
// unconfirmed — and wraps every admitted one, unary and streaming.
func TestRPCAdmittedInterceptorsSeeOnlyAdmittedCalls(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		f := newFixture(t)
		ic := &countingInterceptor{}
		f.start(cmdsurface.WithRPCAdmittedInterceptors(ic))
		client := f.client(p)

		for _, path := range []string{"nope", "hidden-rpc", "destroy", "confirm"} {
			if _, err := client.Invoke(context.Background(), invocation(path)); err == nil {
				t.Fatalf("%s: unary admitted", path)
			}
			stream, err := client.InvokeStream(context.Background(), invocation(path))
			if err == nil {
				for stream.Receive() {
				}
				err = stream.Err()
			}
			if err == nil {
				t.Fatalf("%s: stream admitted", path)
			}
		}
		if n := ic.unary.Load() + ic.stream.Load(); n != 0 {
			t.Fatalf("refused calls reached the interceptor %d times", n)
		}

		if _, err := client.Invoke(context.Background(), invocation("echo")); err != nil {
			t.Fatalf("unary: %v", err)
		}
		stream, err := client.InvokeStream(context.Background(), invocation("lines"))
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		for stream.Receive() {
		}
		if err := stream.Err(); err != nil {
			t.Fatalf("stream: %v", err)
		}
		if ic.unary.Load() != 1 || ic.stream.Load() != 1 {
			t.Fatalf("admitted calls seen unary=%d stream=%d, want 1 and 1", ic.unary.Load(), ic.stream.Load())
		}
	})
}
