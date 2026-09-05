package main

import (
	"context"
	"sync"
	"time"

	"hop.top/kit/go/console/serve"
)

// heartbeat is the adopter-owned service the fixture registers beside
// kit's api and socket. It does nothing but tick, which is enough to
// prove that a service registered through cli.WithService appears as
// a sibling in `serve --list`, starts under the same supervisor, and
// observes the same readiness and stop semantics.
type heartbeat struct {
	mu      sync.Mutex
	started bool
	ready   bool
	beats   int
}

func newHeartbeat() *heartbeat { return &heartbeat{} }

// Name is the stable service identifier: a CLI word (`serve
// heartbeat`), a config key segment (services.heartbeat.*), and a bus
// payload value.
func (h *heartbeat) Name() string { return "heartbeat" }

// Start reports ready at once — nothing here can fail — then ticks
// until the supervisor cancels it.
func (h *heartbeat) Start(ctx context.Context, ready func()) error {
	h.mu.Lock()
	h.started, h.ready = true, true
	h.mu.Unlock()
	ready()

	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			h.mu.Lock()
			h.beats++
			h.mu.Unlock()
		}
	}
}

func (h *heartbeat) Ready() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ready
}

func (h *heartbeat) Stop(context.Context) error {
	h.mu.Lock()
	h.ready = false
	h.mu.Unlock()
	return nil
}

func (h *heartbeat) wasStarted() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.started
}

var _ serve.Service = (*heartbeat)(nil)
