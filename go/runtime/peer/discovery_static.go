package peer

import "context"

// StaticDiscoverer returns a fixed set of peers. Useful for testing.
type StaticDiscoverer struct {
	Peers []PeerInfo
}

func (s *StaticDiscoverer) Announce(_ context.Context, _ PeerInfo) error { return nil }
func (s *StaticDiscoverer) Browse(_ context.Context) ([]PeerInfo, error) {
	return s.Peers, nil
}
func (s *StaticDiscoverer) Stop() error { return nil }
