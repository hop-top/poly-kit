package api

import "context"

// EventPublisher publishes domain events. Consumers supply their own
// implementation (e.g. wrapping bus.Bus) so the api package stays
// independently importable with zero kit-internal dependencies.
type EventPublisher interface {
	Publish(ctx context.Context, topic, source string, payload any) error
}
