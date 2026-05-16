package peer

import (
	"context"
	"errors"
)

// PeerInfo describes a discoverable peer on the network.
type PeerInfo struct {
	ID        string            // PublicKeyID fingerprint
	Name      string            // human-readable name
	Addrs     []string          // reachable addresses (host:port)
	PublicKey []byte            // PEM-encoded Ed25519 public key
	Metadata  map[string]string // custom metadata
}

// Validate checks that required fields are present.
func (p PeerInfo) Validate() error {
	if p.ID == "" {
		return errors.New("peer: ID is required")
	}
	if len(p.PublicKey) == 0 {
		return errors.New("peer: PublicKey is required")
	}
	if len(p.Addrs) == 0 {
		return errors.New("peer: at least one address is required")
	}
	return nil
}

// Discoverer finds peers on the network.
type Discoverer interface {
	Announce(ctx context.Context, self PeerInfo) error
	Browse(ctx context.Context) ([]PeerInfo, error)
	Stop() error
}
