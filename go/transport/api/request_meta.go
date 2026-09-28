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
	// trace-id field is what reaches Meta.TraceID; a well-formed
	// value reaches Meta.Traceparent whole, so the invocation
	// continues the caller's trace.
	HeaderTraceparent = "Traceparent"
	// HeaderTracestate is the W3C Trace Context vendor header that
	// travels with a traceparent. It is kept only beside a
	// well-formed traceparent.
	HeaderTracestate = "Tracestate"
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
	// Authenticated reports that the [Auth] middleware verified the
	// request (see [Authenticated]). Principal, Tenant and Scopes are
	// read only from a verified request.
	Authenticated bool
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
	// Traceparent is the caller's W3C traceparent header when it is
	// well-formed, else empty. It is the full span context: the
	// parent a tracer continues from and what a child process
	// receives as TRACEPARENT.
	Traceparent string
	// Tracestate is the caller's W3C tracestate header, kept only
	// beside a well-formed Traceparent.
	Tracestate string
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
	traceparent, tracestate := TraceContextFromHeader(r.Header)
	return RequestMeta{
		Authenticated:  Authenticated(r.Context()),
		Principal:      principal,
		Tenant:         tenant,
		Scopes:         ScopesOf(claims),
		RequestID:      RequestIDFromContext(r.Context()),
		TraceID:        TraceIDFromRequest(r),
		Traceparent:    traceparent,
		Tracestate:     tracestate,
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
	return TraceIDFromHeader(r.Header)
}

// TraceIDFromHeader is [TraceIDFromRequest] over a bare header set,
// for transports that hold headers without an *http.Request, such as
// a Connect request.
func TraceIDFromHeader(h http.Header) string {
	if tp := h.Get(HeaderTraceparent); tp != "" {
		if id := traceIDFromTraceparent(tp); id != "" {
			return id
		}
	}
	return h.Get(HeaderTraceID)
}

// TraceContextFromHeader returns the W3C trace context a caller
// propagated: the traceparent header when it is well-formed, and the
// tracestate header beside it. A malformed traceparent yields two
// empty strings, and so does a tracestate with no traceparent: the
// specification ties tracestate to the parent it travels with.
//
// Well-formed is the specification's rule, stricter than the one
// [TraceIDFromHeader] applies to extract an id for correlation:
// lowercase hex only, a non-zero trace-id and parent-id, and a
// version other than ff. What passes is exactly what a tracer or a
// child process would accept as a parent, and it is returned as
// received rather than re-encoded.
func TraceContextFromHeader(h http.Header) (traceparent, tracestate string) {
	tp := strings.TrimSpace(h.Get(HeaderTraceparent))
	if !validTraceparent(tp) {
		return "", ""
	}
	return tp, strings.TrimSpace(h.Get(HeaderTracestate))
}

// validTraceparent applies the W3C rule: four dash-separated
// lowercase hex fields of 2, 32, 16 and 2 characters. Version ff is
// forbidden, an all-zero trace-id or parent-id is invalid, and only
// version 00 is held to exactly four fields, because a later version
// may append more.
func validTraceparent(v string) bool {
	parts := strings.Split(v, "-")
	if len(parts) < 4 {
		return false
	}
	version, id, parent, flags := parts[0], parts[1], parts[2], parts[3]
	if len(version) != 2 || len(id) != 32 || len(parent) != 16 || len(flags) != 2 {
		return false
	}
	if version == "ff" || (version == "00" && len(parts) != 4) {
		return false
	}
	for _, f := range [...]string{version, id, parent, flags} {
		if !isLowerHex(f) {
			return false
		}
	}
	return id != strings.Repeat("0", 32) && parent != strings.Repeat("0", 16)
}

// isLowerHex reports whether s is lowercase hexadecimal, the only
// encoding the specification allows in a traceparent.
func isLowerHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
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
