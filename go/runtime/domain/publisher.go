package domain

import "context"

// EventPublisher publishes domain events. Implementations may use
// kit/bus or any other pub/sub mechanism.
type EventPublisher interface {
	// Publish sends an event. Returning an error vetoes the operation
	// (for pre-hooks). Topic and source are dot-separated strings.
	Publish(ctx context.Context, topic, source string, payload any) error
}
