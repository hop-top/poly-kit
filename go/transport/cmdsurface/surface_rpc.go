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
)

// RPCServicePath is the fixed Connect service mount path. Surfaces
// that want to address Invoke / InvokeStream directly construct URLs
// against this prefix.
const RPCServicePath = "/cmdsurface.v1.Commands/"

// Per-procedure paths (the URL suffixes Connect routes on).
const (
	// RPCInvokeProcedure is the unary Invoke method URL.
	RPCInvokeProcedure = RPCServicePath + "Invoke"
	// RPCInvokeStreamProcedure is the server-streaming InvokeStream
	// method URL.
	RPCInvokeStreamProcedure = RPCServicePath + "InvokeStream"
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
	interceptors []connect.Interceptor
}

// WithRPCInterceptors appends interceptors run on top of the server's
// own Interceptors().
func WithRPCInterceptors(ic ...connect.Interceptor) RPCOption {
	return func(c *rpcConfig) { c.interceptors = append(c.interceptors, ic...) }
}

// rpcServer wires a Bridge into the two Connect handlers. It is
// internal — callers reach it only via MountRPC.
type rpcServer struct {
	b     *Bridge
	index map[string]*Leaf
}

// MountRPC registers a single ConnectRPC service on s that exposes
// every Bridge leaf where SurfaceRPC is enabled. Two procedures are
// installed under RPCServicePath:
//
//	Invoke(Invocation)       -> Result        // unary
//	InvokeStream(Invocation) -> stream Event  // server-streaming
//
// The Connect handler:
//   - forces inv.Meta.Surface = SurfaceRPC;
//   - rejects unknown / non-enabled / destructive-blocked leaves with
//     the codes in the package mapping table;
//   - returns Result with non-zero ExitCode as a success response
//     (clients inspect ExitCode themselves);
//   - cancels the running Stream goroutine when the client disconnects.
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

	srv := &rpcServer{b: b, index: indexLeaves(b)}

	// Compose handler options: server interceptors first, then any
	// caller-supplied options, then the JSON codec override that lets
	// Connect carry plain Go structs.
	hopts := []connect.HandlerOption{
		connect.WithCodec(jsonAnyCodec{name: "proto"}),
		connect.WithCodec(jsonAnyCodec{name: "json"}),
		connect.WithCodec(jsonAnyCodec{name: "json; charset=utf-8"}),
	}
	ics := append([]connect.Interceptor{}, s.Interceptors()...)
	ics = append(ics, cfg.interceptors...)
	if len(ics) > 0 {
		hopts = append(hopts, connect.WithInterceptors(ics...))
	}

	unary := connect.NewUnaryHandler(
		RPCInvokeProcedure,
		srv.invoke,
		hopts...,
	)
	stream := connect.NewServerStreamHandler(
		RPCInvokeStreamProcedure,
		srv.invokeStream,
		hopts...,
	)

	s.Handle(RPCServicePath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case RPCInvokeProcedure:
			unary.ServeHTTP(w, r)
		case RPCInvokeStreamProcedure:
			stream.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
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

// invoke implements the unary Invoke procedure.
func (s *rpcServer) invoke(
	ctx context.Context,
	req *connect.Request[Invocation],
) (*connect.Response[Result], error) {
	inv := *req.Msg
	leaf, cerr := s.preflight(req.Header(), &inv)
	if cerr != nil {
		return nil, cerr
	}
	res, err := s.b.Invoke(ctx, inv)
	if err != nil {
		return nil, mapBridgeError(err, leaf)
	}
	return connect.NewResponse(&res), nil
}

// invokeStream implements the server-streaming InvokeStream procedure.
// Events from the Runner are forwarded one-per-Send; the goroutine
// closes when the runner exits or ctx is canceled (client disconnect).
func (s *rpcServer) invokeStream(
	ctx context.Context,
	req *connect.Request[Invocation],
	stream *connect.ServerStream[Event],
) error {
	inv := *req.Msg
	leaf, cerr := s.preflight(req.Header(), &inv)
	if cerr != nil {
		return cerr
	}
	if !s.b.Policy().Allowed(leaf.Class, SurfaceRPC) {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("%w: %s on %s",
				ErrDestructiveBlocked, leaf.PathKey(), SurfaceRPC))
	}

	// Run the streamer in its own goroutine so we can multiplex Event
	// receipt with ctx cancellation observability.
	events := make(chan Event, 16)
	errc := make(chan error, 1)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		errc <- s.b.Runner().Stream(streamCtx, inv, events)
	}()

	for {
		select {
		case <-ctx.Done():
			cancel()
			// Drain remaining events so the runner goroutine can exit
			// cleanly without blocking on a full / unread channel.
			drain(events)
			<-errc
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
			if err := stream.Send(&ev); err != nil {
				cancel()
				drain(events)
				<-errc
				return err
			}
			if ev.Kind == "done" {
				// Runner has emitted its terminal event; let it close
				// the channel naturally to release resources.
				continue
			}
		}
	}
}

// preflight validates leaf existence, surface enablement, and the
// gating headers. It overwrites inv.Path with the resolved leaf path
// and forces inv.Meta.Surface = SurfaceRPC. Returns the resolved
// leaf or a Connect error.
func (s *rpcServer) preflight(
	header http.Header,
	inv *Invocation,
) (*Leaf, *connect.Error) {
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
		if header.Get(authHeader) == "" && inv.Meta.Caller == "" {
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
func mapBridgeError(err error, leaf *Leaf) error {
	_ = leaf
	switch {
	case errors.Is(err, ErrUnknownCommand):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrSurfaceNotEnabled):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, ErrDestructiveBlocked):
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

// RPCClientOptions returns the connect.ClientOptions clients must
// pass to NewClient when talking to a MountRPC service. Required
// because cmdsurface's wire types (Invocation / Result / Event) are
// plain Go structs, not proto.Messages — both ends must agree on the
// JSON-over-arbitrary-Go-values codec.
func RPCClientOptions() []connect.ClientOption {
	return []connect.ClientOption{
		connect.WithCodec(jsonAnyCodec{name: "proto"}),
		connect.WithCodec(jsonAnyCodec{name: "json"}),
		connect.WithCodec(jsonAnyCodec{name: "json; charset=utf-8"}),
	}
}

// jsonAnyCodec is a Codec that uses encoding/json to (un)marshal
// arbitrary Go values. It is registered under the "proto" and "json"
// codec names so Connect's default content-type negotiation picks it
// instead of the protobuf-only built-in codecs. cmdsurface's wire
// types (Invocation / Result / Event) are not proto.Messages — they
// are plain Go structs with JSON tags.
type jsonAnyCodec struct{ name string }

// Name implements connect.Codec.
func (c jsonAnyCodec) Name() string { return c.name }

// Marshal implements connect.Codec.
func (c jsonAnyCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

// Unmarshal implements connect.Codec.
func (c jsonAnyCodec) Unmarshal(data []byte, v any) error {
	if len(data) == 0 {
		// Mirror connect's protoJSONCodec contract; empty body is an
		// invalid JSON object so callers see CodeInvalidArgument.
		return errors.New("cmdsurface: zero-length request body")
	}
	return json.Unmarshal(data, v)
}

// IsBinary implements connect.stableCodec — returning false declares
// the wire format as text, which lets Connect set Content-Type
// correctly.
func (c jsonAnyCodec) IsBinary() bool { return false }

// MarshalStable implements connect.stableCodec by delegating to
// json.Marshal; for our purposes its output is already stable enough
// for protocol replay.
func (c jsonAnyCodec) MarshalStable(v any) ([]byte, error) { return json.Marshal(v) }
