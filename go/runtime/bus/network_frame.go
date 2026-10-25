package bus

import (
	"encoding/json"
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

// decodeFrame parses one inbound frame of either format and returns the
// event and its wire origin. A JSON object with a member named exactly
// "specversion" (any value, null included) is a CloudEvents frame;
// any other object is a kit frame. A CloudEvents frame whose kitorigin
// is not a string is rejected.
func decodeFrame(data []byte) (Event, string, error) {
	// Probe by exact key: struct decoding would fold case and treat
	// "SPECVERSION" as a CloudEvents frame.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return Event{}, "", err
	}
	if _, isCE := probe["specversion"]; !isCE {
		var msg networkMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return Event{}, "", err
		}
		return Event{
			ID:          msg.ID,
			Topic:       Topic(msg.Topic),
			Source:      msg.Source,
			Timestamp:   msg.Timestamp,
			Payload:     msg.Payload,
			WorkspaceID: msg.WorkspaceID,
		}, msg.Origin, nil
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
