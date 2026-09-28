package cmdsurface

import (
	"errors"
	"fmt"
	"net/http"

	"hop.top/kit/go/transport/api"
)

// ErrBodyTooLarge is the error a transport reports through
// [Bridge.Audit] when it refuses a request because its body (or, on
// WebSocket, one message) exceeds the entry point's size cap. Like
// [ErrAuthRefused], the bridge never returns it: the cap is enforced
// before anything is decoded, and routing the refusal through the
// same sinks keeps oversized calls in the one audit stream.
var ErrBodyTooLarge = errors.New("cmdsurface: request body too large")

// AuditBodyTooLarge records a size-cap refusal for a request the
// bridge never saw, with the provenance the HTTP request carries.
// path is the command the request addressed when the
// transport knows it (REST routes are per-leaf), nil otherwise.
func (b *Bridge) AuditBodyTooLarge(r *http.Request, surface Surface, path []string, limit int64) {
	if b == nil {
		return
	}
	meta := api.RequestMetaFrom(r)
	inv := Invocation{
		Path: append([]string(nil), path...),
		Meta: Meta{
			Surface:     surface,
			RequestID:   meta.RequestID,
			TraceID:     meta.TraceID,
			RequestedAt: meta.ReceivedAt,
			Extra:       httpRefusalExtra(r, meta),
		},
	}
	b.Audit(r.Context(), inv, Result{}, fmt.Errorf("%w: exceeds %d bytes", ErrBodyTooLarge, limit))
}

// httpRefusalExtra is the provenance an HTTP-plane refusal is audited
// with: the request line and the client address, and the proxy it
// arrived through when a trusted proxy forwarded it.
func httpRefusalExtra(r *http.Request, meta api.RequestMeta) map[string]string {
	extra := map[string]string{
		"http_method": r.Method,
		"http_path":   r.URL.Path,
		"remote_addr": meta.RemoteAddr,
	}
	if meta.PeerAddr != "" {
		extra["peer_addr"] = meta.PeerAddr
	}
	return extra
}

// ProjectionBodyTooLarge returns the hook [api.OnBodyTooLarge] takes
// for a router serving b's projection. A refused body reaches b's
// sinks as [ErrBodyTooLarge] on SurfaceREST, against the command the
// URL addresses — a streaming route's command included — or no
// command for any other URL.
//
// [WithProjectionMaxBodyBytes] installs it. A router that caps bodies
// for itself passes it to its own [api.BodyLimit].
func ProjectionBodyTooLarge(b *Bridge) func(r *http.Request, limit int64) {
	if b == nil {
		return func(*http.Request, int64) {}
	}
	known := projectedPaths(b)
	return func(r *http.Request, limit int64) {
		b.AuditBodyTooLarge(r, SurfaceREST, projectedPathOf(r.URL.Path, known), limit)
	}
}

// bodyLimitMiddleware is api.BodyLimit wired to audit its refusals
// into b for surface. It is how the HTTP surfaces that render the
// api package's error shape (REST) cap their bodies.
func bodyLimitMiddleware(b *Bridge, surface Surface, path []string, limit int64) api.Middleware {
	return api.BodyLimit(limit, api.OnBodyTooLarge(func(r *http.Request, l int64) {
		b.AuditBodyTooLarge(r, surface, path, l)
	}))
}
