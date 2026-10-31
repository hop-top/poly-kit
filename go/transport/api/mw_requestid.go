package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type requestIDKey struct{}

const headerRequestID = "X-Request-ID"

// RequestID returns a middleware that sets a unique X-Request-ID header
// on each request. An incoming X-Request-ID is kept only when it is 1 to
// 128 characters of [A-Za-z0-9._-]; anything else (control characters,
// quotes, spaces, non-ASCII, over-long) is dropped and a fresh id is
// minted, so a caller cannot forge log lines or response headers
// through it.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(headerRequestID)
			if !validRequestID(id) {
				id = newID()
			}
			w.Header().Set(headerRequestID, id)
			ctx := context.WithValue(r.Context(), requestIDKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// maxRequestIDLen bounds an accepted caller-supplied id.
const maxRequestIDLen = 128

// validRequestID reports whether id is 1 to 128 of [A-Za-z0-9._-].
func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > maxRequestIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// GetRequestID extracts the request ID from the request context.
func GetRequestID(r *http.Request) string {
	return RequestIDFromContext(r.Context())
}

// RequestIDFromContext extracts the request ID the [RequestID]
// middleware stored, or "" when the middleware did not run.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

var idCounter atomic.Uint64

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback: timestamp + monotonic counter.
		return fmt.Sprintf("%x-%x", time.Now().UnixNano(), idCounter.Add(1))
	}
	return hex.EncodeToString(b)
}
