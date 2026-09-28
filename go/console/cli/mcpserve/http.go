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

	kitlog "hop.top/kit/go/console/log"
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
// listener. Sessions are stateful: the SDK issues Mcp-Session-Id and
// server-to-client requests — a confirmation question among them —
// travel on the open response stream.
type httpServing struct {
	svc *service

	mu     sync.Mutex
	ln     net.Listener
	srv    *http.Server
	mcpSrv *mcp.Server
	stop   context.CancelFunc
}

func newHTTP(svc *service) *httpServing {
	return &httpServing{svc: svc}
}

// bind acquires the listener and reports the endpoint URL, which is
// what an MCP client is configured with.
func (h *httpServing) bind(context.Context) (string, error) {
	ln, err := net.Listen("tcp", h.svc.addr())
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}
	h.mu.Lock()
	h.ln = ln
	h.mu.Unlock()
	return "http://" + ln.Addr().String() + h.svc.path(), nil
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
	srv := &http.Server{
		Handler:           h.middleware()(mux),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return base },
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

	err := srv.Serve(ln)
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

func ignoreClosed(err error) error {
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// middleware is the stack in front of the SDK handler: request ids,
// request logging, panic recovery, the adopter's Auth when set, and
// the call header last, so it records what the layers before it
// established.
func (h *httpServing) middleware() api.Middleware {
	logger := kitlog.New(h.svc.root.Viper)
	mws := []api.Middleware{
		api.RequestID(),
		api.Logger(logger.Info),
		api.Recovery(func(v any, r *http.Request) {
			logger.Error("panic recovered", "error", v, "path", r.URL.Path)
		}),
	}
	authed := h.svc.cfg.Auth != nil
	if authed {
		mws = append(mws, api.Auth(h.svc.cfg.Auth, api.OnAuthRefused(h.auditAuthRefusal)))
	}
	mws = append(mws, mcpCallRecorder(authed))
	return api.Chain(mws...)
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
