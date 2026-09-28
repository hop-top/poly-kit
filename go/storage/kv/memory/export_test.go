package memory

import "time"

// SetClock replaces the store's clock, for expiry tests.
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}
