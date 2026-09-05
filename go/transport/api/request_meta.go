package api

import (
	"net/http"
	"strings"
	"time"
)

// Request headers the projection reads for provenance. They are the
// standard spellings, so a caller that already propagates them for
// its own tracing needs nothing kit-specific.
const (
	// HeaderTraceparent is the W3C Trace Context header. The
	// trace-id field is what reaches Meta.TraceID.
	HeaderTraceparent = "Traceparent"
	// HeaderTraceID is the fallback trace header for callers that do
	// not speak W3C Trace Context.
	HeaderTraceID = "X-Trace-ID"
	// HeaderIdempotencyKey is the IETF Idempotency-Key header.
	HeaderIdempotencyKey = "Idempotency-Key"
)

// RequestMeta is the provenance the projection extracts from an HTTP
// request before handing a command to its executor. It is the
// transport-side view of the bridge's Meta: everything here is what
// the HTTP layer can vouch for, gathered in one place so the
// executor does not read headers or the request context itself.
type RequestMeta struct {
	// Principal is the authenticated caller, from the claims the
	// [Auth] middleware stored (see [IdentityOf]). Empty when no
	// auth ran or the claims carry no identity.
	Principal string
	// Tenant is the authenticated tenant, from the same claims.
	Tenant string
	// Scopes are the credential's entitlements, from the same
	// claims (see [ScopesOf]).
	Scopes []string
	// RequestID is the X-Request-ID the [RequestID] middleware
	// issued or echoed.
	RequestID string
	// TraceID is the trace-id field of a traceparent header, else
	// the X-Trace-ID header, else empty.
	TraceID string
	// IdempotencyKey is the Idempotency-Key header, else empty.
	IdempotencyKey string
	// RemoteAddr is the peer address as the server saw it.
	RemoteAddr string
	// ReceivedAt is when the projection began handling the request.
	ReceivedAt time.Time
}

// RequestMetaFrom gathers provenance from r. It reads the claims and
// request id the middleware stored in the context, and the standard
// trace and idempotency headers.
func RequestMetaFrom(r *http.Request) RequestMeta {
	claims := ClaimsFromContext(r.Context())
	principal, tenant := IdentityOf(claims)
	return RequestMeta{
		Principal:      principal,
		Tenant:         tenant,
		Scopes:         ScopesOf(claims),
		RequestID:      RequestIDFromContext(r.Context()),
		TraceID:        TraceIDFromRequest(r),
		IdempotencyKey: r.Header.Get(HeaderIdempotencyKey),
		RemoteAddr:     r.RemoteAddr,
		ReceivedAt:     time.Now(),
	}
}

// TraceIDFromRequest returns the trace identifier a caller
// propagated: the trace-id field of a well-formed traceparent header
// (version-traceid-parentid-flags), else the X-Trace-ID header, else
// "". A traceparent whose trace-id is all zeros is invalid per the
// specification and is ignored.
func TraceIDFromRequest(r *http.Request) string {
	if tp := r.Header.Get(HeaderTraceparent); tp != "" {
		if id := traceIDFromTraceparent(tp); id != "" {
			return id
		}
	}
	return r.Header.Get(HeaderTraceID)
}

// traceIDFromTraceparent parses the trace-id out of a traceparent
// value. The header is four dash-separated hex fields; the second is
// the 32-character trace-id.
func traceIDFromTraceparent(v string) string {
	parts := strings.Split(strings.TrimSpace(v), "-")
	if len(parts) < 4 || len(parts[1]) != 32 {
		return ""
	}
	id := strings.ToLower(parts[1])
	if id == strings.Repeat("0", 32) {
		return ""
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return id
}
