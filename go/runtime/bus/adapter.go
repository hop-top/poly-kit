package bus

import "context"

// Adapter is the transport layer for the bus. Implementations handle
// event delivery and subscriber management. Delivery semantics
// (timing, error propagation, ordering) are adapter-specific.
// The default is MemoryAdapter.
type Adapter interface {
	// Publish delivers an event. Delivery timing and error
	// propagation are adapter-specific.
	Publish(ctx context.Context, e Event) error
	// Subscribe registers a handler. Delivery guarantees depend
	// on the adapter.
	Subscribe(pattern string, h Handler) Unsubscribe
	// SubscribeAsync registers an async handler. Delivery
	// guarantees depend on the adapter.
	SubscribeAsync(pattern string, h AsyncHandler) Unsubscribe
	// Close stops the adapter and waits for in-flight handlers.
	Close(ctx context.Context) error
}

// MemoryAdapter is an in-process adapter using goroutines for async
// delivery. It is the default when no adapter is specified.
type MemoryAdapter struct {
	bus *memBus
}

// NewMemoryAdapter returns a new MemoryAdapter.
func NewMemoryAdapter() *MemoryAdapter {
	return &MemoryAdapter{bus: &memBus{sem: make(chan struct{}, defaultMaxAsync)}}
}

func (m *MemoryAdapter) Publish(ctx context.Context, e Event) error {
	return m.bus.Publish(ctx, e)
}

func (m *MemoryAdapter) Subscribe(pattern string, h Handler) Unsubscribe {
	return m.bus.Subscribe(pattern, h)
}

func (m *MemoryAdapter) SubscribeAsync(pattern string, h AsyncHandler) Unsubscribe {
	return m.bus.SubscribeAsync(pattern, h)
}

func (m *MemoryAdapter) Close(ctx context.Context) error {
	return m.bus.Close(ctx)
}
