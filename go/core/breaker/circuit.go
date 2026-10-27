package breaker

import (
	"sync"
	"time"
)

// Clock is the time source of a Breaker's circuit: cooldown expiry and
// trip timestamps read it. WithClock swaps it, so tests and the
// breaker-v1 conformance runner drive cooldowns without sleeping.
type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// transition is one state change the circuit made; the breaker logs
// and publishes it after the circuit's lock is released.
type transition struct {
	from, to State
	manual   bool
}

// circuit is the kit-side circuit-breaker state machine: count-based
// consecutive-failure thresholding in Closed, a cooldown in Open, and
// a bounded number of probes in HalfOpen. All methods are safe for
// concurrent use.
type circuit struct {
	mu    sync.Mutex
	clock Clock

	failureThreshold uint
	successThreshold uint
	delay            time.Duration

	state    State
	openedAt time.Time
	// failures counts consecutive failures while Closed.
	failures uint
	// successes counts probe successes while HalfOpen.
	successes uint
	// permits is the number of probes HalfOpen may still admit.
	permits uint
}

func newCircuit(clock Clock, failureThreshold, successThreshold uint, delay time.Duration) *circuit {
	return &circuit{
		clock:            clock,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		delay:            delay,
		state:            Closed,
	}
}

// current returns the state, applying cooldown expiry first.
func (c *circuit) current() (State, []transition) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.expire()
	return c.state, out
}

// expire moves Open to HalfOpen once the cooldown has elapsed: the
// boundary is inclusive. Requires c.mu.
func (c *circuit) expire() []transition {
	if c.state == Open && c.clock.Now().Sub(c.openedAt) >= c.delay {
		return []transition{c.moveTo(HalfOpen, false)}
	}
	return nil
}

// release returns a probe permit taken by tryAcquire whose execution
// never ran (a later pre-flight check rejected it).
func (c *circuit) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == HalfOpen && c.permits < c.successThreshold {
		c.permits++
	}
}

// tryAcquire admits one execution. Open moves to HalfOpen once the
// cooldown has elapsed; HalfOpen admits up to successThreshold probes.
func (c *circuit) tryAcquire() (bool, []transition) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.expire()
	switch c.state {
	case Open:
		return false, out
	case HalfOpen:
		if c.permits == 0 {
			return false, out
		}
		c.permits--
	}
	return true, out
}

// record feeds one outcome to the state machine. While HalfOpen a
// failure re-opens, successThreshold successes close, and any other
// outcome returns its probe permit. Open ignores outcomes.
func (c *circuit) record(success bool) []transition {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.expire()
	switch c.state {
	case Closed:
		if success {
			c.failures = 0
			return out
		}
		c.failures++
		if c.failures >= c.failureThreshold {
			out = append(out, c.moveTo(Open, false))
		}
	case HalfOpen:
		if success {
			c.successes++
		}
		switch {
		case !success:
			out = append(out, c.moveTo(Open, false))
		case c.successes >= c.successThreshold:
			out = append(out, c.moveTo(Closed, false))
		case c.permits < c.successThreshold:
			c.permits++
		}
	}
	return out
}

// open forces Open; a no-op while Open within its cooldown. An Open
// whose cooldown elapsed goes through HalfOpen, restarting it.
func (c *circuit) open(manual bool) []transition {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.expire()
	if c.state == Open {
		return out
	}
	return append(out, c.moveTo(Open, manual))
}

// reset forces Closed with a fresh failure window, in every state.
// Emits a transition only when the state changes.
func (c *circuit) reset() []transition {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.expire()
	if c.state == Closed {
		c.failures = 0
		return out
	}
	return append(out, c.moveTo(Closed, true))
}

// moveTo enters next with fresh per-state counters. Requires c.mu.
func (c *circuit) moveTo(next State, manual bool) transition {
	t := transition{from: c.state, to: next, manual: manual}
	c.state = next
	switch next {
	case Closed:
		c.failures = 0
	case Open:
		c.openedAt = c.clock.Now()
	case HalfOpen:
		c.successes = 0
		c.permits = c.successThreshold
	}
	return t
}
