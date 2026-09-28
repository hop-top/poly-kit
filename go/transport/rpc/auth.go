package rpc

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/api"
)

// RefusedCall describes a call [Authenticate] refused: what the
// transport knows before any handler ran.
type RefusedCall struct {
	// Procedure is the full procedure name, "/pkg.Service/Method".
	Procedure string
	// Peer is the remote end: its address and the protocol spoken
	// ("connect", "grpc", "grpcweb").
	Peer connect.Peer
	// Header is the request's headers.
	Header http.Header
}

// AuthOption configures [Authenticate].
type AuthOption func(*authConfig)

type authConfig struct {
	onRefused func(ctx context.Context, call RefusedCall, err error)
	challenge string
}

// AuthChallenge sets the WWW-Authenticate challenge a refused call
// carries in its error metadata, which Connect sends as a response
// header: an OAuth protected resource's
// ([api.ProtectedResource.Challenge]), naming its metadata document
// (RFC 9728 §5.1), so a client refused here finds the authorization
// server. Without it a refusal carries no challenge.
func AuthChallenge(challenge string) AuthOption {
	return func(c *authConfig) { c.challenge = challenge }
}

// OnAuthRefused installs a hook called once for every call
// [Authenticate] refuses, before the refusal is sent. It is the audit
// seam: an unauthenticated call reaches no handler, so without it the
// refusal leaves no record.
func OnAuthRefused(fn func(ctx context.Context, call RefusedCall, err error)) AuthOption {
	return func(c *authConfig) { c.onRefused = fn }
}

type verifiedKey struct{}

// Authenticated reports whether [Authenticate] verified the call ctx
// belongs to. It is the answer to "did this caller authenticate",
// which a nil claims value cannot give: an [api.AuthFunc] may accept
// a request and return no claims.
func Authenticated(ctx context.Context) bool {
	v, _ := ctx.Value(verifiedKey{}).(bool)
	return v
}

// Authenticate returns an interceptor that calls fn for every call,
// unary and streaming alike, before the handler runs. A call fn
// refuses fails with CodeUnauthenticated in the protocol the client
// spoke; one it accepts carries fn's claims ([ClaimsFromContext]) and
// the verified mark ([Authenticated]) in its context.
//
// fn receives a synthetic *http.Request carrying the call's headers,
// its context, and the peer address as RemoteAddr. The URL, Method and
// Body are not the call's: an AuthFunc shared with the REST surface
// must decide on headers alone, which is what a bearer or API-key
// check does.
//
// Client-side streaming calls pass through untouched: interceptors
// installed on a handler only wrap the handler side.
func Authenticate(fn api.AuthFunc, opts ...AuthOption) connect.Interceptor {
	cfg := &authConfig{}
	for _, o := range opts {
		o(cfg)
	}
	return &authInterceptor{fn: fn, cfg: cfg}
}

type authInterceptor struct {
	fn  api.AuthFunc
	cfg *authConfig
}

// verify runs fn and returns the context the handler runs in, or the
// refusal.
func (a *authInterceptor) verify(
	ctx context.Context, call RefusedCall,
) (context.Context, error) {
	hr := (&http.Request{Header: call.Header.Clone(), RemoteAddr: call.Peer.Addr}).WithContext(ctx)
	claims, err := a.fn(hr)
	if err != nil {
		if a.cfg.onRefused != nil {
			a.cfg.onRefused(ctx, call, err)
		}
		refusal := connect.NewError(connect.CodeUnauthenticated, err)
		if a.cfg.challenge != "" {
			refusal.Meta().Set("WWW-Authenticate", a.cfg.challenge)
		}
		return nil, refusal
	}
	ctx = context.WithValue(ctx, claimsKeyType{}, claims)
	return context.WithValue(ctx, verifiedKey{}, true), nil
}

func (a *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Spec().IsClient {
			return next(ctx, req)
		}
		ctx, err := a.verify(ctx, RefusedCall{
			Procedure: req.Spec().Procedure,
			Peer:      req.Peer(),
			Header:    req.Header(),
		})
		if err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (a *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (a *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx, err := a.verify(ctx, RefusedCall{
			Procedure: conn.Spec().Procedure,
			Peer:      conn.Peer(),
			Header:    conn.RequestHeader(),
		})
		if err != nil {
			return err
		}
		return next(ctx, conn)
	}
}
