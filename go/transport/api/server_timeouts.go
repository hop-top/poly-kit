package api

import (
	"net/http"
	"time"
)

// Default server timeouts of a kit HTTP listener, sized for
// request/reply. Stream routes lift the write deadline for their own
// response (see [LiftWriteDeadline]).
const (
	DefaultReadHeaderTimeout = 5 * time.Second
	DefaultReadTimeout       = 5 * time.Second
	DefaultWriteTimeout      = 10 * time.Second
)

// ServerTimeouts are the server-side timeouts of one HTTP listener:
// the timeouts block of a served service. A zero field means no
// timeout, except where net/http falls back — a zero ReadHeader
// uses Read, and a zero Idle uses Read.
type ServerTimeouts struct {
	// ReadHeader bounds reading a request's headers.
	ReadHeader time.Duration
	// Read bounds reading a whole request, body included.
	Read time.Duration
	// Write bounds writing a response, from the end of the request's
	// headers. Stream routes are exempt.
	Write time.Duration
	// Idle bounds how long a keep-alive connection waits for its next
	// request.
	Idle time.Duration
}

// DefaultServerTimeouts returns the kit defaults: read header 5s,
// read 5s, write 10s, and idle falling back to read.
func DefaultServerTimeouts() ServerTimeouts {
	return ServerTimeouts{
		ReadHeader: DefaultReadHeaderTimeout,
		Read:       DefaultReadTimeout,
		Write:      DefaultWriteTimeout,
	}
}

// ServerTimeoutsOf returns the timeouts srv is configured with.
func ServerTimeoutsOf(srv *http.Server) ServerTimeouts {
	if srv == nil {
		return ServerTimeouts{}
	}
	return ServerTimeouts{
		ReadHeader: srv.ReadHeaderTimeout,
		Read:       srv.ReadTimeout,
		Write:      srv.WriteTimeout,
		Idle:       srv.IdleTimeout,
	}
}

// Apply sets t on srv.
func (t ServerTimeouts) Apply(srv *http.Server) {
	if srv == nil {
		return
	}
	srv.ReadHeaderTimeout = t.ReadHeader
	srv.ReadTimeout = t.Read
	srv.WriteTimeout = t.Write
	srv.IdleTimeout = t.Idle
}

// LiftWriteDeadline exempts every response next writes from the
// server's write deadline. It is for a route whose responses are
// streams by design — a stream outlives a deadline sized for
// request/reply, and one cut at the deadline would end without its
// terminal frame. The read deadline needs nothing: net/http clears it
// once the request body is consumed.
func LiftWriteDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		next.ServeHTTP(w, r)
	})
}
