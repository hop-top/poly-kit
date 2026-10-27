package bus

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Stream error codes. They are contract; messages are not.
const (
	CodeBadRequest     = "bad_request"
	CodeInvalidPattern = "invalid_pattern"
	CodeInvalidScope   = "invalid_scope"
	CodeLimit          = "limit"
	CodeForbidden      = "forbidden"
	CodeUnknownType    = "unknown_type"
)

// codeInvalidAuth reports an unusable auth frame. It never goes on the
// wire: the acceptor closes with 1008, as before streams.
const codeInvalidAuth = "invalid_auth"

// ErrStream matches every *StreamError with errors.Is.
var ErrStream = errors.New("bus: stream error")

// ErrStreamsUnsupported is returned by subscription calls on a link
// that did not negotiate streams.
var ErrStreamsUnsupported = errors.New("bus: peer does not support streams")

// StreamError is a refused stream request: a control-frame error reply
// or a handshake session error.
type StreamError struct {
	Code    string
	Message string
}

func (e *StreamError) Error() string { return "bus: stream: " + e.Code + ": " + e.Message }

// Is reports whether target is [ErrStream].
func (e *StreamError) Is(target error) bool { return target == ErrStream }

func streamErr(code, format string, args ...any) *StreamError {
	return &StreamError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// errProtocol marks a frame that breaks the stream protocol; the link
// closes with 1002 and the dialer reconnects with resume.
var errProtocol = errors.New("bus: stream protocol error")

// Protocol error kinds. Vectors pin them, so a case cannot fail for the
// wrong reason; the message text is not contract.
const (
	pkOffset         = "offset"          // event frame offset missing or malformed
	pkStreamInfo     = "stream_info"     // ack stream id or offsets malformed
	pkOldest         = "oldest"          // ack oldest beyond head+1
	pkHeartbeat      = "heartbeat"       // ack heartbeat_ms missing or out of range
	pkResume         = "resume"          // ack resume outcome malformed
	pkUnrequested    = "unrequested"     // ack resume outcome without a request
	pkReplayAfter    = "replay_after"    // ack replay after differs from the request or exceeds the head
	pkResetHead      = "reset_head"      // reset head beyond the stream head, or below the position
	pkResetAfter     = "reset_after"     // reset after beyond its head
	pkResetMalformed = "reset_malformed" // reset frame members malformed
	pkPosMalformed   = "position_malformed"
	pkForeignStream  = "foreign_stream" // reset or position naming another stream
)

// protocolError is an errProtocol with a kind.
type protocolError struct{ kind, msg string }

func (e *protocolError) Error() string        { return errProtocol.Error() + ": " + e.msg }
func (e *protocolError) Is(target error) bool { return target == errProtocol }

func protocolErr(kind, format string, args ...any) error {
	return &protocolError{kind: kind, msg: fmt.Sprintf(format, args...)}
}

func isProtocolError(err error) bool { return errors.Is(err, errProtocol) }

// protocolKind returns the kind of a protocol error, "" for others.
func protocolKind(err error) string {
	var pe *protocolError
	if errors.As(err, &pe) {
		return pe.kind
	}
	return ""
}

// parseOffset parses an offset token: decimal digits, no sign, no
// leading zeros, at most 2^64-1.
func parseOffset(s string) (uint64, bool) {
	if s == "" || len(s) > 20 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseUint(s, 10, 64)
	return v, err == nil
}

func formatOffset(v uint64) string { return strconv.FormatUint(v, 10) }

// parseOffsetToken parses a raw JSON member that must be an offset
// token in a JSON string.
func parseOffsetToken(raw json.RawMessage) (uint64, bool) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return 0, false
	}
	return parseOffset(s)
}

const (
	maxPatternBytes  = 256
	maxRequestID     = 64
	maxStreamIDBytes = 128
)

// validatePattern checks a subscribe pattern: non-empty, at most 256
// bytes, non-empty segments, wildcards only as whole segments, '#' last.
func validatePattern(p string) *StreamError {
	if p == "" || len(p) > maxPatternBytes {
		return streamErr(CodeInvalidPattern, "pattern must be 1-%d bytes", maxPatternBytes)
	}
	segs := strings.Split(p, ".")
	for i, s := range segs {
		switch {
		case s == "":
			return streamErr(CodeInvalidPattern, "segment %d is empty", i+1)
		case s == "#" && i != len(segs)-1:
			return streamErr(CodeInvalidPattern, "segment %d: %q must be the last segment", i+1, "#")
		case s != "*" && s != "#" && strings.ContainsAny(s, "*#"):
			return streamErr(CodeInvalidPattern, "segment %d: wildcards must be whole segments", i+1)
		}
	}
	return nil
}

func validStreamID(s string) bool {
	if s == "" || len(s) > maxStreamIDBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// Scope limits a link to some workspaces. Events without a workspace id
// always pass; an empty list passes only those.
type Scope struct {
	Workspaces []string `json:"workspaces"`
}

func (s *Scope) allows(workspace string) bool {
	if s == nil || workspace == "" {
		return true
	}
	for _, w := range s.Workspaces {
		if w == workspace {
			return true
		}
	}
	return false
}

// Subscription is a link's effective subscription, as an authorizer
// sees it. Patterns is sorted; the implicit subscription is ["#"]. A nil
// Scope means all workspaces.
type Subscription struct {
	Patterns []string
	Scope    *Scope
}

// isNull reports whether a member is absent or JSON null.
func isNull(raw json.RawMessage) bool { return len(raw) == 0 || string(raw) == "null" }

// parseScope decodes a scope member: an object whose workspaces is an
// array of strings.
func parseScope(raw json.RawMessage) (*Scope, *StreamError) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, streamErr(CodeInvalidScope, "scope must be an object")
	}
	var ws []string
	if isNull(m["workspaces"]) || json.Unmarshal(m["workspaces"], &ws) != nil {
		return nil, streamErr(CodeInvalidScope, "workspaces must be an array of strings")
	}
	if ws == nil {
		ws = []string{}
	}
	return &Scope{Workspaces: ws}, nil
}

// parsePatterns decodes a patterns member and validates each pattern.
// allowEmpty admits [] (a handshake may subscribe to nothing).
func parsePatterns(raw json.RawMessage, allowEmpty bool) ([]string, *StreamError) {
	var ps []string
	if json.Unmarshal(raw, &ps) != nil || ps == nil {
		return nil, streamErr(CodeBadRequest, "patterns must be an array of strings")
	}
	if len(ps) == 0 && !allowEmpty {
		return nil, streamErr(CodeBadRequest, "patterns must not be empty")
	}
	for _, p := range ps {
		if err := validatePattern(p); err != nil {
			return nil, err
		}
	}
	return ps, nil
}

// Frame kinds for classifyFrame, in addition to the kit and CloudEvents
// kinds of frameKind.
const (
	frameKindDrop    = "drop"
	frameKindControl = "control"
)

// classifyFrame applies the stream classification: drop non-objects,
// `specversion` makes a CloudEvents event, a string `type` makes a
// control frame on stream links only, anything else is a kit event.
func classifyFrame(data []byte, streamLink bool) (string, map[string]json.RawMessage) {
	kind, members, err := frameKind(data)
	if err != nil {
		return frameKindDrop, nil
	}
	if kind == frameKindKit && streamLink {
		if raw, ok := members["type"]; ok && len(raw) > 0 && raw[0] == '"' {
			return frameKindControl, members
		}
	}
	return kind, members
}

func memberString(m map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := m[key]
	if !ok || len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// ctlRequest is a subscribe or unsubscribe request from a dialer.
// Patterns nil means absent; Scope nil means absent.
type ctlRequest struct {
	Type     string
	ID       string
	Patterns []string
	Scope    *Scope
}

// ctlError is an error reply; ID is empty when the request had no valid
// id.
type ctlError struct {
	ID string
	*StreamError
}

// acceptorControl handles a control frame received by an acceptor. It
// returns a request to apply, an error to send back, or neither (the
// frame is ignored).
func acceptorControl(m map[string]json.RawMessage) (*ctlRequest, *ctlError) {
	typ, _ := memberString(m, "type")
	id, idOK := memberString(m, "id")
	if !idOK || id == "" || len(id) > maxRequestID {
		id, idOK = "", false
	}
	switch typ {
	case "ack", "error", "puback":
		return nil, nil
	case "reset", "position":
		return nil, &ctlError{id, streamErr(CodeBadRequest, "%s is sent by acceptors only", typ)}
	case "subscribe", "unsubscribe":
	default:
		return nil, &ctlError{id, streamErr(CodeUnknownType, "unknown control frame type %q", typ)}
	}
	if !idOK {
		return nil, &ctlError{"", streamErr(CodeBadRequest, "id must be a string of 1-%d bytes", maxRequestID)}
	}
	r := &ctlRequest{Type: typ, ID: id}
	if !isNull(m["patterns"]) {
		ps, err := parsePatterns(m["patterns"], false)
		if err != nil {
			return nil, &ctlError{id, err}
		}
		r.Patterns = ps
	}
	if typ == "subscribe" && !isNull(m["scope"]) {
		s, err := parseScope(m["scope"])
		if err != nil {
			return nil, &ctlError{id, err}
		}
		r.Scope = s
	}
	if r.Patterns == nil && (typ == "unsubscribe" || r.Scope == nil) {
		return nil, &ctlError{id, streamErr(CodeBadRequest, "%s needs patterns", typ)}
	}
	return r, nil
}

type resetInfo struct {
	Stream, Reason string
	After, Head    uint64
}

type positionInfo struct {
	Stream string
	Offset uint64
}

// dialerCtl is a control frame as a dialer reads it. Kind is ack,
// error, reset, position or ignore.
type dialerCtl struct {
	Kind          string
	ID            string
	At            uint64
	Code, Message string
	Reset         resetInfo
	Position      positionInfo
}

// dialerControl handles a control frame received by a dialer. A
// malformed reset or position is a protocol error; anything else it
// cannot use is ignored.
func dialerControl(m map[string]json.RawMessage) (dialerCtl, error) {
	typ, _ := memberString(m, "type")
	ignore := dialerCtl{Kind: "ignore"}
	switch typ {
	case "ack":
		id, ok := memberString(m, "id")
		at, okAt := parseOffsetToken(m["at"])
		if !ok || !okAt {
			return ignore, nil
		}
		return dialerCtl{Kind: "ack", ID: id, At: at}, nil
	case "error":
		code, ok := memberString(m, "code")
		if !ok || code == "" {
			return ignore, nil
		}
		id, _ := memberString(m, "id")
		msg, _ := memberString(m, "message")
		return dialerCtl{Kind: "error", ID: id, Code: code, Message: msg}, nil
	case "reset":
		stream, okS := memberString(m, "stream")
		reason, okR := memberString(m, "reason")
		after, okA := parseOffsetToken(m["after"])
		head, okH := parseOffsetToken(m["head"])
		if !okS || !validStreamID(stream) || !okR || reason == "" || !okA || !okH {
			return dialerCtl{}, protocolErr(pkResetMalformed, "malformed reset")
		}
		return dialerCtl{Kind: "reset", Reset: resetInfo{stream, reason, after, head}}, nil
	case "position":
		stream, okS := memberString(m, "stream")
		off, okO := parseOffsetToken(m["offset"])
		if !okS || !validStreamID(stream) || !okO {
			return dialerCtl{}, protocolErr(pkPosMalformed, "malformed position")
		}
		return dialerCtl{Kind: "position", Position: positionInfo{stream, off}}, nil
	}
	return ignore, nil
}

// subState is a link's subscription set and scope. Implicit is the
// initial "#" that the first explicit subscription replaces.
type subState struct {
	Implicit bool
	Patterns map[string]struct{}
	Scope    *Scope
}

func newSubState() subState { return subState{Implicit: true} }

// withPatterns returns s with an explicit pattern set.
func (s subState) withPatterns(ps []string) subState {
	s.Implicit = false
	s.Patterns = make(map[string]struct{}, len(ps))
	for _, p := range ps {
		s.Patterns[p] = struct{}{}
	}
	return s
}

func (s subState) sortedPatterns() []string {
	out := make([]string, 0, len(s.Patterns))
	for p := range s.Patterns {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// effective is what an authorizer sees.
func (s subState) effective() Subscription {
	if s.Implicit {
		return Subscription{Patterns: []string{"#"}, Scope: s.Scope}
	}
	return Subscription{Patterns: s.sortedPatterns(), Scope: s.Scope}
}

// apply returns the state after r, or a limit error; all or nothing.
func (s subState) apply(r ctlRequest, max int) (subState, *StreamError) {
	next := subState{Implicit: s.Implicit, Scope: s.Scope, Patterns: make(map[string]struct{}, len(s.Patterns))}
	for p := range s.Patterns {
		next.Patterns[p] = struct{}{}
	}
	switch r.Type {
	case "subscribe":
		if r.Patterns != nil {
			if next.Implicit {
				next.Implicit = false
				next.Patterns = map[string]struct{}{}
			}
			for _, p := range r.Patterns {
				next.Patterns[p] = struct{}{}
			}
			if len(next.Patterns) > max {
				return s, streamErr(CodeLimit, "more than %d subscriptions", max)
			}
		}
		if r.Scope != nil {
			next.Scope = r.Scope
		}
	case "unsubscribe":
		if next.Implicit {
			next.Implicit = false
			next.Patterns = map[string]struct{}{"#": {}}
		}
		for _, p := range r.Patterns {
			delete(next.Patterns, p)
		}
	}
	return next, nil
}

func (s subState) matches(t Topic) bool {
	if s.Implicit {
		return true
	}
	for p := range s.Patterns {
		if t.Match(p) {
			return true
		}
	}
	return false
}

// entryDelivers is the per-link delivery predicate, before the
// adapter's link filter callback. Gap markers always pass.
func entryDelivers(s subState, peerOrigin string, e LogEntry) bool {
	if e.Marker {
		return true
	}
	if peerOrigin != "" && e.Origin == peerOrigin {
		return false
	}
	return s.matches(e.Event.Topic) && s.Scope.allows(e.Event.WorkspaceID)
}
