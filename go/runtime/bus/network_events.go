package bus

import (
	"context"
	"strings"
)

// Topics the network adapter publishes on the local bus. The adapter
// owns kit.runtime.stream.* and kit.runtime.link.*: they never enter a
// log, and inbound frames carrying them are dropped.
const (
	// TopicStreamInvalidated: the dialer missed events from a peer; mark
	// every live view fed by it stale and refetch. Payload: peer, stream,
	// reason, after, head (offsets as decimal strings).
	TopicStreamInvalidated Topic = "kit.runtime.stream.invalidated"
	// TopicLinkConnected: an outbound link came up. Payload: peer,
	// stream, resume, format.
	TopicLinkConnected Topic = "kit.runtime.link.connected"
	// TopicLinkDisconnected: an outbound link went down. Payload: peer,
	// final, code (when known).
	TopicLinkDisconnected Topic = "kit.runtime.link.disconnected"
)

// NetworkEventSource is the Source of events the adapter publishes.
const NetworkEventSource = "kit.bus.network"

func isReservedTopic(t Topic) bool {
	s := string(t)
	return strings.HasPrefix(s, "kit.runtime.stream.") || strings.HasPrefix(s, "kit.runtime.link.")
}

// deliveryKey marks a context as a delivery from the outbound link to
// an address: a request to that address from such a handler would wait
// for a reply only the delivering goroutine can read.
type deliveryKey struct{}

// publishLocal publishes an adapter event on the local bus, marked as
// network-originated so it is never forwarded. A non-empty from marks
// it as delivered by the link to that address.
func (n *NetworkAdapter) publishLocal(t Topic, payload map[string]any, from string) {
	ctx := context.WithValue(context.Background(), networkOriginKey, n.originID)
	if from != "" {
		ctx = context.WithValue(ctx, deliveryKey{}, from)
	}
	_ = n.bus.Publish(ctx, NewEvent(t, NetworkEventSource, payload))
}

// invalidate publishes the gap signal for addr; a relay also logs a gap
// marker so its own stream links learn of the hole.
func (n *NetworkAdapter) invalidate(addr string, g *gapInfo) {
	n.publishLocal(TopicStreamInvalidated, g.payload(addr), addr)
	if n.relay {
		n.appendMarker()
	}
}

func (n *NetworkAdapter) publishConnected(addr string, st LinkState) {
	n.publishLocal(TopicLinkConnected, map[string]any{
		"peer": addr, "stream": st.Stream, "resume": st.Resume, "format": st.Format.String(),
	}, addr)
}

func (n *NetworkAdapter) publishDisconnected(addr string, final bool, code string) {
	p := map[string]any{"peer": addr, "final": final}
	if code != "" {
		p["code"] = code
	}
	n.publishLocal(TopicLinkDisconnected, p, "")
}
