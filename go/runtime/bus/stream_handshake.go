package bus

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
)

// StreamsVersion is the stream protocol version this adapter speaks.
const StreamsVersion = 1

// knownFrames are the frame format names a peer may advertise.
var knownFrames = []string{"kit", "cloudevents"}

// streamCaps is the capability advertisement in the handshake.
type streamCaps struct {
	Streams int
	Frames  []string
}

func (c streamCaps) MarshalJSON() ([]byte, error) {
	fr := c.Frames
	if fr == nil {
		fr = []string{}
	}
	return json.Marshal(struct {
		Streams int      `json:"streams"`
		Frames  []string `json:"frames"`
	}{c.Streams, fr})
}

func (c streamCaps) decodes(f FrameFormat) bool {
	for _, name := range c.Frames {
		if name == f.String() {
			return true
		}
	}
	return false
}

// parseCaps decodes caps leniently: a non-object is absent, a bad
// streams is 0, unknown frame names are dropped.
func parseCaps(raw json.RawMessage) *streamCaps {
	var m map[string]json.RawMessage
	if isNull(raw) || json.Unmarshal(raw, &m) != nil || m == nil {
		return nil
	}
	c := &streamCaps{Frames: []string{}}
	if s := m["streams"]; len(s) > 0 && bytes.IndexAny(s, ".eE") < 0 {
		if v, err := strconv.Atoi(string(s)); err == nil && v >= 0 {
			c.Streams = v
		}
	}
	var frames []json.RawMessage
	if json.Unmarshal(m["frames"], &frames) == nil {
		for _, raw := range frames {
			var name string
			if json.Unmarshal(raw, &name) != nil {
				continue
			}
			for _, k := range knownFrames {
				if name == k {
					c.Frames = append(c.Frames, name)
				}
			}
		}
	}
	return c
}

type resumeReq struct {
	Stream string
	After  uint64
}

// sessionReq is the dialer's session request. Nil fields were absent.
type sessionReq struct {
	Subscribe []string
	Scope     *Scope
	Resume    *resumeReq
}

// authFrame is the dialer's first frame as the acceptor reads it.
type authFrame struct {
	Token   string
	Caps    *streamCaps
	Origin  string
	Session *sessionReq
}

// parseAuthFrame decodes the dialer's first frame. An unusable token is
// codeInvalidAuth; a malformed session carries a control-frame code.
func parseAuthFrame(data []byte) (authFrame, *StreamError) {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil || m == nil {
		return authFrame{}, streamErr(codeInvalidAuth, "not a JSON object")
	}
	tok, ok := memberString(m, "auth")
	if !ok || tok == "" {
		return authFrame{}, streamErr(codeInvalidAuth, "auth must be a non-empty string")
	}
	a := authFrame{Token: tok, Caps: parseCaps(m["caps"])}
	a.Origin, _ = memberString(m, "origin")
	if isNull(m["session"]) {
		return a, nil
	}
	var s map[string]json.RawMessage
	if json.Unmarshal(m["session"], &s) != nil || s == nil {
		return authFrame{}, streamErr(CodeBadRequest, "session must be an object")
	}
	sess := &sessionReq{}
	if !isNull(s["subscribe"]) {
		ps, err := parsePatterns(s["subscribe"], true)
		if err != nil {
			return authFrame{}, err
		}
		sess.Subscribe = ps
	}
	if !isNull(s["scope"]) {
		sc, err := parseScope(s["scope"])
		if err != nil {
			return authFrame{}, err
		}
		sess.Scope = sc
	}
	if !isNull(s["resume"]) {
		var r map[string]json.RawMessage
		_ = json.Unmarshal(s["resume"], &r)
		stream, okS := memberString(r, "stream")
		after, okA := parseOffsetToken(r["after"])
		if !okS || !validStreamID(stream) || !okA {
			return authFrame{}, streamErr(CodeBadRequest, "resume needs a stream id and an after offset")
		}
		sess.Resume = &resumeReq{Stream: stream, After: after}
	}
	a.Session = sess
	return a, nil
}

// encodeAuthFrame renders the dialer's first frame.
func encodeAuthFrame(token string, caps *streamCaps, origin string, s *sessionReq) []byte {
	if caps == nil {
		b, _ := json.Marshal(map[string]string{"auth": token})
		return b
	}
	out := map[string]any{"auth": token, "caps": *caps, "origin": origin}
	if s != nil {
		sess := map[string]any{}
		if s.Subscribe != nil {
			sess["subscribe"] = s.Subscribe
		}
		if s.Scope != nil {
			sess["scope"] = s.Scope
		}
		if s.Resume != nil {
			sess["resume"] = map[string]string{"stream": s.Resume.Stream, "after": formatOffset(s.Resume.After)}
		}
		out["session"] = sess
	}
	b, _ := json.Marshal(out)
	return b
}

type streamInfo struct {
	ID           string
	Oldest, Head uint64
	HeartbeatMs  int64
}

type resumeOutcome struct {
	Status, Reason string
	After, Head    uint64
}

// ackFrame is the acceptor's reply as the dialer reads it. Kind is
// auth_ok or session_error.
type ackFrame struct {
	Kind   string
	Caps   *streamCaps
	Origin string
	Stream *streamInfo
	Resume *resumeOutcome
	Err    *StreamError
}

func (a *ackFrame) streams() int {
	if a == nil || a.Caps == nil {
		return 0
	}
	return a.Caps.Streams
}

var errAckRejected = errors.New("bus: auth rejected")

// parseAck decodes the acceptor's reply. With caps.streams >= 1 the
// stream info and any resume outcome must be well formed.
func parseAck(data []byte) (ackFrame, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil || m == nil {
		return ackFrame{}, errAckRejected
	}
	typ, _ := memberString(m, "type")
	switch typ {
	case "session_error":
		code, _ := memberString(m, "code")
		msg, _ := memberString(m, "message")
		return ackFrame{Kind: typ, Err: &StreamError{Code: code, Message: msg}}, nil
	case "auth_ok":
	default:
		return ackFrame{}, errAckRejected
	}
	a := ackFrame{Kind: typ, Caps: parseCaps(m["caps"])}
	a.Origin, _ = memberString(m, "origin")
	if a.streams() < 1 {
		return a, nil
	}
	var st map[string]json.RawMessage
	_ = json.Unmarshal(m["stream"], &st)
	si := &streamInfo{}
	var okID, okO, okH bool
	si.ID, okID = memberString(st, "id")
	si.Oldest, okO = parseOffsetToken(st["oldest"])
	si.Head, okH = parseOffsetToken(st["head"])
	hb, err := strconv.ParseInt(string(st["heartbeat_ms"]), 10, 64)
	if !okID || !validStreamID(si.ID) || !okO || !okH || err != nil || hb < 1 {
		return ackFrame{}, protocolErr("ack: malformed stream info")
	}
	si.HeartbeatMs = hb
	a.Stream = si
	var sess map[string]json.RawMessage
	_ = json.Unmarshal(m["session"], &sess)
	if isNull(sess["resume"]) {
		return a, nil
	}
	var r map[string]json.RawMessage
	_ = json.Unmarshal(sess["resume"], &r)
	ro := &resumeOutcome{}
	var okSt, okA, okHd bool
	ro.Status, okSt = memberString(r, "status")
	ro.Reason, _ = memberString(r, "reason")
	ro.After, okA = parseOffsetToken(r["after"])
	ro.Head, okHd = parseOffsetToken(r["head"])
	switch {
	case !okSt || !okA || !okHd:
		return ackFrame{}, protocolErr("ack: malformed resume outcome")
	case ro.Status == "reset" && ro.Reason == "":
		return ackFrame{}, protocolErr("ack: reset without a reason")
	case ro.Status != "reset" && ro.Status != "replaying":
		return ackFrame{}, protocolErr("ack: unknown resume status %q", ro.Status)
	}
	a.Resume = ro
	return a, nil
}

// encodeAck renders the acceptor's reply. A dialer that sent no caps
// gets exactly {"type":"auth_ok"}.
func encodeAck(dialerCaps bool, caps streamCaps, origin string, si *streamInfo, ro *resumeOutcome) []byte {
	if !dialerCaps {
		return []byte(`{"type":"auth_ok"}`)
	}
	out := map[string]any{"type": "auth_ok", "caps": caps, "origin": origin}
	if si != nil {
		out["stream"] = map[string]any{
			"id": si.ID, "oldest": formatOffset(si.Oldest), "head": formatOffset(si.Head),
			"heartbeat_ms": si.HeartbeatMs,
		}
	}
	if ro != nil {
		r := map[string]string{"status": ro.Status, "after": formatOffset(ro.After), "head": formatOffset(ro.Head)}
		if ro.Reason != "" {
			r["reason"] = ro.Reason
		}
		out["session"] = map[string]any{"resume": r}
	}
	b, _ := json.Marshal(out)
	return b
}

func encodeSessionError(e *StreamError) []byte {
	b, _ := json.Marshal(map[string]string{"type": "session_error", "code": e.Code, "message": e.Message})
	return b
}

// negotiate returns the link's stream version and the format to send:
// the configured one when there was no handshake or the peer lists it,
// else kit.
func negotiate(mine streamCaps, peer *streamCaps, handshake bool, configured FrameFormat) (int, FrameFormat) {
	v := 0
	if peer != nil {
		v = min(mine.Streams, peer.Streams)
	}
	if !handshake || (peer != nil && peer.decodes(configured)) {
		return v, configured
	}
	return v, FrameKit
}
