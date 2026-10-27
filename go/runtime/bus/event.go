package bus

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Topic is a dot-separated event path following [Source].[Category].[Object].[Action] notation.
//
// Examples:
//   - "crm.sales.deal.created"
//   - "app.support.ticket.escalated"
//   - "billing.finance.invoice.paid"
//
// This hierarchical naming ensures discoverability, filtering, and clear semantics:
//   - Source: system or domain originating the event (crm, app, billing)
//   - Category: logical grouping within the source (sales, support, finance)
//   - Object: entity type that changed (deal, ticket, invoice)
//   - Action: what happened to the object (created, escalated, paid)
type Topic string

// Match reports whether pattern matches topic t.
//
// Rules:
//   - Exact match: "llm.request" matches "llm.request"
//   - `*` matches one segment: "llm.*" matches "llm.request" but not "llm.request.start"
//   - `#` matches zero or more trailing segments: "llm.#" matches "llm", "llm.request", "llm.request.start"
func (t Topic) Match(pattern string) bool {
	tParts := strings.Split(string(t), ".")
	pParts := strings.Split(pattern, ".")
	return matchParts(tParts, pParts)
}

func matchParts(topic, pattern []string) bool {
	ti, pi := 0, 0
	for pi < len(pattern) {
		if pattern[pi] == "#" {
			// Per MQTT convention, # must be the last segment.
			if pi != len(pattern)-1 {
				return false
			}
			return true
		}
		if ti >= len(topic) {
			return false
		}
		if pattern[pi] != "*" && pattern[pi] != topic[ti] {
			return false
		}
		ti++
		pi++
	}
	return ti == len(topic)
}

// Event is the standard envelope for all bus messages.
//
// JSON keys are lowercase. Cross-process subscribers parse
// lowercase; capitalized keys would break them. v0.2 closes the gap
// alongside workspace_id.
type Event struct {
	// Topic identifies the event type (e.g. "llm.request").
	Topic Topic `json:"topic"`
	// Source identifies the emitter (e.g. "llm.client", "tool.exec").
	Source string `json:"source"`
	// Timestamp is when the event was created.
	Timestamp time.Time `json:"timestamp"`
	// Payload carries event-specific data.
	//
	// TYPE ERASURE WARNING:
	// Payload is `any` and crosses process boundaries via JSON
	// (NetworkAdapter, sqlite.Adapter, dpkms hub). In-process
	// subscribers receive the original Go value publishers passed
	// to NewEvent. Cross-process subscribers receive the JSON-
	// decoded form: objects → map[string]any, arrays → []any,
	// numbers → float64. The original Go struct type is NOT
	// preserved over the wire.
	//
	// Recommended pattern for typed consumers:
	//
	//	// re-marshal hop: map[string]any → typed struct
	//	raw, _ := json.Marshal(e.Payload)
	//	var p events.TaskCreatedPayload
	//	if err := json.Unmarshal(raw, &p); err != nil { ... }
	//
	// Publishers SHOULD use structs whose fields round-trip
	// cleanly via encoding/json (avoid time.Duration, channels,
	// funcs, unexported fields). See docs/contracts/event-topics.md
	// for the contract.
	Payload any `json:"payload"`
	// WorkspaceID scopes the event to a wsm workspace (ULID).
	// Empty = global event. Added in envelope v0.2.
	WorkspaceID string `json:"workspace_id,omitempty"`

	// The fields below complete the CloudEvents 1.0 mapping; see
	// [ToCloudEvent]. All are optional.

	// ID identifies the event; "evt_" TypeID minted by NewEvent.
	// Events built as literals get one when converted to a CloudEvent.
	ID string `json:"id,omitempty"`
	// Subject is the CloudEvents subject: what, within Source, the
	// event is about.
	Subject string `json:"subject,omitempty"`
	// DataContentType is the media type of Payload. When empty, a
	// CloudEvent gets application/json for a JSON payload and no
	// content type for a []byte payload.
	DataContentType string `json:"datacontenttype,omitempty"`
	// DataSchema is a URI identifying the schema Payload adheres to.
	DataSchema string `json:"dataschema,omitempty"`
	// Extensions holds CloudEvents extension attributes. Names are
	// lowercase ASCII letters and digits; values are string, bool or an
	// integer in int32 range. "workspaceid" is reserved (set
	// WorkspaceID instead), and so is "kitorigin" (owned by the network
	// adapter).
	Extensions map[string]any `json:"extensions,omitempty"`
}

// mintEventID returns a fresh "evt_" TypeID, or "" in the unreachable
// case that minting fails (the prefix is a valid constant); an empty
// ID is minted again on CloudEvents conversion.
func mintEventID() string {
	s, _ := newEventID()
	return s
}

// NewEvent creates an Event with a fresh id and the current timestamp.
func NewEvent(topic Topic, source string, payload any) Event {
	return Event{
		ID:        mintEventID(),
		Topic:     topic,
		Source:    source,
		Timestamp: time.Now(),
		Payload:   payload,
	}
}

// errTimestampOffset is returned when a decoded timestamp carries a zone
// offset time.Time cannot encode again: hours above 23 or minutes above
// 59 (time.Time.UnmarshalJSON accepts +24:00 and +23:60, then fails to
// marshal them).
var errTimestampOffset = errors.New("bus: event timestamp: invalid zone offset")

// UnmarshalJSON decodes an Event like encoding/json does for the plain
// struct, then rejects a timestamp whose offset is not hh:mm with hh
// 00-23 and mm 00-59.
func (e *Event) UnmarshalJSON(data []byte) error {
	type plain Event
	if err := json.Unmarshal(data, (*plain)(e)); err != nil {
		return err
	}
	var ts struct {
		Timestamp *string `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &ts); err != nil || ts.Timestamp == nil {
		return nil
	}
	if !validTimestampOffset(*ts.Timestamp) {
		return errTimestampOffset
	}
	return nil
}

// validTimestampOffset checks the trailing +hh:mm or -hh:mm of a
// timestamp time.Time already parsed. A trailing Z has no offset.
func validTimestampOffset(s string) bool {
	if len(s) < 6 {
		return true
	}
	z := s[len(s)-6:]
	if (z[0] != '+' && z[0] != '-') || z[3] != ':' {
		return true
	}
	h := int(z[1]-'0')*10 + int(z[2]-'0')
	m := int(z[4]-'0')*10 + int(z[5]-'0')
	return h <= 23 && m <= 59
}
