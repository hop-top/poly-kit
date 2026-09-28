package rpcserve_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/rpcserve"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
)

// callLog records what adopter interceptors saw, in order.
type callLog struct {
	mu    sync.Mutex
	calls []string
	sent  atomic.Int32
}

func (l *callLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, s)
}

func (l *callLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

// spy is an adopter interceptor. It logs "<name> <kind> <path>" for
// every call it sees and refuses the command named refuse. On a
// stream, the outermost spy (peek) reads the request message from the
// conn and hands its path down in the context: a stream carries one
// request message, so only one interceptor can receive it.
type spy struct {
	name   string
	log    *callLog
	refuse string
	peek   bool
}

type pathKey struct{}

func (s spy) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		path := strings.Join(req.Any().(*cmdsurfacev1.Invocation).GetPath(), " ")
		s.log.add(s.name + " unary " + path)
		if path == s.refuse {
			return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("quota spent"))
		}
		return next(ctx, req)
	}
}

func (s spy) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (s spy) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if s.peek {
			// The conn replays the request message the handler read.
			var inv cmdsurfacev1.Invocation
			if err := conn.Receive(&inv); err != nil {
				return err
			}
			ctx = context.WithValue(ctx, pathKey{}, strings.Join(inv.GetPath(), " "))
		}
		path, _ := ctx.Value(pathKey{}).(string)
		s.log.add(s.name + " stream " + path)
		if path == s.refuse {
			return connect.NewError(connect.CodeResourceExhausted, errors.New("quota spent"))
		}
		return next(ctx, &countingConn{StreamingHandlerConn: conn, sent: &s.log.sent})
	}
}

// countingConn counts the messages the handler sends through an
// adopter's wrapped conn.
type countingConn struct {
	connect.StreamingHandlerConn
	sent *atomic.Int32
}

func (c *countingConn) Send(m any) error {
	c.sent.Add(1)
	return c.StreamingHandlerConn.Send(m)
}

// TestRPCServiceInterceptorsRunInsideKitsGates pins that adopter
// interceptors see only calls every kit gate admitted: a call Auth,
// the kit/auth-required or confirmation gate, exposure, the
// destructive ceiling or the permission gate refuses never reaches
// them, unary or streaming. An admitted call reaches them in order,
// the first outermost, and a call they refuse does not run and is
// audited with their error.
func TestRPCServiceInterceptorsRunInsideKitsGates(t *testing.T) {
	deny := func(_ context.Context, _ cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		if leaf.PathKey() == "forever" {
			return cmdsurface.PermissionDecision{Reason: "not for you"}
		}
		return cmdsurface.PermissionDecision{Allowed: true}
	}
	good := http.Header{"Authorization": {"Bearer good"}}

	t.Run("refused by kit", func(t *testing.T) {
		log := &callLog{}
		ics := []connect.Interceptor{spy{name: "a", log: log, peek: true}}
		authed := startDefault(t, rpcserve.Config{Auth: bearerAuth, Interceptors: ics}, cli.WithPermission(deny))
		anon := startDefault(t, rpcserve.Config{Interceptors: ics})

		cases := []struct {
			name, base, path string
			hdr              http.Header
			code             connect.Code
		}{
			{"auth", authed, "ping", nil, connect.CodeUnauthenticated},
			{"auth-required", anon, "secret", nil, connect.CodeUnauthenticated},
			{"confirmation", authed, "deploy", good, connect.CodeFailedPrecondition},
			{"not exposed", authed, "shell", good, connect.CodeNotFound},
			{"unknown", authed, "nope", good, connect.CodeNotFound},
			{"destructive", authed, "nuke", good, connect.CodePermissionDenied},
			{"permission", authed, "forever", good, connect.CodePermissionDenied},
		}
		for _, p := range protocols {
			for _, tc := range cases {
				c := client(tc.base, p)
				_, err := c.Invoke(t.Context(), call(tc.path, tc.hdr))
				assert.Equal(t, tc.code, connect.CodeOf(err), "%s unary %s", p.name, tc.name)
				_, _, err = streamAll(t.Context(), c, call(tc.path, tc.hdr))
				assert.Equal(t, tc.code, connect.CodeOf(err), "%s stream %s", p.name, tc.name)
			}
		}
		assert.Empty(t, log.snapshot(), "a refused call never reaches an adopter interceptor")
	})

	t.Run("admitted", func(t *testing.T) {
		log := &callLog{}
		rec := &recorder{}
		base := startDefault(t, rpcserve.Config{
			Auth:         bearerAuth,
			Interceptors: []connect.Interceptor{spy{name: "a", log: log, peek: true}, spy{name: "b", log: log, refuse: "tick"}},
		}, cli.WithAuditSinks(rec.spec()))
		c := client(base, protocols[0])

		resp, err := c.Invoke(t.Context(), call("ping", good))
		require.NoError(t, err)
		assert.Equal(t, "pong", resp.Msg.GetStdout())

		// The adopter's refusal is the call's answer; the command does
		// not run.
		_, err = c.Invoke(t.Context(), call("tick", good))
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
		_, _, err = streamAll(t.Context(), c, call("tick", good))
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

		// The handler's messages pass through the adopter's conn.
		deploy := http.Header{"Authorization": {"Bearer good"}, "X-Confirm-Token": {"yes"}}
		_, res, err := streamAll(t.Context(), c, call("deploy", deploy))
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, int32(0), res.GetExitCode())

		assert.Equal(t, []string{
			"a unary ping", "b unary ping",
			"a unary tick", "b unary tick",
			"a stream tick", "b stream tick",
			"a stream deploy", "b stream deploy",
		}, log.snapshot())
		assert.Positive(t, log.sent.Load(), "the stream's events went through the adopter's conn")

		rec.mu.Lock()
		defer rec.mu.Unlock()
		var runs, refusals int
		for i, inv := range rec.invs {
			if inv.Meta.Caller != "alice" {
				continue
			}
			switch {
			case rec.errs[i] == nil:
				runs++
				assert.NotEqual(t, "tick", strings.Join(inv.Path, " "), "a refused tick ran")
			case strings.Contains(rec.errs[i].Error(), "quota spent"):
				refusals++
				assert.Equal(t, "tick", strings.Join(inv.Path, " "))
			}
		}
		assert.Equal(t, 2, runs, "ping and deploy ran")
		assert.Equal(t, 2, refusals, "the adopter's refusals are audited")
	})
}
