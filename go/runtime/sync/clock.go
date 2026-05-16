package sync

import (
	"sync"
)

// Timestamp is a hybrid logical clock value combining wall time,
// a logical counter, and the originating node identifier.
type Timestamp struct {
	Physical int64  `json:"physical"` // wall clock (UnixNano)
	Logical  uint32 `json:"logical"`  // logical counter
	NodeID   string `json:"node_id"`  // originating node
}

// Before reports whether a precedes b in causal order.
func (a Timestamp) Before(b Timestamp) bool {
	if a.Physical != b.Physical {
		return a.Physical < b.Physical
	}
	if a.Logical != b.Logical {
		return a.Logical < b.Logical
	}
	return a.NodeID < b.NodeID
}

// Equal reports whether a and b represent the same timestamp.
func (a Timestamp) Equal(b Timestamp) bool {
	return a.Physical == b.Physical &&
		a.Logical == b.Logical &&
		a.NodeID == b.NodeID
}

// Clock is a hybrid logical clock bound to a specific node.
type Clock struct {
	nodeID string
	wall   WallClock
	mu     sync.Mutex
	last   Timestamp
}

// NewClock returns a Clock for the given node identifier backed by the
// process [System] wall clock.
func NewClock(nodeID string) *Clock {
	return NewClockWithWallClock(nodeID, System)
}

// NewClockWithWallClock returns a Clock for the given node identifier
// backed by the supplied [WallClock]. Pass [FixedClock] or
// [MockWallClock] in tests for deterministic physical times. A nil wall
// argument falls back to [System].
func NewClockWithWallClock(nodeID string, wall WallClock) *Clock {
	if wall == nil {
		wall = System
	}
	return &Clock{nodeID: nodeID, wall: wall}
}

// Now generates a new monotonically increasing timestamp.
func (c *Clock) Now() Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()

	wall := c.wall.WallTime().UnixNano()
	if wall > c.last.Physical {
		c.last = Timestamp{Physical: wall, Logical: 0, NodeID: c.nodeID}
	} else {
		c.last.Logical++
		c.last.NodeID = c.nodeID
	}
	return c.last
}

// Update merges a remote timestamp into the local clock state,
// returning the new local timestamp.
func (c *Clock) Update(remote Timestamp) Timestamp {
	c.mu.Lock()
	defer c.mu.Unlock()

	wall := c.wall.WallTime().UnixNano()
	maxPhys := wall
	if c.last.Physical > maxPhys {
		maxPhys = c.last.Physical
	}
	if remote.Physical > maxPhys {
		maxPhys = remote.Physical
	}

	var logical uint32
	switch {
	case maxPhys == c.last.Physical && maxPhys == remote.Physical:
		logical = c.last.Logical
		if remote.Logical > logical {
			logical = remote.Logical
		}
		logical++
	case maxPhys == c.last.Physical:
		logical = c.last.Logical + 1
	case maxPhys == remote.Physical:
		logical = remote.Logical + 1
	default:
		logical = 0
	}

	c.last = Timestamp{Physical: maxPhys, Logical: logical, NodeID: c.nodeID}
	return c.last
}
