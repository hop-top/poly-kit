// Package bus provides pub/sub event delivery for CLI hooking.
//
// Events are published to topics; subscribers receive events matching
// their topic pattern. Wildcards follow MQTT conventions:
//   - `*` matches exactly one segment
//   - `#` matches zero or more trailing segments
//
// # Topic grammar
//
// Published topics have exactly four dot-separated segments:
//
//	[Source].[Category].[Object].[Action]
//	kit.runtime.entity.created
//	kit.config.snapshot_reload.failed
//
// Each segment matches `^[a-z][a-z0-9_]*$`; the trailing Action is
// past tense (`Validate` and `ValidateTopic` enforce both rules).
// The Object segment may carry an optional snake_case modifier
// joined with an underscore — see [TopicOf] and [ParseTopic].
//
// See ADR-0017 (docs/adr/0017-bus-topic-naming-and-qualifiers.md)
// for the full grammar and the rationale for keeping semantic
// qualifiers (reason / mechanism / property / circumstance) in
// the payload via [Qualifiers] rather than in the topic string.
//
// # Builder API
//
// Use [TopicOf] to build a validated topic without hand-concatenating
// segments:
//
//	t := bus.TopicOf("kit", "config", "snapshot").
//	    Mod("reload").
//	    Action("failed")
//	// t == "kit.config.snapshot_reload.failed"
//
// [ParseTopic] is the inverse and returns a builder + the action so
// callers can re-render or retarget.
//
// # Qualifiers convention
//
// Events that need to express why/how/with-what/during-what embed
// [Qualifiers] in the payload struct (anonymous OR named field):
//
//	type SnapshotReloadFailed struct {
//	    bus.Qualifiers
//	    SnapshotID string `json:"snapshot_id"`
//	}
//
// Subscribers read qualifiers generically via [QualifiersFrom]. An
// empty Qualifiers JSON-marshals to "{}" thanks to omitempty on
// every field. The Publish API does not change.
//
// # Adapters
//
// Bus supports pluggable adapters via [WithAdapter]:
//   - MemoryAdapter: in-process, goroutine-based async (default)
//   - SQLiteAdapter: cross-process via shared SQLite + polling
//   - NetworkAdapter: cross-machine via WebSocket
//
// # Network Adapter
//
// NetworkAdapter bridges local bus instances over WebSocket for
// cross-machine event delivery. Usage:
//
//	b := bus.New()
//	na := bus.NewNetworkAdapter(b,
//	    bus.WithOriginID("node-1"),
//	    bus.WithFilter(bus.TopicFilter{Allow: []string{"task.*"}}),
//	    bus.WithAuth(&bus.StaticTokenAuth{Token_: "secret"}),
//	)
//	na.Connect(ctx, "ws://peer:8080/bus")
//
// Or use the [WithNetwork] option for auto-connect on construction:
//
//	b := bus.New(bus.WithNetwork("ws://peer:8080/bus"))
//
// To accept inbound peers, mount the handler on an HTTP server:
//
//	http.Handle("/bus", na.Handler())
//
// Features:
//   - Exponential backoff reconnect (100ms base, 30s cap, jitter)
//   - Topic filtering: deny-first, then allow (glob patterns)
//   - Loop prevention via origin tracking
//   - Auth handshake (JWT or static token via [Authenticator])
//   - JSON text frames compatible with standard tooling
//
// # Payload type erasure (cross-process)
//
// [Event.Payload] is typed as `any`. Publishers pass any Go value;
// in-process subscribers receive that exact value. Cross-process
// subscribers (NetworkAdapter, SQLiteAdapter, dpkms hub) receive
// the JSON-decoded form — objects become map[string]any, arrays
// []any, numbers float64. The publisher's Go struct type is NOT
// preserved over the wire.
//
// Typed consumers SHOULD re-marshal-hop:
//
//	raw, _ := json.Marshal(e.Payload)
//	var p events.TaskCreatedPayload
//	_ = json.Unmarshal(raw, &p)
//
// Publishers SHOULD use payload structs that round-trip cleanly
// via encoding/json. See [Event] for details and the bus topics
// spec (tlc/docs/bus-topics-spec-0.1.md §4) for the catalog.
package bus
