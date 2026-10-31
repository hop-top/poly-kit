package api

import (
	"strings"
	"sync"
)

// Per-connection subscription bounds. They mirror the Rust hub and the
// websocket constants of contracts/parity/api.json.
const (
	// maxSubscriptions caps patterns one connection holds.
	maxSubscriptions = 1000
	// maxPatternLen caps one pattern, in bytes.
	maxPatternLen = 256
	// maxPatternSegments caps dot-separated segments in a pattern.
	maxPatternSegments = 32
	// maxPatternDoubleStars caps `**` segments in a pattern: each
	// retries every suffix of the topic, so match cost grows with them.
	maxPatternDoubleStars = 3
	// maxSubscriptionBytes caps the bytes of all patterns on one
	// connection.
	maxSubscriptionBytes = 64 * 1024
)

// subscriptions is one connection's patterns and the bytes they hold.
// The connection's read loop mutates it while the hub loop matches
// broadcasts against it, so every access goes through mu.
type subscriptions struct {
	mu    sync.RWMutex
	set   map[string]bool
	bytes int
}

// add stores pattern unless a bound refuses it.
func (s *subscriptions) add(pattern string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.set[pattern] {
		return len(s.set) <= maxSubscriptions
	}
	if len(s.set) >= maxSubscriptions ||
		len(pattern) > maxPatternLen ||
		s.bytes+len(pattern) > maxSubscriptionBytes ||
		overSegmentBounds(pattern) {
		return false
	}
	if s.set == nil {
		s.set = make(map[string]bool)
	}
	s.bytes += len(pattern)
	s.set[pattern] = true
	return true
}

// remove drops pattern and releases its bytes.
func (s *subscriptions) remove(pattern string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.set[pattern] {
		delete(s.set, pattern)
		s.bytes -= len(pattern)
	}
}

// overSegmentBounds reports a pattern with too many segments or `**`.
func overSegmentBounds(pattern string) bool {
	stars := 0
	for _, seg := range strings.Split(pattern, ".") {
		if seg == "**" {
			stars++
		}
	}
	segs := strings.Count(pattern, ".") + 1
	return segs > maxPatternSegments || stars > maxPatternDoubleStars
}

// matches reports whether topic matches any held pattern.
func (s *subscriptions) matches(topic string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return matchesAny(topic, s.set)
}
