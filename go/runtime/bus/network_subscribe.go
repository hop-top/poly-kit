package bus

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/coder/websocket"
)

// errLinkClosed reports a request whose link closed before the reply.
var errLinkClosed = errors.New("bus: link closed before the reply")

// Subscribe adds patterns to what the acceptor at addr sends this
// dialer. The first explicit subscription replaces the implicit "#".
// It returns the ack's offset: every event after it is filtered by the
// new set. On a disconnected address it only updates the set sent in
// the next handshake and returns 0.
func (n *NetworkAdapter) Subscribe(ctx context.Context, addr string, patterns ...string) (uint64, error) {
	return n.request(ctx, addr, ctlRequest{Type: "subscribe", Patterns: patterns})
}

// Unsubscribe removes patterns; see [NetworkAdapter.Subscribe].
func (n *NetworkAdapter) Unsubscribe(ctx context.Context, addr string, patterns ...string) (uint64, error) {
	return n.request(ctx, addr, ctlRequest{Type: "unsubscribe", Patterns: patterns})
}

// SetScope replaces the workspace scope the acceptor at addr applies;
// see [NetworkAdapter.Subscribe].
func (n *NetworkAdapter) SetScope(ctx context.Context, addr string, s Scope) (uint64, error) {
	if s.Workspaces == nil {
		s.Workspaces = []string{}
	}
	return n.request(ctx, addr, ctlRequest{Type: "subscribe", Scope: &s})
}

func (n *NetworkAdapter) request(ctx context.Context, addr string, r ctlRequest) (uint64, error) {
	if r.Patterns != nil || r.Type == "unsubscribe" {
		if len(r.Patterns) == 0 {
			return 0, streamErr(CodeBadRequest, "%s needs patterns", r.Type)
		}
		for _, p := range r.Patterns {
			if err := validatePattern(p); err != nil {
				return 0, err
			}
		}
	}
	ps := n.peerFor(addr)
	n.mu.RLock()
	l := n.conns[addr]
	n.mu.RUnlock()
	if l == nil {
		ps.mu.Lock()
		defer ps.mu.Unlock()
		next, err := ps.subs.apply(r, n.maxSubs)
		if err != nil {
			return 0, err
		}
		ps.subs = next
		return 0, nil
	}
	if !l.stream {
		return 0, ErrStreamsUnsupported
	}

	r.ID = "r" + strconv.FormatUint(l.nextID.Add(1), 10)
	ch := make(chan dialerCtl, 1)
	l.pendMu.Lock()
	l.pending[r.ID] = ch
	l.pendMu.Unlock()
	frame := map[string]any{"type": r.Type, "id": r.ID}
	if r.Patterns != nil {
		frame["patterns"] = r.Patterns
	}
	if r.Scope != nil {
		frame["scope"] = r.Scope
	}
	data, _ := json.Marshal(frame)
	if err := l.conn.Write(ctx, websocket.MessageText, data); err != nil {
		l.pendMu.Lock()
		delete(l.pending, r.ID)
		l.pendMu.Unlock()
		return 0, err
	}
	select {
	case <-ctx.Done():
		l.pendMu.Lock()
		delete(l.pending, r.ID)
		l.pendMu.Unlock()
		return 0, ctx.Err()
	case reply, ok := <-ch:
		if !ok {
			return 0, errLinkClosed
		}
		if reply.Kind == "error" {
			return 0, &StreamError{Code: reply.Code, Message: reply.Message}
		}
		ps.mu.Lock()
		if next, err := ps.subs.apply(r, n.maxSubs); err == nil {
			ps.subs = next
		}
		ps.mu.Unlock()
		return reply.At, nil
	}
}
