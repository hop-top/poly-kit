package bus

import "context"

// Sink receives events for side-effect processing (logging, metrics, tracing).
// Sink errors never block publish or handler delivery.
type Sink interface {
	Drain(ctx context.Context, e Event) error
	Close() error
}

// TeeBus wraps a Bus and fans published events to a set of Sinks.
// Subscribe/SubscribeAsync delegate to the wrapped bus. Sink errors
// are reported via ErrFunc but never block the publisher.
type TeeBus struct {
	bus   Bus
	sinks []Sink
	onErr ErrFunc
}

// NewTeeBus returns a TeeBus wrapping bus, fanning to the given sinks.
// The optional onErr receives sink errors; nil errors are silently dropped.
func NewTeeBus(bus Bus, sinks []Sink, onErr ...ErrFunc) *TeeBus {
	t := &TeeBus{bus: bus, sinks: sinks}
	if len(onErr) > 0 && onErr[0] != nil {
		t.onErr = onErr[0]
	}
	return t
}

// Publish delivers the event to the wrapped bus first, then fans
// the event to all sinks. Sink errors are logged via ErrFunc and
// do not affect the publish result.
func (t *TeeBus) Publish(ctx context.Context, e Event) error {
	if err := t.bus.Publish(ctx, e); err != nil {
		return err
	}
	for _, s := range t.sinks {
		if err := s.Drain(ctx, e); err != nil && t.onErr != nil {
			t.onErr(err)
		}
	}
	return nil
}

// Subscribe delegates to the wrapped bus.
func (t *TeeBus) Subscribe(pattern string, h Handler) Unsubscribe {
	return t.bus.Subscribe(pattern, h)
}

// SubscribeAsync delegates to the wrapped bus.
func (t *TeeBus) SubscribeAsync(pattern string, h AsyncHandler) Unsubscribe {
	return t.bus.SubscribeAsync(pattern, h)
}

// Close closes the wrapped bus, then closes all sinks. The first
// error encountered is returned; remaining sinks are still closed.
func (t *TeeBus) Close(ctx context.Context) error {
	busErr := t.bus.Close(ctx)
	var firstErr error
	for _, s := range t.sinks {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if busErr != nil {
		return busErr
	}
	return firstErr
}
