package serve

import (
	"context"

	"hop.top/kit/go/runtime/bus"
)

// EventPayload is the body of every serve lifecycle event. The
// service identifier travels here rather than in the topic, so
// subscribers are not forced to re-bind when a tool gains a service
// (contract §"Surfaced events").
//
// Fields round-trip cleanly through encoding/json: Elapsed is
// milliseconds rather than a time.Duration, because a Duration
// marshals as an opaque integer nanosecond count that cross-process
// subscribers cannot interpret without knowing the unit.
type EventPayload struct {
	bus.Qualifiers

	// Service is the identifier of the service the event concerns.
	// Empty for supervisor-scoped events.
	Service string `json:"service,omitempty"`

	// Error is the failure text for a failed event, empty otherwise.
	// The reason a service failed belongs in the payload, never in
	// the topic.
	Error string `json:"error,omitempty"`

	// ElapsedMS is milliseconds since the supervisor began the run.
	ElapsedMS int64 `json:"elapsed_ms"`

	// Address is where the service is accepting work, when it has
	// one: a listen address, a socket path. Empty for a service with
	// no address and for every event but ready_reported.
	//
	// It is carried because the resolved address is the single most
	// useful thing an operator reads out of a startup trace, and for
	// a wildcard port (":0") it is not knowable from configuration.
	Address string `json:"address,omitempty"`
}

// Publisher is the narrow slice of hop.top/kit/go/runtime/bus.Bus the
// supervisor needs. Depending on the method rather than the concrete
// Bus keeps a tool that has not wired a bus from paying for one, and
// lets a test observe emissions without standing up a bus.
//
// A nil Publisher means events are not published; the log counterpart
// still runs, so a tool with no bus still produces an operator-legible
// startup trace (contract §"Surfaced events").
type Publisher interface {
	Publish(ctx context.Context, e bus.Event) error
}

// Logger is the narrow slice of a structured logger the supervisor
// needs. It matches the method set of the charmbracelet logger kit
// returns from hop.top/kit/go/console/log, so *log.Logger satisfies
// it without an adapter.
//
// A nil Logger silences the log counterpart of every event.
type Logger interface {
	Info(msg any, keyvals ...any)
	Error(msg any, keyvals ...any)
}

// emitter publishes one lifecycle transition to both surfaces: the
// bus (when a Publisher is wired) and the log (when a Logger is).
// Neither is required, and a publish failure never fails the
// lifecycle — an event sink is observability, not correctness.
type emitter struct {
	topics bus.TopicMap
	pub    Publisher
	log    Logger
	source string
	onErr  func(error)
}

// emit publishes the "<object>.<action>" topic with payload, and logs
// the same transition. A failed action logs at ERROR; everything else
// logs at INFO (contract §"Surfaced events").
func (e *emitter) emit(ctx context.Context, object, action string, payload EventPayload) {
	e.logEvent(object, action, payload)

	if e.pub == nil {
		return
	}
	topic, ok := e.topics[object+"."+action]
	if !ok {
		return
	}
	if err := e.pub.Publish(ctx, bus.NewEvent(topic, e.source, payload)); err != nil && e.onErr != nil {
		e.onErr(err)
	}
}

func (e *emitter) logEvent(object, action string, payload EventPayload) {
	if e.log == nil {
		return
	}
	kv := []any{"object", object, "elapsed_ms", payload.ElapsedMS}
	if payload.Service != "" {
		kv = append(kv, "service", payload.Service)
	}
	if payload.Address != "" {
		kv = append(kv, "address", payload.Address)
	}
	if payload.Reason != "" {
		kv = append(kv, "reason", payload.Reason)
	}
	if action == ActionFailed {
		if payload.Error != "" {
			kv = append(kv, "error", payload.Error)
		}
		e.log.Error("serve: "+action, kv...)
		return
	}
	e.log.Info("serve: "+action, kv...)
}
