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
// on each request. If the incoming request already has the header, it
// is preserved.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(headerRequestID)
			if id == "" {
				id = newID()
			}
			w.Header().Set(headerRequestID, id)
			ctx := context.WithValue(r.Context(), requestIDKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetRequestID extracts the request ID from the request context.
func GetRequestID(r *http.Request) string {
	v, _ := r.Context().Value(requestIDKey{}).(string)
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
