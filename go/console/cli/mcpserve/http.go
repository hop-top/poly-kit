package mcpserve

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/mcpsdk"
)

// mcpCallHeader carries what the HTTP layer established about a
// request — the identity Auth verified, the request id, the trace and
// idempotency headers, the peer — to the tool handler.
//
// The SDK hands a tool handler the request's headers but not its
// context, so this is the one channel from middleware to handler. It
// is server-owned: the middleware deletes whatever a client sent
// under this name and writes its own value on every request, before
// the SDK reads anything, so a client cannot claim an identity by
// setting it.
const mcpCallHeader = "X-Kit-Mcp-Call"

// mcpCall is the value of [mcpCallHeader].
type mcpCall struct {
	Verified       bool     `json:"verified,omitempty"`
	Principal      string   `json:"principal,omitempty"`
	Tenant         string   `json:"tenant,omitempty"`
	Scopes         []string `json:"scopes,omitempty"`
	RequestID      string   `json:"request_id,omitempty"`
	TraceID        string   `json:"trace_id,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
	RemoteAddr     string   `json:"remote_addr,omitempty"`
}

// httpServing serves the surface over streamable HTTP on its own
// listener, every protocol revision on one endpoint (mcpsdk's
// Handler). A client that runs initialize gets a stateful session:
// the SDK issues Mcp-Session-Id and server-to-client requests — a
// confirmation question among them — travel on the open response
// stream. A 2026-07-28 request is served statelessly; its
// confirmation question comes back as an input_required result.
type httpServing struct {
	svc *service

	mu     sync.Mutex
	ln     net.Listener
	srv    *http.Server
	mcpSrv *mcp.Server
	stop   context.CancelFunc

	// unread holds connections whose first request's headers have not
	// all arrived: never used, or stalled mid-header.
	unread map[net.Conn]struct{}
}

func newHTTP(svc *service) *httpServing {
	return &httpServing{svc: svc}
}

// bind acquires the listener and reports the endpoint URL, which is
// what an MCP client is configured with.
func (h *httpServing) bind(context.Context) (string, error) {
	if err := h.svc.resolveTLS(); err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", h.svc.addr())
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}
	h.mu.Lock()
	h.ln = ln
	h.mu.Unlock()
	return h.svc.tls.Scheme() + "://" + ln.Addr().String() + h.svc.path(), nil
}

// serve runs the HTTP server until ctx is canceled or close runs.
func (h *httpServing) serve(ctx context.Context, s *mcpsdk.Surface) error {
	h.mu.Lock()
	ln := h.ln
	h.mu.Unlock()
	if ln == nil {
		return errors.New("mcp: serve called before bind")
	}

	// Request contexts derive from base, so stopping the service ends
	// every open response stream — a server-to-client stream would
	// otherwise hold Shutdown until its budget ran out.
	base, cancelBase := context.WithCancel(context.WithoutCancel(ctx))
	mux := http.NewServeMux()
	mux.Handle(h.svc.path(), s.Handler())
	handler, err := h.handler(mux)
	if err != nil {
		cancelBase()
		_ = ln.Close()
		return err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return base },
		ConnState:         h.trackUnread,
	}

	h.mu.Lock()
	h.srv = srv
	h.mcpSrv = s.Server()
	h.stop = cancelBase
	h.mu.Unlock()

	// A supervisor that cancels without calling Stop must not leave
	// the listener open.
	after := context.AfterFunc(ctx, func() { _ = h.close(context.Background()) })
	defer after()

	err = h.svc.tls.Serve(srv, ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// close ends every MCP session, which cancels calls in flight, then
// drains the server within ctx; a server that cannot drain in time is
// closed outright.
func (h *httpServing) close(ctx context.Context) error {
	h.mu.Lock()
	srv, mcpSrv, stop, ln := h.srv, h.mcpSrv, h.stop, h.ln
	h.mu.Unlock()

	if stop != nil {
		stop()
	}
	if mcpSrv != nil {
		for ss := range mcpSrv.Sessions() {
			_ = ss.Close()
		}
	}
	h.closeUnread()
	if srv == nil {
		if ln != nil {
			return ignoreClosed(ln.Close())
		}
		return nil
	}
	if err := srv.Shutdown(ctx); err != nil {
		_ = srv.Close()
		return err
	}
	return nil
}

// trackUnread records which connections have not sent a complete
// request header yet. net/http leaves a connection in StateNew until
// its first request's headers are read, so this covers one a client
// leaves unused (Go's transport parks a connection it dialed for a
// request another connection served) and one stalled mid-header.
// Shutdown waits five seconds before it counts either as idle. A stall
// in a later request's headers needs no tracking: the connection is
// StateIdle while it waits, and Shutdown closes idle connections.
func (h *httpServing) trackUnread(c net.Conn, st http.ConnState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if st == http.StateNew {
		if h.unread == nil {
			h.unread = make(map[net.Conn]struct{})
		}
		h.unread[c] = struct{}{}
		return
	}
	delete(h.unread, c)
}

// closeUnread closes the connections that carry no complete request,
// so Shutdown does not wait on them. Stopping has already canceled every
// request in flight (and releaseOnStop has ended any body read); one
// arriving now would not be served.
func (h *httpServing) closeUnread() {
	h.mu.Lock()
	conns := make([]net.Conn, 0, len(h.unread))
	for c := range h.unread {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

func ignoreClosed(err error) error {
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// handler is the stack in front of mux, the SDK handler's: the stop
// release first, then the HTTP-plane chain every kit listener shares
// (serve-lifecycle.md §"Middleware order on the HTTP plane": request
// id, access log, recovery, tracing and metrics, security headers,
// health probes, Host and Origin checks with the metrics endpoint,
// body limit, compression), read from services.mcp.*; then the
// verifier when set (slot 12: the client certificate under
// auth.mode: mtls, else Config.Auth), and the call header last, so it
// records what the layers before it established.
//
// The SDK's own copies of two checks sit beneath: its body cap and
// its DNS-rebinding check, set from the same blocks by
// surfaceOptions.
func (h *httpServing) handler(mux *http.ServeMux) (http.Handler, error) {
	var inner []api.Middleware
	auth := h.svc.auth()
	authed := auth != nil
	if authed {
		inner = append(inner, api.Auth(auth, api.OnAuthRefused(h.auditAuthRefusal)))
	}
	inner = append(inner, mcpCallRecorder(authed))

	l := h.svc.httpListener()
	l.Routes = mux
	chain, err := cli.ServeHTTPHandler(h.svc.root, l, api.Chain(inner...)(mux))
	if err != nil {
		return nil, err
	}
	return releaseOnStop()(chain), nil
}

// surfaceOptions sets the SDK's copies of the HTTP-plane checks from
// services.mcp.*, so the SDK never refuses what the service's
// configuration allows: its body cap is the body_limit block's, and
// its DNS-rebinding check — which admits loopback names only — steps
// aside when host_check admits other names (an allow list) or is
// switched off. Otherwise it stays in force beneath kit's Host check.
func (h *httpServing) surfaceOptions() ([]mcpsdk.Option, error) {
	set, err := cli.ResolveServeHTTPListener(h.svc.root, h.svc.httpListener())
	if err != nil {
		return nil, err
	}
	opts := []mcpsdk.Option{mcpsdk.WithMaxBodyBytes(set.MaxBodyBytes)}
	if !set.HostCheck || len(set.AllowHosts) > 0 {
		opts = append(opts, mcpsdk.WithoutLocalhostProtection())
	}
	return opts, nil
}

// releaseOnStop ends the read of a request body when the request's
// context ends, which stopping the service does. A client that sends
// headers and then stalls mid-body would otherwise hold the handler in
// that read, and Shutdown with it, for the whole stop budget: canceling
// the context does not interrupt a blocked read, a read deadline does.
//
// It must wrap the server's own ResponseWriter, so it sits first. The
// release is withdrawn when the handler returns, before the server
// reads the connection's next request.
func releaseOnStop() api.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := http.NewResponseController(w)
			release := context.AfterFunc(r.Context(), func() {
				_ = rc.SetReadDeadline(time.Now())
			})
			defer release()
			next.ServeHTTP(w, r)
		})
	}
}

// mcpCallRecorder writes [mcpCallHeader] from what the layers before
// it established, replacing anything the client sent under that name.
func mcpCallRecorder(authed bool) api.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m := api.RequestMetaFrom(r)
			call := mcpCall{
				// Reaching here past the Auth middleware is the
				// verification; without Auth nothing was verified.
				Verified:       authed,
				RequestID:      m.RequestID,
				TraceID:        m.TraceID,
				IdempotencyKey: m.IdempotencyKey,
				RemoteAddr:     m.RemoteAddr,
			}
			if authed {
				call.Principal, call.Tenant, call.Scopes = m.Principal, m.Tenant, m.Scopes
			}
			raw, _ := json.Marshal(call)
			r.Header.Del(mcpCallHeader)
			r.Header.Set(mcpCallHeader, string(raw))
			next.ServeHTTP(w, r)
		})
	}
}

// auditAuthRefusal reports a request the Auth middleware refused into
// the bridge's sinks, so "not authenticated" lands in the same stream
// as the bridge's own verdicts. The tool is not known at this layer.
func (h *httpServing) auditAuthRefusal(r *http.Request, err error) {
	b := h.svc.Bridge()
	if b == nil {
		return
	}
	m := api.RequestMetaFrom(r)
	b.Audit(r.Context(), cmdsurface.Invocation{
		Meta: cmdsurface.Meta{
			Surface:     cmdsurface.SurfaceMCP,
			RequestID:   m.RequestID,
			TraceID:     m.TraceID,
			RequestedAt: m.ReceivedAt,
			Extra: map[string]string{
				"mcp_transport": TransportHTTP,
				"http_method":   r.Method,
				"http_path":     r.URL.Path,
				"remote_addr":   m.RemoteAddr,
			},
		},
	}, cmdsurface.Result{}, fmt.Errorf("%w: %v", cmdsurface.ErrAuthRefused, err))
}

// callOf decodes the call header the middleware wrote.
func callOf(req *mcp.CallToolRequest) mcpCall {
	var call mcpCall
	if req == nil || req.Extra == nil || req.Extra.Header == nil {
		return call
	}
	_ = json.Unmarshal([]byte(req.Extra.Header.Get(mcpCallHeader)), &call)
	return call
}

// callMeta is the provenance of one tool call over HTTP.
func (h *httpServing) callMeta(_ context.Context, req *mcp.CallToolRequest) cmdsurface.Meta {
	call := callOf(req)
	meta := cmdsurface.Meta{
		RequestID:      call.RequestID,
		TraceID:        call.TraceID,
		IdempotencyKey: call.IdempotencyKey,
		RequestedAt:    time.Now(),
	}
	if call.Verified {
		meta.Caller, meta.Tenant = call.Principal, call.Tenant
		meta.Established = cmdsurface.EstablishedVerified
	}
	if meta.RequestID == "" {
		meta.RequestID = newRequestID()
	}
	extra := map[string]string{"mcp_transport": TransportHTTP}
	if call.RemoteAddr != "" {
		extra["remote_addr"] = call.RemoteAddr
	}
	if call.Verified && len(call.Scopes) > 0 {
		extra["scopes"] = strings.Join(call.Scopes, ",")
	}
	if name := clientName(req); name != "" {
		extra["mcp_client"] = name
	}
	meta.Extra = extra
	return meta
}

// authenticated admits a kit/auth-required leaf only when Auth
// verified the request. A bare Authorization header is presence, not
// authentication, and a loopback listener is reachable by every local
// user, so neither admits one.
func (h *httpServing) authenticated(_ context.Context, req *mcp.CallToolRequest) bool {
	return callOf(req).Verified
}

// newMCPRequestID issues an id for a call that arrived without one, so
// every audit record has a handle.
func newRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
