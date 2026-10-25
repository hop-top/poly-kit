package bus

import (
	"encoding/json"
	"errors"
	"fmt"
)

// FrameFormat selects how a NetworkAdapter encodes outbound event frames.
// Inbound frames are accepted in either format regardless of this
// setting.
type FrameFormat int

const (
	// FrameKit is the original kit frame:
	// {topic, source, timestamp, payload, origin, workspace_id?}.
	// Default; every kit peer reads it.
	FrameKit FrameFormat = iota
	// FrameCloudEvents is a CloudEvents 1.0 structured-mode JSON document
	// (see [CloudEvent.MarshalJSON]), with the loop-prevention origin in
	// the "kitorigin" extension. Peers that predate CloudEvents support
	// cannot read it.
	FrameCloudEvents
)

// String returns "kit" or "cloudevents".
func (f FrameFormat) String() string {
	switch f {
	case FrameKit:
		return "kit"
	case FrameCloudEvents:
		return "cloudevents"
	default:
		return fmt.Sprintf("FrameFormat(%d)", int(f))
	}
}

// OriginExtension is the CloudEvents extension carrying the
// loop-prevention origin on FrameCloudEvents frames. The adapter sets it
// on send and strips it on receive, so it never reaches subscribers.
const OriginExtension = "kitorigin"

// WithFrameFormat selects the outbound frame format. Default FrameKit,
// in every kit SDK: there is no capability negotiation yet, so no peer
// may default to CloudEvents frames until peers advertise support.
// Switch a peer to FrameCloudEvents only once every peer it talks to
// decodes CloudEvents frames (this kit version or later); older peers
// misread them.
func WithFrameFormat(f FrameFormat) NetworkOption {
	return func(n *NetworkAdapter) { n.frames = f }
}

// encodeFrame renders e as one wire frame in format f, stamping origin
// for loop prevention.
func encodeFrame(f FrameFormat, e Event, origin string) ([]byte, error) {
	if f != FrameCloudEvents {
		return json.Marshal(networkMsg{
			ID:          e.ID,
			Topic:       string(e.Topic),
			Source:      e.Source,
			Timestamp:   e.Timestamp,
			Payload:     e.Payload,
			Origin:      origin,
			WorkspaceID: e.WorkspaceID,
		})
	}
	c, err := ToCloudEvent(e)
	if err != nil {
		return nil, err
	}
	if origin != "" {
		ext := make(map[string]any, len(c.Extensions)+1)
		for k, v := range c.Extensions {
			ext[k] = v
		}
		ext[OriginExtension] = origin
		c.Extensions = ext
	}
	return c.MarshalJSON()
}

// Frame kinds reported by frameKind.
const (
	frameKindKit         = "kit"
	frameKindCloudEvents = "cloudevents"
)

// errFrameNotObject rejects frames that are valid JSON but not an
// object (null, arrays, scalars).
var errFrameNotObject = errors.New("bus: frame is not a JSON object")

// frameKind classifies one inbound frame and returns its top-level
// members. A JSON object with a member named exactly "specversion"
// (any value, null included; no case folding) is a CloudEvents frame;
// any other object is a kit frame. Anything else is an error, and the
// frame is dropped.
func frameKind(data []byte) (string, map[string]json.RawMessage, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return "", nil, err
	}
	if members == nil {
		return "", nil, errFrameNotObject
	}
	if _, ok := members["specversion"]; ok {
		return frameKindCloudEvents, members, nil
	}
	return frameKindKit, members, nil
}

// decodeKitFrame builds an Event from kit-frame members, matching keys
// exactly (topic, source, timestamp, payload, origin, workspace_id,
// id); a case variant such as "Topic" is ignored. A null or absent
// member leaves its field zero; a member of the wrong JSON type drops
// the frame.
func decodeKitFrame(members map[string]json.RawMessage) (Event, string, error) {
	var (
		e      Event
		topic  string
		origin string
	)
	fields := []struct {
		key string
		dst any
	}{
		{"topic", &topic},
		{"source", &e.Source},
		{"timestamp", &e.Timestamp},
		{"payload", &e.Payload},
		{"origin", &origin},
		{"workspace_id", &e.WorkspaceID},
		{"id", &e.ID},
	}
	for _, f := range fields {
		raw, ok := members[f.key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(raw, f.dst); err != nil {
			return Event{}, "", err
		}
	}
	e.Topic = Topic(topic)
	return e, origin, nil
}

// decodeFrame parses one inbound frame of either format (see
// frameKind) and returns the event and its wire origin. A CloudEvents
// frame whose kitorigin is not a string is rejected.
func decodeFrame(data []byte) (Event, string, error) {
	kind, members, err := frameKind(data)
	if err != nil {
		return Event{}, "", err
	}
	if kind == frameKindKit {
		return decodeKitFrame(members)
	}
	var c CloudEvent
	if err := c.UnmarshalJSON(data); err != nil {
		return Event{}, "", err
	}
	var origin string
	if v, ok := c.Extensions[OriginExtension]; ok {
		s, isStr := v.(string)
		if !isStr {
			return Event{}, "", ceErr(OriginExtension, "must be a string")
		}
		origin = s
		delete(c.Extensions, OriginExtension)
		if len(c.Extensions) == 0 {
			c.Extensions = nil
		}
	}
	e, err := FromCloudEvent(c)
	return e, origin, err
}
