// bus.go is an in-process pub/sub backend that satisfies both
// cmdsurface.Subscriber and api.EventPublisher. It exists ONLY so the
// example is runnable in one process without a Kafka / NATS / Redis
// dependency. Real adopters replace this with their broker's
// subscriber + publisher; the bus surface on cmdsurface stays
// untouched.
package main

import (
	"context"
	"encoding/json"
	"sync"

	"hop.top/kit/go/transport/cmdsurface"
)

// exampleBus is a minimal pub/sub fan-out keyed by topic. Each
// Subscribe call appends a handler; each Publish call dispatches the
// payload (rendered as JSON) to every registered handler on the
// matching topic. Concurrent Subscribe / Publish / Cancel are guarded
// by a single mutex — the example optimizes for clarity, not throughput.
type exampleBus struct {
	mu     sync.Mutex
	subs   map[string][]busSub
	nextID int
}

// busSub is one registered subscription. id keys the cancel func so
// Subscribe's return value can target precisely this entry without
// disturbing siblings registered to the same topic.
type busSub struct {
	id      int
	handler func(cmdsurface.BusMessage) error
}

// newExampleBus returns an empty bus ready to accept subscribers.
func newExampleBus() *exampleBus {
	return &exampleBus{subs: make(map[string][]busSub)}
}

// Subscribe implements cmdsurface.Subscriber. handler is invoked
// synchronously on each delivered message; deliveries on different
// topics may proceed concurrently from separate Publish callers.
// The returned cancel function removes this subscription and is
// idempotent — repeated calls are no-ops.
func (eb *exampleBus) Subscribe(
	_ context.Context,
	topic string,
	handler func(cmdsurface.BusMessage) error,
) (func(), error) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.nextID++
	id := eb.nextID
	eb.subs[topic] = append(eb.subs[topic], busSub{id: id, handler: handler})
	cancel := func() {
		eb.mu.Lock()
		defer eb.mu.Unlock()
		list := eb.subs[topic]
		kept := list[:0]
		for _, s := range list {
			if s.id != id {
				kept = append(kept, s)
			}
		}
		eb.subs[topic] = kept
	}
	return cancel, nil
}

// Publish implements api.EventPublisher. Payload is JSON-marshaled
// once and dispatched to every subscriber on topic; handlers run
// sequentially. Errors returned by handlers are discarded — the
// example mirrors at-most-once semantics, which is what most adopters
// want when wiring synthetic test traffic.
func (eb *exampleBus) Publish(_ context.Context, topic, _ string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	eb.mu.Lock()
	handlers := make([]func(cmdsurface.BusMessage) error, 0, len(eb.subs[topic]))
	for _, s := range eb.subs[topic] {
		handlers = append(handlers, s.handler)
	}
	eb.mu.Unlock()

	msg := cmdsurface.BusMessage{Topic: topic, Payload: raw}
	for _, h := range handlers {
		_ = h(msg)
	}
	return nil
}
