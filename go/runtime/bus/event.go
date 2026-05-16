package bus

import (
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
// JSON keys follow tlc/docs/bus-topics-spec-0.1.md §4 (lowercase).
// Cross-process subscribers (aps story 051, etc.) parse lowercase;
// capitalized keys would break them. v0.2 (T-0196) closes the gap
// alongside workspace_id (T-0194).
type Event struct {
	// Topic identifies the event type (e.g. "llm.request").
	Topic Topic `json:"topic"`
	// Source identifies the emitter (e.g. "llm.client", "tool.exec").
	Source string `json:"source"`
	// Timestamp is when the event was created.
	Timestamp time.Time `json:"timestamp"`
	// Payload carries event-specific data.
	//
	// TYPE ERASURE WARNING (T-0178):
	// Payload is `any` and crosses process boundaries via JSON
	// (NetworkAdapter, SQLiteAdapter, dpkms hub). In-process
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
	// funcs, unexported fields). See aps/docs/dev/event-topics.md
	// §3 and tlc/docs/bus-topics-spec-0.1.md §4 for the contract.
	Payload any `json:"payload"`
	// WorkspaceID scopes the event to a wsm workspace (ULID).
	// Empty = global event. Per bus-topics-spec-0.1 §4 v0.2.
	WorkspaceID string `json:"workspace_id,omitempty"`
}

// NewEvent creates an Event with the current timestamp.
func NewEvent(topic Topic, source string, payload any) Event {
	return Event{
		Topic:     topic,
		Source:    source,
		Timestamp: time.Now(),
		Payload:   payload,
	}
}
