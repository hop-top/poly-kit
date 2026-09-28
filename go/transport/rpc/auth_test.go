package rpc_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/rpc"
)

const (
	echoProcedure   = "/test.v1.Echo/Say"
	streamProcedure = "/test.v1.Echo/Repeat"
)

// authServer mounts one unary and one server-streaming procedure behind
// Authenticate. Each handler answers with the principal it saw, or
// "anonymous" when the call carried no verified mark.
func authServer(t *testing.T, fn api.AuthFunc, opts ...rpc.AuthOption) *httptest.Server {
	t.Helper()
	ic := connect.WithInterceptors(rpc.Authenticate(fn, opts...))
	who := func(ctx context.Context) string {
		if !rpc.Authenticated(ctx) {
			return "anonymous"
		}
		p, _ := api.IdentityOf(rpc.ClaimsFromContext(ctx))
		return p
	}
	mux := http.NewServeMux()
	mux.Handle(echoProcedure, connect.NewUnaryHandler(echoProcedure,
		func(ctx context.Context, _ *connect.Request[wrapperspb.StringValue]) (*connect.Response[wrapperspb.StringValue], error) {
			return connect.NewResponse(wrapperspb.String(who(ctx))), nil
		}, ic))
	mux.Handle(streamProcedure, connect.NewServerStreamHandler(streamProcedure,
		func(ctx context.Context, _ *connect.Request[wrapperspb.StringValue], s *connect.ServerStream[wrapperspb.StringValue]) error {
			return s.Send(wrapperspb.String(who(ctx)))
		}, ic))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func bearerAlice(r *http.Request) (any, error) {
	if r.Header.Get("Authorization") != "Bearer good" {
		return nil, errors.New("bad token")
	}
	return api.Claims{Subject: "alice"}, nil
}

func say(t *testing.T, ts *httptest.Server, token string) (string, error) {
	t.Helper()
	c := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](ts.Client(), ts.URL+echoProcedure)
	req := connect.NewRequest(wrapperspb.String("hi"))
	if token != "" {
		req.Header().Set("Authorization", "Bearer "+token)
	}
	resp, err := c.CallUnary(context.Background(), req)
	if err != nil {
		return "", err
	}
	return resp.Msg.GetValue(), nil
}

func repeat(t *testing.T, ts *httptest.Server, token string) (string, error) {
	t.Helper()
	c := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](ts.Client(), ts.URL+streamProcedure)
	req := connect.NewRequest(wrapperspb.String("hi"))
	if token != "" {
		req.Header().Set("Authorization", "Bearer "+token)
	}
	stream, err := c.CallServerStream(context.Background(), req)
	if err != nil {
		return "", err
	}
	defer func() { _ = stream.Close() }()
	var got string
	for stream.Receive() {
		got = stream.Msg().GetValue()
	}
	return got, stream.Err()
}

// TestAuthenticateCoversStreamingHandlers pins the property the unary
// AuthInterceptor lacks: a streaming procedure is refused without a
// credential, not run anonymously.
func TestAuthenticateCoversStreamingHandlers(t *testing.T) {
	ts := authServer(t, bearerAlice)

	_, err := repeat(t, ts, "")
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "stream without a credential")
	_, err = repeat(t, ts, "forged")
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "stream with a bad credential")

	got, err := repeat(t, ts, "good")
	require.NoError(t, err)
	assert.Equal(t, "alice", got)
}

func TestAuthenticateCoversUnaryHandlers(t *testing.T) {
	ts := authServer(t, bearerAlice)

	_, err := say(t, ts, "")
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	got, err := say(t, ts, "good")
	require.NoError(t, err)
	assert.Equal(t, "alice", got)
}

// TestAuthenticateMarksNilClaimsVerified pins that an AuthFunc accepting
// a call with no claims still marks it authenticated.
func TestAuthenticateMarksNilClaimsVerified(t *testing.T) {
	ts := authServer(t, func(*http.Request) (any, error) { return nil, nil })
	got, err := say(t, ts, "")
	require.NoError(t, err)
	assert.Equal(t, "", got, "verified, with no principal")
}

func TestAuthenticateReportsRefusals(t *testing.T) {
	var (
		mu    sync.Mutex
		calls []rpc.RefusedCall
		errs  []error
	)
	ts := authServer(t, bearerAlice, rpc.OnAuthRefused(func(_ context.Context, c rpc.RefusedCall, err error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, c)
		errs = append(errs, err)
	}))

	_, _ = say(t, ts, "forged")
	_, _ = repeat(t, ts, "")
	_, err := say(t, ts, "good")
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, calls, 2, "one report per refusal, none for the accepted call")
	assert.Equal(t, echoProcedure, calls[0].Procedure)
	assert.Equal(t, streamProcedure, calls[1].Procedure)
	assert.Equal(t, "Bearer forged", calls[0].Header.Get("Authorization"))
	assert.NotEmpty(t, calls[0].Peer.Addr)
	assert.Equal(t, "connect", calls[0].Peer.Protocol)
	assert.EqualError(t, errs[0], "bad token")
}

// TestAuthenticatePassesThePeerAddress pins that the synthetic request
// carries the caller's address, for an AuthFunc that allowlists hosts.
func TestAuthenticatePassesThePeerAddress(t *testing.T) {
	var remote string
	ts := authServer(t, func(r *http.Request) (any, error) {
		remote = r.RemoteAddr
		return nil, nil
	})
	_, err := say(t, ts, "")
	require.NoError(t, err)
	assert.Contains(t, remote, "127.0.0.1:")
}
