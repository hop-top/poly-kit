package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"hop.top/kit/go/transport/api"
)

// defaultSSEHeartbeat is the interval at which idle streams emit a
// comment frame to keep proxies (nginx, ELB) from idle-timing the
// connection. Overridable via the unexported withSSEHeartbeat option
// used by tests.
const defaultSSEHeartbeat = 15 * time.Second

// MountSSE registers one streaming HTTP endpoint per leaf where
// SurfaceSSE is enabled on b. Each endpoint is a GET that reads the
// Invocation from the URL query, opens a text/event-stream response,
// and forwards Runner.Stream Events as SSE frames terminated by a
// single "result" (or "error") frame.
//
//	GET {prefix}/{cmd-path}/stream?arg=<v>&flag.<name>=<v>
//
// cmd-path is the leaf's cobra path joined with "/". Example: leaf
// ["widget","add"] at prefix="/cmd" → GET /cmd/widget/add/stream.
//
// Query parameter shape:
//
//   - arg=<v>            (repeatable) → inv.Args, in URL order
//   - flag.<name>=<v>    (repeatable) → inv.Flags[name]; a single
//     value is stored as string, repeated values as []string. The
//     cobra leaf parses concrete types from there.
//
// MountSSE forces inv.Meta.Surface = SurfaceSSE; the resolved leaf's
// path is authoritative. Meta.TraceID is taken from X-Request-ID
// when present.
//
// Pre-stream sentinel error mapping (status sent before any frame):
//
//	ErrUnknownCommand       → 404 code=unknown_command
//	ErrSurfaceNotEnabled    → 404 code=not_enabled
//	ErrDestructiveBlocked   → 403 code=destructive_blocked
//	auth required, missing  → 401 code=unauthorized
//	confirmation required   → 428 code=confirmation_required
//	http.Flusher cast fail  → 500 code=server_error
//	any other error         → api.MapError passthrough
//
// Once the stream has BEGUN (status 200 + headers flushed), every
// further error is reported as an `event: error` frame.
//
// Per-leaf middleware:
//
//   - Class.AuthRequired wraps the route with api.Auth using the
//     AuthFunc supplied via WithSSEAuth (default: deny-all → 401).
//   - Class.RequiresConfirmation gates the route on the presence of
//     an X-Confirm-Token header (value is not inspected).
func MountSSE(b *Bridge, r *api.Router, opts ...SSEOption) error {
	if b == nil {
		return errors.New("cmdsurface: MountSSE: nil Bridge")
	}
	if r == nil {
		return errors.New("cmdsurface: MountSSE: nil api.Router")
	}
	cfg := defaultSSEConfig()
	for _, o := range opts {
		o(&cfg)
	}

	for _, leaf := range b.Leaves() {
		if !leaf.Enabled[SurfaceSSE] {
			continue
		}
		path := cfg.prefix + "/" + strings.Join(leaf.Path, "/") + "/stream"
		handler := newSSEHandler(b, leaf, cfg)
		wrapped := wrapSSEMiddleware(handler, leaf, cfg)
		r.Handle(http.MethodGet, path, wrapped)
	}
	return nil
}

// SSEOption configures MountSSE.
type SSEOption func(*sseConfig)

type sseConfig struct {
	prefix     string
	authFn     api.AuthFunc
	middleware []func(http.Handler) http.Handler
	heartbeat  time.Duration
}

func defaultSSEConfig() sseConfig {
	return sseConfig{prefix: "/cmd", heartbeat: defaultSSEHeartbeat}
}

// WithSSEPrefix sets the URL prefix for every mounted route. Default
// is "/cmd". A trailing "/" on prefix is stripped to avoid producing
// "/cmd//widget/add/stream".
func WithSSEPrefix(prefix string) SSEOption {
	return func(c *sseConfig) {
		for len(prefix) > 1 && strings.HasSuffix(prefix, "/") {
			prefix = prefix[:len(prefix)-1]
		}
		c.prefix = prefix
	}
}

// WithSSEAuth installs the AuthFunc used by api.Auth to wrap routes
// whose leaf has Class.AuthRequired. When unset, AuthRequired leaves
// are wrapped with a default AuthFunc that always returns an error,
// so unauthenticated calls receive 401.
func WithSSEAuth(fn api.AuthFunc) SSEOption {
	return func(c *sseConfig) { c.authFn = fn }
}

// WithSSEMiddleware installs middleware applied to every mounted SSE
// route, after any per-leaf safety wrapping (auth / confirmation).
// Middleware is applied in the order given: first wraps outermost.
func WithSSEMiddleware(mw ...func(http.Handler) http.Handler) SSEOption {
	return func(c *sseConfig) {
		c.middleware = append(c.middleware, mw...)
	}
}

// withSSEHeartbeat overrides the comment-frame interval. Unexported
// because production callers should not tune it; tests rely on a
// short interval to assert heartbeat behavior without sleeping for
// 15s.
func withSSEHeartbeat(d time.Duration) SSEOption {
	return func(c *sseConfig) { c.heartbeat = d }
}

// newSSEHandler returns the HandlerFunc that opens the stream for
// leaf and forwards Runner.Stream events as SSE frames.
func newSSEHandler(b *Bridge, leaf *Leaf, cfg sseConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Build invocation from the URL query.
		inv := buildSSEInvocation(r, leaf)

		// Pre-flight: check surface enablement + policy via a non-
		// executing path. We do this BEFORE writing any header so
		// sentinel errors can map to real HTTP status codes.
		if !leaf.Enabled[SurfaceSSE] {
			writeSSEError(w, fmt.Errorf("%w: %s on %s",
				ErrSurfaceNotEnabled, leaf.PathKey(), SurfaceSSE))
			return
		}
		if !b.Policy().Allowed(leaf.Class, SurfaceSSE) {
			writeSSEError(w, fmt.Errorf("%w: %s on %s",
				ErrDestructiveBlocked, leaf.PathKey(), SurfaceSSE))
			return
		}

		// Verify the response writer supports flushing. SSE without
		// flush is broken; refuse rather than buffer-and-pretend.
		flusher, ok := w.(http.Flusher)
		if !ok {
			api.Error(w, http.StatusInternalServerError, &api.APIError{
				Status:  http.StatusInternalServerError,
				Code:    "server_error",
				Message: "sse not supported: ResponseWriter is not an http.Flusher",
			})
			return
		}

		// Open the stream: set headers, status 200, flush. Past this
		// point, errors are SSE frames, not HTTP status codes.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Cancellable child ctx tied to the request. r.Context() is
		// already canceled on client disconnect by net/http.
		streamCtx, cancel := context.WithCancel(r.Context())
		defer cancel()

		events := make(chan Event, 16)
		errc := make(chan error, 1)
		go func() {
			errc <- b.Runner().Stream(streamCtx, inv, events)
		}()

		hb := time.NewTicker(cfg.heartbeat)
		defer hb.Stop()

		// Frame writer guards against concurrent writes from the
		// heartbeat ticker and the event-forward path.
		var writeMu sync.Mutex
		writeFrame := func(name string, data any) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			return sseWriteFrame(w, flusher, name, data)
		}
		writeComment := func() error {
			writeMu.Lock()
			defer writeMu.Unlock()
			return sseWriteComment(w, flusher)
		}

		var stdoutBuf, stderrBuf strings.Builder
		var runResult *Result // populated by the Runner's terminal "done" event, if any

		forwardEvent := func(ev Event) error {
			switch ev.Kind {
			case "done":
				if res, ok := ev.Data.(*Result); ok && res != nil {
					runResult = res
				}
				return nil
			case "stdout":
				if s, ok := ev.Data.(string); ok {
					stdoutBuf.WriteString(s)
					stdoutBuf.WriteByte('\n')
				}
			case "stderr":
				if s, ok := ev.Data.(string); ok {
					stderrBuf.WriteString(s)
					stderrBuf.WriteByte('\n')
				}
			}
			return writeFrame("event", ev)
		}

	loop:
		for {
			select {
			case <-r.Context().Done():
				cancel()
				drain(events)
				<-errc
				return
			case <-hb.C:
				if err := writeComment(); err != nil {
					cancel()
					drain(events)
					<-errc
					return
				}
			case ev, ok := <-events:
				if !ok {
					break loop
				}
				if err := forwardEvent(ev); err != nil {
					cancel()
					drain(events)
					<-errc
					return
				}
			}
		}

		// Stream goroutine has finished — collect its return.
		runErr := <-errc

		// Terminal frame: either error or result.
		if runErr != nil {
			_ = writeFrame("error", map[string]string{
				"code":    "stream_error",
				"message": runErr.Error(),
			})
			return
		}
		res := buildTerminalResult(runResult, stdoutBuf.String(), stderrBuf.String())
		_ = writeFrame("result", res)
	}
}

// buildSSEInvocation reads the query string into an Invocation,
// forcing Path to the resolved leaf's path and Surface to SurfaceSSE.
func buildSSEInvocation(r *http.Request, leaf *Leaf) Invocation {
	q := r.URL.Query()
	inv := Invocation{
		Path: append([]string(nil), leaf.Path...),
		Args: append([]string(nil), q["arg"]...),
	}
	for key, vals := range q {
		if !strings.HasPrefix(key, "flag.") {
			continue
		}
		if len(vals) == 0 {
			continue
		}
		if inv.Flags == nil {
			inv.Flags = make(map[string]any)
		}
		name := strings.TrimPrefix(key, "flag.")
		if name == "" {
			continue
		}
		if len(vals) == 1 {
			inv.Flags[name] = vals[0]
		} else {
			inv.Flags[name] = append([]string(nil), vals...)
		}
	}
	inv.Meta.Surface = SurfaceSSE
	inv.Meta.RequestedAt = time.Now()
	if tid := r.Header.Get("X-Request-ID"); tid != "" {
		inv.Meta.TraceID = tid
	}
	return inv
}

// buildTerminalResult returns the final Result frame. When the
// Runner produced its own *Result via a "done" Event we honor it;
// otherwise we synthesize one from the accumulated stdout/stderr.
func buildTerminalResult(fromRunner *Result, stdout, stderr string) Result {
	if fromRunner != nil {
		return *fromRunner
	}
	return Result{Stdout: stdout, Stderr: stderr}
}

// sseWriteFrame writes one "event: <name>\ndata: <json>\n\n" frame
// and flushes the response. data is JSON-encoded.
func sseWriteFrame(w http.ResponseWriter, f http.Flusher, name string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload); err != nil {
		return err
	}
	f.Flush()
	return nil
}

// sseWriteComment writes a comment frame ": ping\n\n" used as a
// keep-alive. SSE clients ignore lines starting with ':'.
func sseWriteComment(w http.ResponseWriter, f http.Flusher) error {
	if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
		return err
	}
	f.Flush()
	return nil
}

// writeSSEError writes the pre-stream sentinel-error response. It
// MUST only be called before any SSE header/frame has been written.
func writeSSEError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnknownCommand):
		api.Error(w, http.StatusNotFound, &api.APIError{
			Status:  http.StatusNotFound,
			Code:    "unknown_command",
			Message: err.Error(),
		})
	case errors.Is(err, ErrSurfaceNotEnabled):
		api.Error(w, http.StatusNotFound, &api.APIError{
			Status:  http.StatusNotFound,
			Code:    "not_enabled",
			Message: err.Error(),
		})
	case errors.Is(err, ErrDestructiveBlocked):
		api.Error(w, http.StatusForbidden, &api.APIError{
			Status:  http.StatusForbidden,
			Code:    "destructive_blocked",
			Message: err.Error(),
		})
	default:
		ae := api.MapError(err)
		api.Error(w, ae.Status, ae)
	}
}

// wrapSSEMiddleware composes confirmation gate → auth → user
// middleware around h. Mirrors the REST surface wrapper.
func wrapSSEMiddleware(h http.HandlerFunc, leaf *Leaf, cfg sseConfig) http.HandlerFunc {
	var handler http.Handler = h
	if leaf.Class.RequiresConfirmation {
		handler = sseConfirmationGate(handler)
	}
	if leaf.Class.AuthRequired {
		fn := cfg.authFn
		if fn == nil {
			fn = sseDenyAuth
		}
		handler = api.Auth(fn)(handler)
	}
	for i := len(cfg.middleware) - 1; i >= 0; i-- {
		handler = cfg.middleware[i](handler)
	}
	return handler.ServeHTTP
}

// sseConfirmationGate refuses requests that lack the X-Confirm-Token
// header with 428 Precondition Required. The token's value is not
// inspected — only its presence.
func sseConfirmationGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Confirm-Token") == "" {
			api.Error(w, http.StatusPreconditionRequired, &api.APIError{
				Status:  http.StatusPreconditionRequired,
				Code:    "confirmation_required",
				Message: "X-Confirm-Token header required for this command",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sseDenyAuth is the fallback AuthFunc used when WithSSEAuth is not
// supplied but a leaf requires auth. It always refuses, producing 401.
func sseDenyAuth(_ *http.Request) (any, error) {
	return nil, errors.New("authentication required")
}
