package api

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ReleaseStalledOnShutdown keeps srv's Shutdown from waiting on a
// client that stalls. Without it, Shutdown waits out the read timeout
// (or, with none, the caller's whole budget) for:
//
//   - a connection whose first request's headers have not all
//     arrived — never used, or stalled mid-header. net/http counts it
//     as busy for five seconds before treating it as idle;
//   - a request whose headers arrived and whose body is stalled
//     mid-read. Its handler is blocked in the read, and canceling the
//     request's context does not interrupt a blocked read.
//
// When Shutdown begins, the first are closed and the second have their
// body read ended with a read deadline of now, so the handler sees a
// read error and returns. Nothing else changes: a request whose body
// has been read runs to completion, its context untouched, and a
// connection idle between keep-alive requests is closed by Shutdown
// itself.
//
// The same holds for an HTTP/1 handler that answers without reading
// its body to the end — a refusal answered before the body is read
// (Host, Origin, body limit, authentication): net/http would read the
// rest of the body before answering, to reuse the connection, and a
// client stalled mid-body would hold it for the read timeout. Such a
// response carries Connection: close instead, the rest of the body is
// not waited for, and the connection ends after the response. HTTP/2
// resets the stream and needs nothing.
//
// Call it once srv carries its Handler and ConnState, before it
// serves. It wraps the Handler outermost, so the body read it can end
// is the server's own, and chains ConnState.
func ReleaseStalledOnShutdown(srv *http.Server) {
	if srv == nil {
		return
	}
	g := &stallGuard{
		unread: make(map[net.Conn]struct{}),
		bodies: make(map[*watchedBody]struct{}),
	}
	next := srv.Handler
	if next == nil {
		next = http.DefaultServeMux
	}
	srv.Handler = g.wrap(next)
	prev := srv.ConnState
	srv.ConnState = func(c net.Conn, st http.ConnState) {
		g.connState(c, st)
		if prev != nil {
			prev(c, st)
		}
	}
	srv.RegisterOnShutdown(g.release)
}

// stallGuard is the state behind [ReleaseStalledOnShutdown].
type stallGuard struct {
	mu      sync.Mutex
	stopped bool
	// unread holds connections whose first request's headers have not
	// all arrived.
	unread map[net.Conn]struct{}
	// bodies holds the bodies of requests being served.
	bodies map[*watchedBody]struct{}
}

// connState records which connections carry no complete request yet.
// net/http leaves a connection in StateNew until its first request's
// headers are read; any later state removes it. A stall in a later
// request's headers needs no tracking: the connection is StateIdle
// while it waits, and Shutdown closes idle connections.
func (g *stallGuard) connState(c net.Conn, st http.ConnState) {
	g.mu.Lock()
	if st != http.StateNew {
		delete(g.unread, c)
		g.mu.Unlock()
		return
	}
	stopped := g.stopped
	if !stopped {
		g.unread[c] = struct{}{}
	}
	g.mu.Unlock()
	if stopped {
		// Accepted as Shutdown began: it would not be served.
		_ = c.Close()
	}
}

// wrap tracks each request's body while its handler runs.
func (g *stallGuard) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		b := &watchedBody{ReadCloser: r.Body, rc: http.NewResponseController(w)}
		g.mu.Lock()
		stopped := g.stopped
		if !stopped {
			g.bodies[b] = struct{}{}
		}
		g.mu.Unlock()
		if stopped {
			b.release()
		}
		defer func() {
			g.mu.Lock()
			delete(g.bodies, b)
			g.mu.Unlock()
		}()
		// A shallow copy carries the body: the server keeps its own
		// request, and reads its own body after the handler returns.
		r = r.WithContext(r.Context())
		r.Body = b
		if r.ProtoMajor != 1 {
			next.ServeHTTP(w, r)
			return
		}
		cw := &closeOnUnreadBody{ResponseWriter: w, body: b}
		next.ServeHTTP(cw, r)
		cw.handlerReturned()
	})
}

// release runs when Shutdown begins, after the listeners are closed.
func (g *stallGuard) release() {
	g.mu.Lock()
	g.stopped = true
	conns := make([]net.Conn, 0, len(g.unread))
	for c := range g.unread {
		conns = append(conns, c)
	}
	clear(g.unread)
	bodies := make([]*watchedBody, 0, len(g.bodies))
	for b := range g.bodies {
		bodies = append(bodies, b)
	}
	g.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	for _, b := range bodies {
		b.release()
	}
}

// closeOnUnreadBody marks a response Connection: close when its
// headers go out before the request body was read to its end. net/http
// would otherwise read the rest of the body before sending them, and
// again when it closes the body, for as long as the client takes.
type closeOnUnreadBody struct {
	http.ResponseWriter
	body *watchedBody
	// wrote is set once the headers are fixed; closing once they went
	// out with Connection: close.
	wrote, closing bool
}

// headersGoOut runs before the response's headers are fixed.
func (c *closeOnUnreadBody) headersGoOut() {
	if c.wrote {
		return
	}
	c.wrote = true
	if !c.body.done.Load() {
		c.Header().Set("Connection", "close")
		c.closing = true
	}
}

// handlerReturned covers a handler that wrote nothing, then ends a
// body still unread on a closing connection: net/http reads it to the
// end when it closes the body, and a read deadline of now makes that
// read return at once. The connection ends after this response, so
// nothing reads it again.
func (c *closeOnUnreadBody) handlerReturned() {
	c.headersGoOut()
	if c.closing && !c.body.done.Load() {
		_ = c.body.rc.SetReadDeadline(time.Now())
	}
}

func (c *closeOnUnreadBody) WriteHeader(code int) {
	if code >= http.StatusOK || code == http.StatusSwitchingProtocols {
		c.headersGoOut()
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *closeOnUnreadBody) Write(p []byte) (int, error) {
	c.headersGoOut()
	return c.ResponseWriter.Write(p)
}

// Flush implements http.Flusher for handlers that assert it.
func (c *closeOnUnreadBody) Flush() {
	c.headersGoOut()
	_ = http.NewResponseController(c.ResponseWriter).Flush()
}

// Hijack implements http.Hijacker for handlers that assert it.
func (c *closeOnUnreadBody) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(c.ResponseWriter).Hijack()
}

// Unwrap lets http.ResponseController reach the server's writer.
func (c *closeOnUnreadBody) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// watchedBody is a request body that knows whether it has been read
// to its end.
type watchedBody struct {
	io.ReadCloser
	rc   *http.ResponseController
	done atomic.Bool
}

func (b *watchedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.done.Store(true)
	}
	return n, err
}

// release ends a body read that has not reached its end. A body read
// to its end is left alone: its connection is watching for the client
// to go away, and a deadline now would cancel the request's context.
func (b *watchedBody) release() {
	if !b.done.Load() {
		_ = b.rc.SetReadDeadline(time.Now())
	}
}
