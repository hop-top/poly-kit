package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/api"
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

// RPCOption configures MountRPC.
type RPCOption func(*rpcConfig)

type rpcConfig struct {
	interceptors     []connect.Interceptor
	admitted         []connect.Interceptor
	handlerOpts      []connect.HandlerOption
	callMeta         func(ctx context.Context, req connect.AnyRequest, claimed Meta) Meta
	authenticated    func(ctx context.Context, req connect.AnyRequest) bool
	maxBody          int64
	compressMinBytes int
}

// WithRPCMaxBodyBytes caps each request message at n bytes, after
// decompression, through connect.WithReadMaxBytes. Zero keeps the
// default, api.DefaultMaxBodyBytes (1 MiB); a negative n disables the
// cap. An oversized message is refused by connect-go itself with
// CodeResourceExhausted on every protocol it serves (HTTP 429 on the
// Connect unary wire, grpc-status 8 on gRPC and gRPC-Web).
func WithRPCMaxBodyBytes(n int64) RPCOption {
	return func(c *rpcConfig) { c.maxBody = n }
}

// WithRPCInterceptors appends interceptors run on top of the server's
// own Interceptors().
func WithRPCInterceptors(ic ...connect.Interceptor) RPCOption {
	return func(c *rpcConfig) { c.interceptors = append(c.interceptors, ic...) }
}

// WithRPCHandlerOptions passes options to the generated handler after
// the interceptors: connect.WithReadMaxBytes to bound a request
// message, connect.WithCompressMinBytes, and the like. They apply
// last: a connect.WithReadMaxBytes here overrides WithRPCMaxBodyBytes,
// and a connect.WithCompressMinBytes overrides WithRPCCompression.
func WithRPCHandlerOptions(opts ...connect.HandlerOption) RPCOption {
	return func(c *rpcConfig) { c.handlerOpts = append(c.handlerOpts, opts...) }
}

// WithRPCCallMeta installs fn to supply each call's provenance: the
// [Meta] the bridge's permission gate and audit sinks see. claimed is
// what the client put in the Invocation's meta; fn returns the Meta
// to run with. Surface is pinned to [SurfaceRPC] and RequestedAt is
// stamped afterwards whatever fn returns.
//
// fn fills Caller and Tenant only from an identity the host verified,
// and sets Established when it did: a caller named in the request body
// is a claim, not a principal. Without fn the claimed Meta is used as
// sent, which is right only when every client is trusted; it is never
// established, since no body can carry [Meta.Established].
func WithRPCCallMeta(fn func(ctx context.Context, req connect.AnyRequest, claimed Meta) Meta) RPCOption {
	return func(c *rpcConfig) { c.callMeta = fn }
}

// WithRPCAuthenticated installs the predicate the gate for
// kit/auth-required leaves asks: whether the caller is authenticated.
// A call it accepts runs as [EstablishedVerified] (unless WithRPCCallMeta
// already established it); one it refuses runs unestablished.
//
// Without it the gate reads the Meta WithRPCCallMeta returned: only
// an established one is authenticated. An Authorization header or a
// claimed caller is presence, not verification, so a bare MountRPC
// refuses every kit/auth-required leaf until the host says who
// verified the call.
func WithRPCAuthenticated(fn func(ctx context.Context, req connect.AnyRequest) bool) RPCOption {
	return func(c *rpcConfig) { c.authenticated = fn }
}

// WithRPCCompression turns on gzip compression of response messages
// of at least minBytes, for clients that accept it; zero or less
// compresses every message. api.DefaultCompressMinBytes is the floor
// the REST surface uses. Connect, gRPC and gRPC-Web each negotiate
// compression per message in their own headers, which is why this is
// a handler option and the api package's HTTP Compress middleware
// passes their requests through.
//
// Without it, responses are sent uncompressed. Compressed requests are
// accepted either way.
func WithRPCCompression(minBytes int) RPCOption {
	return func(c *rpcConfig) { c.compressMinBytes = max(minBytes, 0) }
}

// rpcNoCompression is a message-size floor no message reaches: the
// handler keeps gzip registered, so compressed requests still decode,
// but never compresses a response.
const rpcNoCompression = math.MaxInt32

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
//   - cancels the running Stream goroutine when the client disconnects;
//   - caps each request message at WithRPCMaxBodyBytes (default 1 MiB);
//   - sends responses uncompressed unless [WithRPCCompression] opts in.
//
// Options wire the host's own gates in: WithRPCInterceptors for
// authentication and the like, WithRPCCallMeta for the provenance the
// host verified, WithRPCAuthenticated for the kit/auth-required gate,
// and WithRPCHandlerOptions for a message size bound. The rpc service
// in go/console/cli/rpcserve sets all four.
// WithRPCAdmittedInterceptors adds interceptors that see only the
// calls every gate admitted.
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
	cfg := rpcConfig{compressMinBytes: rpcNoCompression}
	for _, o := range opts {
		o(&cfg)
	}

	srv := &rpcServer{b: b, index: indexLeaves(b), cfg: cfg}

	// Server interceptors first, then caller-supplied ones.
	hopts := []connect.HandlerOption{connect.WithCompressMinBytes(cfg.compressMinBytes)}
	ics := append([]connect.Interceptor{}, s.Interceptors()...)
	ics = append(ics, cfg.interceptors...)
	if len(ics) > 0 {
		hopts = append(hopts, connect.WithInterceptors(ics...))
	}
	// The body cap goes before the caller's handler options, so an
	// explicit connect.WithReadMaxBytes there wins over it.
	if limit := api.MaxBodyBytesOrDefault(cfg.maxBody); limit > 0 {
		hopts = append(hopts, connect.WithReadMaxBytes(clampReadMaxBytes(limit)))
	}
	hopts = append(hopts, cfg.handlerOpts...)

	path, handler := cmdsurfacev1connect.NewCommandsHandler(srv, hopts...)
	s.Handle(path, handler)
	return nil
}

// clampReadMaxBytes narrows a byte cap to connect's int option,
// saturating rather than wrapping on a 32-bit platform.
func clampReadMaxBytes(n int64) int {
	if n > int64(math.MaxInt) {
		return math.MaxInt
	}
	return int(n)
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
	adm, err := s.b.Admit(ctx, inv)
	if err != nil {
		return nil, mapBridgeError(err, leaf)
	}
	run := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		res, err := adm.Run(ctx)
		if err != nil {
			return nil, mapBridgeError(err, leaf)
		}
		out, err := resultToProto(res)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(out), nil
	}
	return s.runAdmittedUnary(ctx, req, adm, run)
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
	run := func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return s.stream(ctx, conn, adm, leaf)
	}
	return s.runAdmittedStream(ctx, stream.Conn(), req.Msg, adm, run)
}

// stream runs an admitted invocation and forwards its Events to conn.
func (s *rpcServer) stream(ctx context.Context, conn connect.StreamingHandlerConn, adm *Admission, leaf *Leaf) error {
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
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				// The caller's own deadline (Connect-Timeout-Ms,
				// grpc-timeout) ran out.
				return connect.NewError(connect.CodeDeadlineExceeded, ctx.Err())
			}
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
			if err := conn.Send(msg); err != nil {
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

// authenticated answers the kit/auth-required gate and records the
// answer on inv.Meta: the installed predicate, or, without one,
// whether the call's Meta is already established.
func (s *rpcServer) authenticated(ctx context.Context, req connect.AnyRequest, inv *Invocation) bool {
	if s.cfg.authenticated == nil {
		return inv.Meta.Authenticated()
	}
	if !s.cfg.authenticated(ctx, req) {
		inv.Meta.Established = EstablishedNone
		return false
	}
	if !inv.Meta.Authenticated() {
		inv.Meta.Established = EstablishedVerified
	}
	return true
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
	if !s.authenticated(ctx, req, inv) && leaf.Class.AuthRequired {
		// Answered here, before the confirmation gate, so an
		// unauthenticated caller learns nothing else; the bridge
		// refuses the same call at its own slot.
		err := fmt.Errorf("%w: %s on %s requires an authenticated caller",
			ErrAuthRefused, leaf.PathKey(), SurfaceRPC)
		refused := *inv
		refused.Path = append([]string(nil), leaf.Path...)
		refused.Meta.Surface = SurfaceRPC
		if refused.Meta.RequestedAt.IsZero() {
			refused.Meta.RequestedAt = time.Now()
		}
		s.b.Audit(ctx, refused, Result{}, err)
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	if leaf.Class.RequiresConfirmation {
		if header.Get(confirmHeader) == "" {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("confirmation_required"))
		}
	}
	// W3C trace context rides the request headers on every protocol
	// Connect serves; the message's own trace_id stays authoritative
	// for correlation when the caller set it.
	if inv.Meta.Traceparent == "" {
		inv.Meta.Traceparent, inv.Meta.Tracestate = api.TraceContextFromHeader(header)
	}
	if inv.Meta.TraceID == "" {
		inv.Meta.TraceID = api.TraceIDFromHeader(header)
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
	case errors.Is(err, ErrAuthRefused):
		return connect.NewError(connect.CodeUnauthenticated, err)
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
	case errors.Is(err, ErrRateLimited):
		return rateLimitedConnectError(err)
	case errors.Is(err, ErrDeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
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
