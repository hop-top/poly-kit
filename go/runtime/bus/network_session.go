package bus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"hop.top/kit/go/core/netpolicy"
)

const handshakeTimeout = 10 * time.Second

// ErrDisconnected is returned by a Connect that lost a race with
// Disconnect for the same address: no link was made.
var ErrDisconnected = errors.New("bus: address disconnected during connect")

// errAckTimeout reports a handshake whose ack never came: a transport
// problem, retried, reported with code "timeout". It wraps ErrAuthFailed
// for callers that matched that error before.
var errAckTimeout = fmt.Errorf("%w: no ack before the timeout", ErrAuthFailed)

// Connect establishes a WebSocket connection to a remote peer and
// registers addr as an outbound peer. With an authenticator the auth
// exchange doubles as the stream handshake. A refused session returns a
// *StreamError and a malformed handshake reply a protocol error; both
// are final, so the address is not retried until the next explicit
// Connect. A rejected token (ErrAuthFailed) or a missing ack (an
// ErrAuthFailed reported with code "timeout") may be retried: the
// authenticator may hand out a fresh token. Only a reconnect loop
// retries (one started by WithNetwork, Dial, or a dropped link); a
// first Connect that fails starts none, so use Dial to keep trying.
// A Connect that loses a race with Disconnect returns ErrDisconnected.
func (n *NetworkAdapter) Connect(ctx context.Context, addr string) error {
	return n.connect(ctx, addr, false)
}

// connect dials addr. fromLoop marks a reconnect, which never
// re-registers an address that Disconnect forgot.
func (n *NetworkAdapter) connect(ctx context.Context, addr string, fromLoop bool) error {
	if n.closed.Load() {
		return ErrBusClosed
	}

	// Remember the policy for reconnects, which start from their own
	// context long after this one is gone. Sticky by design: once a run
	// is offline, a later online Connect must not un-set it for peers
	// dialed under the offline policy.
	if netpolicy.IsOffline(ctx) {
		n.offline.Store(true)
	}

	var ps *peerState
	if fromLoop {
		if ps = n.peer(addr); ps == nil {
			return nil
		}
	}
	n.mu.RLock()
	_, dup := n.conns[addr]
	n.mu.RUnlock()
	if dup {
		return nil
	}

	// coder/websocket handshakes over an *http.Client, so the policy is
	// applied through a guarded transport rather than a dialer. Naming
	// the client here rather than relying on http.DefaultClient keeps
	// enforcement explicit and independent of netpolicy.Install order.
	conn, _, err := websocket.Dial(ctx, addr, &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: netpolicy.Guard(http.DefaultTransport)},
	})
	if err != nil {
		return err
	}
	if ps == nil {
		ps = n.peerFor(addr)
	}

	var ack *ackFrame
	if n.auth != nil {
		a, err := n.dialHandshake(ctx, conn, ps)
		if err != nil {
			return n.handshakeFailed(addr, ps, err)
		}
		ack = &a
	}

	var peerCaps *streamCaps
	if ack != nil {
		peerCaps = ack.Caps
	}
	ps.mu.Lock()
	err = checkAck(ack, ps.cursor.request())
	ps.mu.Unlock()
	if err != nil {
		_ = conn.Close(statusProtocol, "protocol error")
		return n.handshakeFailed(addr, ps, err)
	}
	v, format := negotiate(n.caps(), peerCaps, n.auth != nil, n.frames)
	l := n.newLink(conn, addr, true)
	l.ps, l.cursor, l.format = ps, ps.send, format
	l.stream = v >= 1 && ack != nil && ack.Stream != nil
	if !l.stream {
		ack = nil
	} else {
		l.peer.Origin = ack.Origin
		l.heartbeatD = time.Duration(ack.Stream.HeartbeatMs) * time.Millisecond
	}

	// Register in one critical section with the checks that decide
	// whether the link may exist at all.
	n.mu.Lock()
	switch {
	case n.closed.Load():
		n.mu.Unlock()
		_ = conn.Close(statusGoingAway, "closing")
		return ErrBusClosed
	case n.peers[addr] != ps:
		// Disconnect forgot the address while we dialed.
		n.mu.Unlock()
		_ = conn.Close(statusNormal, "disconnect")
		if fromLoop {
			return nil
		}
		return ErrDisconnected
	case n.conns[addr] != nil:
		// Lost a race with a reconnect.
		n.mu.Unlock()
		_ = conn.Close(statusNormal, "duplicate")
		return nil
	}
	n.conns[addr] = l
	n.wg.Add(1)
	n.mu.Unlock()

	ps.mu.Lock()
	resume, gap, _ := ps.cursor.linkUp(ack, ps.connected) // checked above, under no concurrent change
	ps.connected = true
	ps.send.linkUp(n.log)
	// A fresh state also clears Final from an earlier refused session.
	ps.state = LinkState{Connected: true, Streams: l.stream, Format: format, Resume: resume}
	if l.stream {
		ps.state.Stream = ack.Stream.ID
	}
	st := ps.state
	ps.mu.Unlock()

	n.publishConnected(addr, st)
	if gap != nil {
		n.invalidate(addr, gap)
	}
	l.start()
	return nil
}

// handshakeFailed reports a failed handshake and records it on the
// address. Session errors and protocol errors are final; a rejected
// token is not. link.disconnected is published once per change of
// code, so a retrying address does not flood the bus.
func (n *NetworkAdapter) handshakeFailed(addr string, ps *peerState, err error) error {
	n.reportErr(fmt.Errorf("bus: handshake with %s: %w", addr, err))
	var (
		se    *StreamError
		code  string
		final bool
	)
	switch {
	case errors.As(err, &se):
		code, final = se.Code, true
	case isProtocolError(err):
		code, final = "protocol", true
	case errors.Is(err, errAckTimeout):
		code = "timeout"
	case errors.Is(err, ErrAuthFailed):
		code = "auth"
	default:
		return err
	}
	ps.mu.Lock()
	repeat := !final && !ps.state.Connected && ps.state.Code == code && !ps.state.Final
	ps.state.Connected = false
	ps.state.Code = code
	ps.state.Final = final
	ps.mu.Unlock()
	if !repeat {
		n.publishDisconnected(addr, final, code)
	}
	return err
}

// isFinalConnectErr reports whether a connect error stops retrying.
func isFinalConnectErr(err error) bool {
	var se *StreamError
	return errors.As(err, &se) || isProtocolError(err)
}

// dialHandshake sends the auth frame (caps, origin, session) and reads
// the acceptor's reply.
func (n *NetworkAdapter) dialHandshake(ctx context.Context, conn *websocket.Conn, ps *peerState) (ackFrame, error) {
	token, err := n.auth.Token()
	if err != nil {
		_ = conn.Close(statusInternal, "auth failed")
		return ackFrame{}, err
	}
	caps := n.caps()
	ps.mu.Lock()
	sess := &sessionReq{Scope: ps.subs.Scope, Resume: ps.cursor.request()}
	if !ps.subs.Implicit {
		sess.Subscribe = ps.subs.sortedPatterns()
	}
	ps.mu.Unlock()
	if err := conn.Write(ctx, websocket.MessageText, encodeAuthFrame(token, &caps, n.originID, sess)); err != nil {
		_ = conn.Close(statusInternal, "auth write failed")
		return ackFrame{}, err
	}
	ackCtx, ackCancel := context.WithTimeout(ctx, n.ackTimeout)
	_, data, err := conn.Read(ackCtx)
	ackCancel()
	if err != nil {
		if websocket.CloseStatus(err) == statusPolicy {
			return ackFrame{}, ErrAuthFailed // the acceptor refused us
		}
		_ = conn.Close(statusPolicy, "auth ack timeout")
		return ackFrame{}, errAckTimeout
	}
	a, err := parseAck(data, n.caps().Streams)
	switch {
	case isProtocolError(err):
		_ = conn.Close(statusProtocol, "protocol error")
		return ackFrame{}, err
	case err != nil:
		_ = conn.Close(statusPolicy, "auth rejected")
		return ackFrame{}, ErrAuthFailed
	case a.Kind == "session_error":
		_ = conn.Close(statusPolicy, "invalid session")
		return ackFrame{}, a.Err
	}
	return a, nil
}

// Handler returns an http.Handler that accepts inbound WebSocket peers.
func (n *NetworkAdapter) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		peer := Peer{Addr: r.RemoteAddr}

		var af authFrame
		handshake := n.auth != nil
		if handshake {
			authCtx, authCancel := context.WithTimeout(ctx, handshakeTimeout)
			_, msg, err := conn.Read(authCtx)
			authCancel()
			if err != nil {
				_ = conn.Close(statusPolicy, "auth required")
				return
			}
			var perr *StreamError
			af, perr = parseAuthFrame(msg)
			if perr != nil && perr.Code == codeInvalidAuth {
				_ = conn.Close(statusPolicy, "invalid auth")
				return
			}
			if err := n.auth.Verify(af.Token); err != nil {
				_ = conn.Close(statusPolicy, "auth failed")
				return
			}
			if perr != nil {
				n.refuseSession(ctx, conn, perr)
				return
			}
			peer.Origin, peer.Token = af.Origin, af.Token
		}

		v, format := negotiate(n.caps(), af.Caps, handshake, n.frames)
		l := n.newLink(conn, peer.Addr, false)
		l.peer, l.format, l.stream = peer, format, v >= 1

		var sess sessionReq
		if l.stream && af.Session != nil {
			sess = *af.Session
		}
		if sess.Subscribe != nil {
			l.subs = l.subs.withPatterns(sess.Subscribe)
			if len(l.subs.Patterns) > n.maxSubs {
				n.refuseOrClose(ctx, conn, af.Caps != nil, streamErr(CodeLimit, "more than %d subscriptions", n.maxSubs))
				return
			}
		}
		l.subs.Scope = sess.Scope
		if n.authorize != nil {
			if err := n.authorize(ctx, peer, l.subs.effective()); err != nil {
				n.refuseOrClose(ctx, conn, af.Caps != nil, streamErr(CodeForbidden, "%v", err))
				return
			}
		}

		var (
			si      *streamInfo
			outcome *resumeOutcome
			cursor  = n.log.Head()
		)
		if l.stream {
			outcome, cursor = decideResume(n.log.ID(), n.log.Oldest(), n.log.Head(), sess.Resume)
			si = &streamInfo{ID: n.log.ID(), Oldest: n.log.Oldest(), Head: n.log.Head(), HeartbeatMs: n.heartbeat.Milliseconds()}
		}
		l.cursor = &sendCursor{at: cursor}
		l.flushing = false

		if handshake {
			ack := encodeAck(af.Caps != nil, n.caps(), n.originID, si, outcome)
			if err := conn.Write(ctx, websocket.MessageText, ack); err != nil {
				_ = conn.Close(statusInternal, "ack write failed")
				return
			}
		}
		n.mu.Lock()
		if n.closed.Load() {
			n.mu.Unlock()
			_ = conn.Close(statusGoingAway, "closing")
			return
		}
		n.conns[peer.Addr] = l
		n.wg.Add(1)
		n.mu.Unlock()
		l.start()
		n.hook("accepted")
	})
}

// refuseOrClose refuses a session: a dialer that sent caps gets a
// session_error; a legacy dialer gets a 1008 close without an ack.
func (n *NetworkAdapter) refuseOrClose(ctx context.Context, conn *websocket.Conn, dialerCaps bool, e *StreamError) {
	if dialerCaps {
		n.refuseSession(ctx, conn, e)
		return
	}
	_ = conn.Close(statusPolicy, e.Code)
}

func (n *NetworkAdapter) refuseSession(ctx context.Context, conn *websocket.Conn, e *StreamError) {
	_ = conn.Write(ctx, websocket.MessageText, encodeSessionError(e))
	_ = conn.Close(statusPolicy, "invalid session")
}
