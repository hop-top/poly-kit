package peer

import (
	"context"
	"sync"
	"time"
)

// Mesh manages peer connections via discovery and trust.
type Mesh struct {
	self         PeerInfo
	trust        *TrustManager
	discoverer   Discoverer
	peers        map[string]*meshConn
	mu           sync.RWMutex
	onConnect    func(PeerInfo)
	onDisconnect func(PeerInfo)
	stopCh       chan struct{}
	stopOnce     sync.Once
}

type meshConn struct {
	info     PeerInfo
	lastSeen time.Time
}

// NewMesh creates a Mesh with the given components.
func NewMesh(self PeerInfo, trust *TrustManager, disc Discoverer) *Mesh {
	return &Mesh{
		self:       self,
		trust:      trust,
		discoverer: disc,
		peers:      make(map[string]*meshConn),
		stopCh:     make(chan struct{}),
	}
}

// Start begins the discover-connect loop. Blocks until ctx is canceled
// or Stop is called.
func (m *Mesh) Start(ctx context.Context) error {
	if err := m.discoverer.Announce(ctx, m.self); err != nil {
		return err
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	m.scan(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.stopCh:
			return nil
		case <-ticker.C:
			m.scan(ctx)
			m.prune()
		}
	}
}

// Stop signals the mesh to stop. Safe to call multiple times.
func (m *Mesh) Stop() error {
	var err error
	m.stopOnce.Do(func() {
		close(m.stopCh)
		err = m.discoverer.Stop()
	})
	return err
}

// Peers returns currently connected peer infos.
func (m *Mesh) Peers() []PeerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PeerInfo, 0, len(m.peers))
	for _, c := range m.peers {
		out = append(out, c.info)
	}
	return out
}

// OnConnect sets the callback for new peer connections.
func (m *Mesh) OnConnect(fn func(PeerInfo)) {
	m.mu.Lock()
	m.onConnect = fn
	m.mu.Unlock()
}

// OnDisconnect sets the callback for peer disconnections.
func (m *Mesh) OnDisconnect(fn func(PeerInfo)) {
	m.mu.Lock()
	m.onDisconnect = fn
	m.mu.Unlock()
}

func (m *Mesh) scan(ctx context.Context) {
	found, err := m.discoverer.Browse(ctx)
	if err != nil {
		return
	}
	for _, p := range found {
		if p.ID == m.self.ID {
			continue
		}
		// Skip blocked peers
		blocked, _ := m.trust.IsBlocked(p.ID)
		if blocked {
			continue
		}

		// TOFU (sets PendingTOFU for first-seen peers)
		if err := m.trust.AcceptTOFU(p); err != nil {
			continue
		}

		// Only connect to explicitly trusted peers
		trusted, _ := m.trust.IsTrusted(p.ID)
		if !trusted {
			continue
		}

		m.mu.Lock()
		_, exists := m.peers[p.ID]
		if !exists {
			m.peers[p.ID] = &meshConn{info: p, lastSeen: time.Now()}
			if m.onConnect != nil {
				m.onConnect(p)
			}
		} else {
			m.peers[p.ID].lastSeen = time.Now()
		}
		m.mu.Unlock()
	}
}

func (m *Mesh) prune() {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-30 * time.Second)
	for id, c := range m.peers {
		if c.lastSeen.Before(cutoff) {
			delete(m.peers, id)
			if m.onDisconnect != nil {
				m.onDisconnect(c.info)
			}
		}
	}
}
