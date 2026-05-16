package api

import "context"

// BusSubscriber abstracts a message bus capable of topic subscriptions.
type BusSubscriber interface {
	Subscribe(ctx context.Context, topic string, handler func(topic string, payload any)) error
}

// BusAdapter bridges a BusSubscriber to a WebSocket Hub, forwarding
// bus events to connected WS clients.
type BusAdapter struct {
	hub *Hub
	sub BusSubscriber
}

// NewBusAdapter creates a BusAdapter that forwards events from sub to hub.
func NewBusAdapter(hub *Hub, sub BusSubscriber) *BusAdapter {
	return &BusAdapter{hub: hub, sub: sub}
}

// Bridge subscribes to each topic on the bus and forwards incoming
// events to the Hub for fan-out to matching WS clients.
func (a *BusAdapter) Bridge(ctx context.Context, topics ...string) error {
	for _, t := range topics {
		if err := a.sub.Subscribe(ctx, t, func(topic string, payload any) {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_ = a.hub.Publish(topic, payload)
		}); err != nil {
			return err
		}
	}
	return nil
}
