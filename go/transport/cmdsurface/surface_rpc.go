package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
)

// RPCServicePath is the fixed service mount path of
// cmdsurface.v1.Commands. Surfaces that want to address Invoke /
// InvokeStream directly construct URLs against this prefix.
const RPCServicePath = "/" + cmdsurfacev1connect.CommandsName + "/"

// Per-procedure paths (the URL suffixes Connect routes on).
const (
	// RPCInvokeProcedure is the unary Invoke method URL.
	RPCInvokeProcedure = cmdsurfacev1connect.CommandsInvokeProcedure
	// RPCInvokeStreamProcedure is the server-streaming InvokeStream
	// method URL.
	RPCInvokeStreamProcedure = cmdsurfacev1connect.CommandsInvokeStreamProcedure
)

// confirmHeader is the request header clients set to satisfy
// SafetyClass.RequiresConfirmation. Any non-empty value is accepted —
// the bridge does not validate token contents.
const confirmHeader = "X-Confirm-Token"

// authHeader is the canonical request header inspected for presence
// when SafetyClass.AuthRequired is true. An entry in inv.Meta.Caller
// is treated as an acceptable substitute.
const authHeader = "Authorization"

// RPCOption configures MountRPC.
type RPCOption func(*rpcConfig)

type rpcConfig struct {
	interceptors  []connect.Interceptor
	handlerOpts   []connect.HandlerOption
	callMeta      func(ctx context.Context, req connect.AnyRequest, claimed Meta) Meta
	authenticated func(ctx context.Context, req connect.AnyRequest) bool
}

// WithRPCInterceptors appends interceptors run on top of the server's
// own Interceptors().
func WithRPCInterceptors(ic ...connect.Interceptor) RPCOption {
	return func(c *rpcConfig) { c.interceptors = append(c.interceptors, ic...) }
}

// WithRPCHandlerOptions passes options to the generated handler after
// the interceptors: connect.WithReadMaxBytes to bound a request
// message, connect.WithCompressMinBytes, and the like. Without a
// read bound a handler reads a message of any size.
func WithRPCHandlerOptions(opts ...connect.HandlerOption) RPCOption {
	return func(c *rpcConfig) { c.handlerOpts = append(c.handlerOpts, opts...) }
}

// WithRPCCallMeta installs fn to supply each call's provenance: the
// [Meta] the bridge's permission gate and audit sinks see. claimed is
// what the client put in the Invocation's meta; fn returns the Meta
// to run with. Surface is pinned to [SurfaceRPC] and RequestedAt is
// stamped afterwards whatever fn returns.
//
// fn fills Caller and Tenant only from an identity the host verified:
// a caller named in the request body is a claim, not a principal.
// Without fn the claimed Meta is used as sent, which is right only
// when every client is trusted.
func WithRPCCallMeta(fn func(ctx context.Context, req connect.AnyRequest, claimed Meta) Meta) RPCOption {
	return func(c *rpcConfig) { c.callMeta = fn }
}

// WithRPCAuthenticated installs the predicate the gate for
// kit/auth-required leaves asks: whether the caller is authenticated.
// Without it the gate accepts any call carrying an Authorization
// header or a claimed caller, which is presence, not verification — a
// host that authenticates calls itself supplies the real answer here.
func WithRPCAuthenticated(fn func(ctx context.Context, req connect.AnyRequest) bool) RPCOption {
	return func(c *rpcConfig) { c.authenticated = fn }
}

// rpcServer wires a Bridge into the generated Commands handler. It is
// internal — callers reach it only via MountRPC.
type rpcServer struct {
	b     *Bridge
	index map[string]*Leaf
	cfg   rpcConfig
}

var _ cmdsurfacev1connect.CommandsHandler = (*rpcServer)(nil)

// MountRPC registers the cmdsurface.v1.Commands service on s, exposing
// every Bridge leaf where SurfaceRPC is enabled. The schema is
// contracts/proto/cmdsurface/v1/commands.proto; clients generate stubs
// from it or import the Go ones in gen/cmdsurfacev1/cmdsurfacev1connect.
// Two procedures are installed under RPCServicePath:
//
//	Invoke(Invocation)       -> Result        // unary
//	InvokeStream(Invocation) -> stream Event  // server-streaming
//
// The handler speaks every protocol connect-go serves: Connect (binary
// proto and JSON), gRPC and gRPC-Web. gRPC needs HTTP/2; without TLS
// that means an h2c server, which rpc.ListenAndServe provides.
//
// The handler:
//   - forces inv.Meta.Surface = SurfaceRPC;
//   - admits every call, unary or streaming, through the bridge's
//     gates (Bridge.Invoke / Bridge.Admit), which audit refusals, and
//     rejects unknown / non-enabled / non-invocable /
//     destructive-blocked / permission-denied leaves with the codes in
//     the package mapping table;
//   - returns Result with non-zero ExitCode as a success response
//     (clients inspect ExitCode themselves);
//   - cancels the running Stream goroutine when the client disconnects.
//
// Options wire the host's own gates in: WithRPCInterceptors for
// authentication and the like, WithRPCCallMeta for the provenance the
// host verified, WithRPCAuthenticated for the kit/auth-required gate,
// and WithRPCHandlerOptions for a message size bound. The rpc service
// in go/console/cli/rpcserve sets all four.
//
// s is required; b is required. Returns a wrapped error when either
// is nil.
func MountRPC(b *Bridge, s rpcServerMount, opts ...RPCOption) error {
	if b == nil {
		return errors.New("cmdsurface: MountRPC: nil Bridge")
	}
	if s == nil {
		return errors.New("cmdsurface: MountRPC: nil server")
	}
	cfg := rpcConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	srv := &rpcServer{b: b, index: indexLeaves(b), cfg: cfg}

	// Server interceptors first, then caller-supplied ones.
	var hopts []connect.HandlerOption
	ics := append([]connect.Interceptor{}, s.Interceptors()...)
	ics = append(ics, cfg.interceptors...)
	if len(ics) > 0 {
		hopts = append(hopts, connect.WithInterceptors(ics...))
	}
	hopts = append(hopts, cfg.handlerOpts...)

	path, handler := cmdsurfacev1connect.NewCommandsHandler(srv, hopts...)
	s.Handle(path, handler)
	return nil
}

// rpcServerMount is the subset of *rpc.Server MountRPC consumes. The
// indirection keeps cmdsurface free of an import cycle with
// hop.top/kit/go/transport/rpc while still accepting that concrete
// type at call sites.
type rpcServerMount interface {
	Handle(path string, h http.Handler)
	Interceptors() []connect.Interceptor
}

// indexLeaves builds a path-key -> *Leaf map snapshot. The snapshot
// is taken at mount time; later Expose / Hide calls update Enabled
// in-place on the same *Leaf, so the index stays valid.
func indexLeaves(b *Bridge) map[string]*Leaf {
	leaves := b.Leaves()
	out := make(map[string]*Leaf, len(leaves))
	for _, l := range leaves {
		out[l.PathKey()] = l
	}
	return out
}

// Invoke implements the unary Invoke procedure.
func (s *rpcServer) Invoke(
	ctx context.Context,
	req *connect.Request[cmdsurfacev1.Invocation],
) (*connect.Response[cmdsurfacev1.Result], error) {
	inv := s.invocation(ctx, req, req.Msg)
	leaf, cerr := s.preflight(ctx, req, &inv)
	if cerr != nil {
		return nil, cerr
	}
	res, err := s.b.Invoke(ctx, inv)
	if err != nil {
		return nil, mapBridgeError(err, leaf)
	}
	out, err := resultToProto(res)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(out), nil
}

// InvokeStream implements the server-streaming InvokeStream procedure.
// The invocation passes [Bridge.Admit] — the gates, order, errors and
// audit of [Bridge.Invoke] — before the first message, so a refusal is
// the stream's error with no Event sent. Events from the admitted run
// are forwarded one-per-Send; the goroutine closes when the runner
// exits or ctx is canceled (client disconnect).
func (s *rpcServer) InvokeStream(
	ctx context.Context,
	req *connect.Request[cmdsurfacev1.Invocation],
	stream *connect.ServerStream[cmdsurfacev1.Event],
) error {
	inv := s.invocation(ctx, req, req.Msg)
	leaf, cerr := s.preflight(ctx, req, &inv)
	if cerr != nil {
		return cerr
	}
	adm, err := s.b.Admit(ctx, inv)
	if err != nil {
		return mapBridgeError(err, leaf)
	}

	// Run the streamer in its own goroutine so we can multiplex Event
	// receipt with ctx cancellation observability.
	events := make(chan Event, 16)
	errc := make(chan error, 1)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		errc <- adm.Stream(streamCtx, events)
	}()

	// abort stops the runner and waits for it, so it never blocks on a
	// full, unread channel.
	abort := func() {
		cancel()
		drain(events)
		<-errc
	}

	for {
		select {
		case <-ctx.Done():
			abort()
			return connect.NewError(connect.CodeCanceled, ctx.Err())
		case ev, ok := <-events:
			if !ok {
				// Channel closed by runner. Wait for the run's err and
				// translate.
				if err := <-errc; err != nil {
					return mapBridgeError(err, leaf)
				}
				return nil
			}
			msg, err := eventToProto(ev)
			if err != nil {
				abort()
				return connect.NewError(connect.CodeInternal, err)
			}
			if err := stream.Send(msg); err != nil {
				abort()
				return err
			}
		}
	}
}

// invocation decodes the wire Invocation and resolves its Meta
// through the WithRPCCallMeta hook when one is installed.
func (s *rpcServer) invocation(
	ctx context.Context, req connect.AnyRequest, m *cmdsurfacev1.Invocation,
) Invocation {
	inv := invocationFromProto(m)
	if s.cfg.callMeta != nil {
		inv.Meta = s.cfg.callMeta(ctx, req, inv.Meta)
	}
	return inv
}

// authenticated answers the kit/auth-required gate: the installed
// predicate, or, without one, the presence of an Authorization header
// or a caller.
func (s *rpcServer) authenticated(ctx context.Context, req connect.AnyRequest, inv *Invocation) bool {
	if s.cfg.authenticated != nil {
		return s.cfg.authenticated(ctx, req)
	}
	return req.Header().Get(authHeader) != "" || inv.Meta.Caller != ""
}

// preflight validates leaf existence, surface enablement, and the
// gating headers. It overwrites inv.Path with the resolved leaf path
// and forces inv.Meta.Surface = SurfaceRPC. Returns the resolved
// leaf or a Connect error.
func (s *rpcServer) preflight(
	ctx context.Context,
	req connect.AnyRequest,
	inv *Invocation,
) (*Leaf, *connect.Error) {
	header := req.Header()
	if inv == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("cmdsurface: nil invocation"))
	}
	if len(inv.Path) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("cmdsurface: empty path"))
	}
	key := strings.Join(inv.Path, " ")
	leaf, ok := s.index[key]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("%w: %s", ErrUnknownCommand, key))
	}
	if !leaf.Enabled[SurfaceRPC] {
		return nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("%w: %s on %s",
				ErrSurfaceNotEnabled, leaf.PathKey(), SurfaceRPC))
	}
	if leaf.Class.AuthRequired {
		if !s.authenticated(ctx, req, inv) {
			return nil, connect.NewError(connect.CodeUnauthenticated,
				fmt.Errorf("auth required: %s", leaf.PathKey()))
		}
	}
	if leaf.Class.RequiresConfirmation {
		if header.Get(confirmHeader) == "" {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("confirmation_required"))
		}
	}
	// Canonicalise the invocation: resolved path + forced surface.
	inv.Path = append([]string(nil), leaf.Path...)
	inv.Meta.Surface = SurfaceRPC
	inv.Meta.RequestedAt = time.Now()
	return leaf, nil
}

// mapBridgeError translates the package sentinels to Connect codes
// per the mandatory mapping table. leaf is the resolved leaf (may be
// nil if mapping fires before resolution).
//
// A non-invocable leaf is NotFound, as over REST, where such a
// command is withheld and its route is absent: the command exists but
// is not reachable here, the same answer as a leaf not enabled on the
// surface. FailedPrecondition stays the confirmation refusal's code.
// A permission refusal shares PermissionDenied with the destructive
// ceiling; the message's sentinel tells them apart.
func mapBridgeError(err error, leaf *Leaf) error {
	_ = leaf
	switch {
	case errors.Is(err, ErrUnknownCommand):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrSurfaceNotEnabled):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrNotInvocable):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrDestructiveBlocked):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

// drain reads remaining events from ch until it is closed. Used on
// cancellation paths so the Runner goroutine never blocks on send.
func drain(ch <-chan Event) {
	for range ch {
	}
}

// RPCClientOptions returns connect.ClientOptions for a client built
// on the Go types, connect.NewClient[Invocation, Result] or
// [Invocation, Event]: they select a JSON codec over encoding/json,
// which the service's JSON wire accepts and answers in kind.
//
// Deprecated: use the generated client,
// cmdsurfacev1connect.NewCommandsClient, which speaks every protocol
// the service serves (Connect, gRPC, gRPC-Web) with typed messages.
func RPCClientOptions() []connect.ClientOption {
	return []connect.ClientOption{connect.WithCodec(jsonGoCodec{})}
}

// jsonGoCodec (un)marshals the Go wire types with encoding/json. Their
// struct tags name the same keys commands.proto sets as json_name, so
// the service's proto3 JSON codec reads what it writes and vice versa.
type jsonGoCodec struct{}

// Name implements connect.Codec; "json" selects application/json.
func (jsonGoCodec) Name() string { return "json" }

// Marshal implements connect.Codec.
func (jsonGoCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

// Unmarshal implements connect.Codec.
func (jsonGoCodec) Unmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return errors.New("cmdsurface: zero-length message body")
	}
	return json.Unmarshal(data, v)
}
