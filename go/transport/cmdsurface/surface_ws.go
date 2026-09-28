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

	"github.com/coder/websocket"

	"hop.top/kit/go/transport/api"
)

// MountWS registers a WebSocket endpoint at the configured path on r
// that lets clients invoke commands and receive Events as JSON frames.
//
// Wire protocol (JSON over WS frames):
//
//	client → server  {"op":"invoke","id":"<corr-id>","invocation":{Invocation}}
//	client → server  {"op":"cancel","id":"<corr-id>"}
//	server → client  {"op":"event","id":"<corr-id>","event":{Event}}
//	server → client  {"op":"result","id":"<corr-id>","result":{Result}}
//	server → client  {"op":"error","id":"<corr-id>","error":{"code":"...","message":"..."}}
//
// Per invocation, the surface ALWAYS streams via Admission.Stream and
// emits a terminal "result" frame derived from the run's accumulated
// stdout / stderr and (when present) the ExitCode carried on the
// runner's terminal "done" event Data field. Clients that just want
// the final Result can ignore "event" frames.
//
// Safety gates applied before the upgrade completes:
//
//   - SafetyClass.AuthRequired      → 401 unauthenticated unless the
//     initial HTTP request was verified: by the AuthFunc given with
//     WithWSAuth, or by an api.Auth on the router. A bare
//     Authorization header is not authentication. The verified claims
//     are the identity of every invocation on the connection, replacing
//     the caller a frame names.
//   - SafetyClass.RequiresConfirmation → 428 when the X-Confirm-Token
//     header is missing on the initial HTTP request.
//
// Per-leaf gates apply uniformly because a single WS connection may
// invoke any number of leaves; therefore the upgrade enforces the
// strictest matrix that covers every leaf the bridge has exposed on
// SurfaceWS. This mirrors REST / RPC, where leaves with auth or
// confirmation requirements gate the entire route at request time.
//
// Each client message is capped at WithWSMaxMessageBytes (default
// DefaultWSMaxMessageBytes); one over the cap closes the connection
// with status 1009 and is audited as ErrBodyTooLarge.
//
// Per-invocation gates fire inside the connection, through
// [Bridge.Admit] — the gates, order, errors and audit of
// [Bridge.Invoke] — before anything runs. A refusal is the
// invocation's only frame, an "error" frame for its id; the
// connection stays open for other invocations:
//
//   - Unknown leaf path → "error" code=unknown_command.
//   - Leaf not enabled on SurfaceWS → "error" code=not_enabled.
//   - Interactive or self-hosting leaf → "error" code=not_invocable.
//   - Destructive without Policy.AllowDestructiveOn → "error"
//     code=destructive_blocked.
//   - Refused by the bridge's PermissionFunc → "error"
//     code=permission_denied.
//
// MountWS forces inv.Path to the resolved leaf path and inv.Meta.Surface
// to SurfaceWS. Concurrent invocations on the same connection are
// allowed: each invoke runs in its own goroutine keyed by the client's
// correlation id. A cancel frame with a known id aborts the running
// invocation by canceling its per-invocation context.
//
// b and r are required; nil values return an error.
func MountWS(b *Bridge, r *api.Router, opts ...WSOption) error {
	if b == nil {
		return errors.New("cmdsurface: MountWS: nil Bridge")
	}
	if r == nil {
		return errors.New("cmdsurface: MountWS: nil api.Router")
	}
	cfg := defaultWSConfig()
	for _, o := range opts {
		o(&cfg)
	}

	// Build the leaf index once at mount; later Expose/Hide updates
	// the Enabled map on the shared *Leaf in-place, so this snapshot
	// stays consistent without re-indexing.
	index := indexLeaves(b)

	// Lazy hub start — only if caller didn't bring their own.
	if cfg.hub == nil {
		cfg.hub = api.NewHub(hubOptionsFromConfig(cfg)...)
		go cfg.hub.Run(cfg.ctx)
	}

	handler := newWSHandler(b, index, cfg)
	if cfg.authFn != nil {
		handler = api.Auth(cfg.authFn)(handler).ServeHTTP
	}
	r.Handle(http.MethodGet, cfg.path, handler)
	return nil
}

// WSOption configures MountWS.
type WSOption func(*wsConfig)

// wsConfig is the resolved option bag MountWS consumes.
type wsConfig struct {
	path           string
	hub            *api.Hub
	ctx            context.Context
	originPatterns []string
	maxMessage     int64
	authFn         api.AuthFunc
}

// WithWSAuth authenticates every upgrade request with fn, through
// api.Auth: a request fn refuses is answered 401 unauthenticated and
// never upgraded, and one it accepts carries fn's claims as the
// identity of every invocation on the connection. Without it the
// surface relies on an api.Auth on the router, if any, and refuses the
// upgrade when a WS-enabled leaf declares kit/auth-required and
// nothing verified the request.
func WithWSAuth(fn api.AuthFunc) WSOption {
	return func(c *wsConfig) { c.authFn = fn }
}

// DefaultWSMaxMessageBytes is the per-message read cap MountWS
// applies when WithWSMaxMessageBytes is not given: 32 KiB, the
// websocket library's own default, made explicit so it is visible
// and configurable. It bounds one frame, not the connection.
const DefaultWSMaxMessageBytes int64 = 32 << 10

// WithWSMaxMessageBytes caps each client message at n bytes. Zero
// keeps DefaultWSMaxMessageBytes; a negative n disables the cap. A
// message over the cap closes the connection with status 1009
// (message too big), the refusal the WebSocket protocol defines, and
// is audited through the bridge's sinks as ErrBodyTooLarge.
func WithWSMaxMessageBytes(n int64) WSOption {
	return func(c *wsConfig) { c.maxMessage = n }
}

// wsReadLimit resolves the configured per-message cap.
func wsReadLimit(n int64) int64 {
	if n == 0 {
		return DefaultWSMaxMessageBytes
	}
	return n
}

func defaultWSConfig() wsConfig {
	return wsConfig{
		path: "/ws/cmd",
		ctx:  context.Background(),
	}
}

// hubOptionsFromConfig converts the surface-level options that map
// onto api.Hub options into the form api.NewHub accepts.
func hubOptionsFromConfig(cfg wsConfig) []api.HubOption {
	var out []api.HubOption
	if len(cfg.originPatterns) > 0 {
		out = append(out, api.WithAcceptOrigins(cfg.originPatterns...))
	}
	return out
}

// WithWSPath sets the URL path at which MountWS registers the WS
// upgrade handler. Default: "/ws/cmd".
func WithWSPath(path string) WSOption {
	return func(c *wsConfig) {
		if path != "" {
			c.path = path
		}
	}
}

// WithWSHub injects an existing *api.Hub. When set, the caller owns
// the hub's lifecycle and MountWS does NOT call hub.Run. When unset,
// MountWS creates a hub and starts hub.Run in a goroutine bound to
// the context supplied via WithWSContext (default context.Background).
func WithWSHub(hub *api.Hub) WSOption {
	return func(c *wsConfig) { c.hub = hub }
}

// WithWSContext sets the context that bounds the lifetime of the hub
// goroutine MountWS starts when WithWSHub is not supplied. Has no
// effect when WithWSHub is set. Default: context.Background.
func WithWSContext(ctx context.Context) WSOption {
	return func(c *wsConfig) {
		if ctx != nil {
			c.ctx = ctx
		}
	}
}

// WithWSAcceptOrigins configures allowed origins for the WebSocket
// upgrade. Each pattern is passed to websocket.AcceptOptions.
// OriginPatterns. Default is empty (same-origin only).
func WithWSAcceptOrigins(origins ...string) WSOption {
	return func(c *wsConfig) {
		c.originPatterns = append(c.originPatterns, origins...)
	}
}

// wsFrame is the on-wire envelope sent in both directions. Each field
// is optional; readers dispatch on Op.
type wsFrame struct {
	Op         string          `json:"op"`
	ID         string          `json:"id,omitempty"`
	Invocation *Invocation     `json:"invocation,omitempty"`
	Event      *Event          `json:"event,omitempty"`
	Result     *Result         `json:"result,omitempty"`
	Error      *wsErrorPayload `json:"error,omitempty"`
}

// wsErrorPayload is the body of an "error" server-to-client frame.
type wsErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// wsConn tracks the per-connection state of one upgraded client.
// Concurrent invocations share writeMu so frames are not interleaved;
// active cancels access invMu (read paths) and the per-id map.
type wsConn struct {
	conn *websocket.Conn

	writeMu sync.Mutex

	invMu sync.Mutex
	// cancels maps a client-supplied correlation id to the cancel
	// function of the per-invocation context. cancel frames look up
	// the entry; the worker goroutine deletes its entry on exit.
	cancels map[string]context.CancelFunc
}

// newWSConn returns a wsConn ready to handle frames.
func newWSConn(c *websocket.Conn) *wsConn {
	return &wsConn{
		conn:    c,
		cancels: make(map[string]context.CancelFunc),
	}
}

// writeFrame serializes and writes a frame, serialized by writeMu so
// concurrent worker goroutines never interleave bytes on the wire.
func (wc *wsConn) writeFrame(ctx context.Context, f wsFrame) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	wc.writeMu.Lock()
	defer wc.writeMu.Unlock()
	return wc.conn.Write(ctx, websocket.MessageText, data)
}

// registerCancel stores a cancel function under id; returns false if
// id is already in use (clients reusing an id during a live run is
// rejected as a protocol error by the caller).
func (wc *wsConn) registerCancel(id string, cancel context.CancelFunc) bool {
	wc.invMu.Lock()
	defer wc.invMu.Unlock()
	if _, exists := wc.cancels[id]; exists {
		return false
	}
	wc.cancels[id] = cancel
	return true
}

// finishCancel removes id from the active map. Safe to call even if
// id is not present (idempotent on the worker's exit path).
func (wc *wsConn) finishCancel(id string) {
	wc.invMu.Lock()
	delete(wc.cancels, id)
	wc.invMu.Unlock()
}

// cancelOne triggers cancellation of the invocation registered under
// id, if any. Returns false when id was not active.
func (wc *wsConn) cancelOne(id string) bool {
	wc.invMu.Lock()
	cancel, ok := wc.cancels[id]
	if ok {
		delete(wc.cancels, id)
	}
	wc.invMu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// cancelAll fires every registered cancel function. Called on
// connection teardown so pending Runner.Stream calls observe the
// context cancellation and exit.
func (wc *wsConn) cancelAll() {
	wc.invMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(wc.cancels))
	for _, c := range wc.cancels {
		cancels = append(cancels, c)
	}
	wc.cancels = make(map[string]context.CancelFunc)
	wc.invMu.Unlock()
	for _, c := range cancels {
		c()
	}
}

// newWSHandler returns the http.HandlerFunc registered on the router.
// It enforces the pre-upgrade safety matrix, accepts the WebSocket,
// then enters the per-connection read loop.
func newWSHandler(
	b *Bridge,
	index map[string]*Leaf,
	cfg wsConfig,
) http.HandlerFunc {
	// Aggregate the safety matrix across every WS-enabled leaf: if ANY
	// such leaf requires auth or confirmation, gate the upgrade for
	// every client. A future refinement might split this per-leaf
	// across separate endpoints; for now we match REST behavior at
	// the connection scope.
	requireAuth, requireConfirm := aggregateSafety(b)

	return func(w http.ResponseWriter, r *http.Request) {
		if requireAuth && !api.Authenticated(r.Context()) {
			api.WriteUnauthenticated(w, "", "authentication required")
			return
		}
		if requireConfirm && r.Header.Get(wsConfirmHeader) == "" {
			http.Error(w, "confirmation_required", http.StatusPreconditionRequired)
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: cfg.originPatterns,
		})
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
		limit := wsReadLimit(cfg.maxMessage)
		conn.SetReadLimit(limit)

		wc := newWSConn(conn)
		defer wc.cancelAll()

		// Read frames until the client disconnects or returns an
		// error. Each invoke spawns a worker goroutine; cancel frames
		// signal a worker via its registered cancel func.
		ctx := r.Context()
		// Every frame's identity is the upgrade request's verdict.
		identify := func(m Meta) Meta { return establishHTTP(m, r) }
		runWSLoop(ctx, b, index, wc, identify, func() { b.AuditBodyTooLarge(r, SurfaceWS, nil, limit) })
	}
}

const wsConfirmHeader = "X-Confirm-Token"

// aggregateSafety scans all WS-enabled leaves and reports whether
// ANY leaf requires auth or confirmation. Surfaces enforce the
// strictest gate at upgrade time so leaves with stronger safety
// requirements remain reachable only by authorized clients.
func aggregateSafety(b *Bridge) (auth, confirm bool) {
	for _, l := range b.Leaves() {
		if !l.Enabled[SurfaceWS] {
			continue
		}
		if l.Class.AuthRequired {
			auth = true
		}
		if l.Class.RequiresConfirmation {
			confirm = true
		}
	}
	return auth, confirm
}

// runWSLoop reads frames until the connection ends. Decode errors on
// a single frame are reported back via an "error" frame and the loop
// continues; the loop exits only on transport-level read errors.
// onTooLarge observes a message over the read cap; the library has
// already closed the connection with 1009 by the time it runs.
func runWSLoop(
	ctx context.Context,
	b *Bridge,
	index map[string]*Leaf,
	wc *wsConn,
	identify func(Meta) Meta,
	onTooLarge func(),
) {
	for {
		_, data, err := wc.conn.Read(ctx)
		if err != nil {
			if errors.Is(err, websocket.ErrMessageTooBig) {
				onTooLarge()
			}
			return
		}
		var f wsFrame
		if err := json.Unmarshal(data, &f); err != nil {
			_ = wc.writeFrame(ctx, wsFrame{
				Op:    "error",
				Error: &wsErrorPayload{Code: "bad_request", Message: err.Error()},
			})
			continue
		}
		switch f.Op {
		case "invoke":
			handleInvoke(ctx, b, index, wc, identify, f)
		case "cancel":
			wc.cancelOne(f.ID)
		default:
			_ = wc.writeFrame(ctx, wsFrame{
				Op: "error",
				ID: f.ID,
				Error: &wsErrorPayload{
					Code:    "bad_request",
					Message: fmt.Sprintf("unknown op %q", f.Op),
				},
			})
		}
	}
}

// handleInvoke validates the invoke frame, canonicalises the
// invocation, and spawns a streamer goroutine that admits it, forwards
// events as frames and emits a terminal frame.
func handleInvoke(
	parent context.Context,
	b *Bridge,
	index map[string]*Leaf,
	wc *wsConn,
	identify func(Meta) Meta,
	f wsFrame,
) {
	if f.Invocation == nil {
		_ = wc.writeFrame(parent, wsFrame{
			Op:    "error",
			ID:    f.ID,
			Error: &wsErrorPayload{Code: "bad_request", Message: "missing invocation"},
		})
		return
	}
	inv := *f.Invocation
	// Canonicalise: resolved path + forced surface, so callers cannot
	// spoof Meta.Surface through the frame. An unknown path is left
	// as sent; admission refuses it.
	if leaf, ok := index[strings.Join(inv.Path, " ")]; ok {
		inv.Path = append([]string(nil), leaf.Path...)
	}
	inv.Meta = identify(inv.Meta)
	inv.Meta.Surface = SurfaceWS
	inv.Meta.RequestedAt = time.Now()

	invCtx, cancel := context.WithCancel(parent)
	if !wc.registerCancel(f.ID, cancel) {
		cancel()
		_ = wc.writeFrame(parent, wsFrame{
			Op: "error",
			ID: f.ID,
			Error: &wsErrorPayload{
				Code:    "bad_request",
				Message: "id already in flight",
			},
		})
		return
	}

	go runStream(parent, invCtx, b, wc, f.ID, inv)
}

// runStream admits inv through [Bridge.Admit], then runs it with
// [Admission.Stream] and forwards every Event as a frame. A refusal
// is the invocation's only frame: an "error" frame carrying the
// sentinel's wire code, audited by the bridge. On the terminal "done"
// event, it derives a Result from accumulated stdout / stderr and
// (when the runner provided one) the Result on the done event's Data
// field. A final "result" frame is always emitted on clean
// termination; a final "error" frame is emitted if the run returns a
// non-nil error.
//
// Admission runs here, on the invocation's own goroutine and under
// runCtx, so a slow permission decision never holds up the
// connection's read loop, and a "cancel" frame for this id cancels
// the context the decision is made under.
//
// parent is the connection-scoped context; runCtx is the per-invocation
// context that can be canceled by a client "cancel" op. Terminal
// frames write under parent so a canceled invocation still delivers a
// closing frame (error or result) to the client.
func runStream(
	parent context.Context,
	runCtx context.Context,
	b *Bridge,
	wc *wsConn,
	id string,
	inv Invocation,
) {
	defer wc.finishCancel(id)

	adm, err := b.Admit(runCtx, inv)
	if err != nil {
		_ = wc.writeFrame(parent, wsFrame{
			Op: "error",
			ID: id,
			Error: &wsErrorPayload{
				Code:    errorCode(err),
				Message: err.Error(),
			},
		})
		return
	}

	events := make(chan Event, 16)
	errc := make(chan error, 1)
	go func() {
		errc <- adm.Stream(runCtx, events)
	}()

	res := Result{}
	var stdout, stderr strings.Builder
	sawDoneResult := false

	for ev := range events {
		ev := ev
		// Forward event frames under parent — runCtx may be canceled
		// mid-stream, but the client should still see the events the
		// runner already produced before cancellation.
		_ = wc.writeFrame(parent, wsFrame{Op: "event", ID: id, Event: &ev})

		switch ev.Kind {
		case "stdout":
			if s, ok := ev.Data.(string); ok {
				if stdout.Len() > 0 {
					stdout.WriteByte('\n')
				}
				stdout.WriteString(s)
			}
		case "stderr":
			if s, ok := ev.Data.(string); ok {
				if stderr.Len() > 0 {
					stderr.WriteByte('\n')
				}
				stderr.WriteString(s)
			}
		case "done":
			// Prefer the Result the runner carries on done; if absent,
			// fall back to the accumulated text.
			if r, ok := ev.Data.(*Result); ok && r != nil {
				res = *r
				sawDoneResult = true
			}
		}
	}

	streamErr := <-errc
	if streamErr != nil {
		_ = wc.writeFrame(parent, wsFrame{
			Op: "error",
			ID: id,
			Error: &wsErrorPayload{
				Code:    errorCode(streamErr),
				Message: streamErr.Error(),
			},
		})
		return
	}
	if !sawDoneResult {
		res.Stdout = stdout.String()
		res.Stderr = stderr.String()
	}
	_ = wc.writeFrame(parent, wsFrame{Op: "result", ID: id, Result: &res})
}

// errorCode maps known bridge sentinels to wire codes; unknown errors
// fall through to "internal" so clients have a stable enumeration.
func errorCode(err error) string {
	switch {
	case errors.Is(err, ErrAuthRefused):
		return api.CodeUnauthenticated
	case errors.Is(err, ErrUnknownCommand):
		return "unknown_command"
	case errors.Is(err, ErrSurfaceNotEnabled):
		return "not_enabled"
	case errors.Is(err, ErrNotInvocable):
		return api.CodeNotInvocable
	case errors.Is(err, ErrDestructiveBlocked):
		return "destructive_blocked"
	case errors.Is(err, ErrInsufficientScope):
		return api.CodeInsufficientScope
	case errors.Is(err, ErrPermissionDenied):
		return api.CodePermissionDenied
	case errors.Is(err, ErrRateLimited):
		return api.CodeRateLimited
	case errors.Is(err, context.DeadlineExceeded):
		return api.CodeDeadlineExceeded
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "internal"
	}
}
