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

// current returns the stored state.
func (c *circuit) current() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// tryAcquire admits one execution. Open moves to HalfOpen once the
// cooldown has elapsed; HalfOpen admits up to successThreshold probes.
func (c *circuit) tryAcquire() (bool, []transition) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []transition
	if c.state == Open {
		if c.clock.Now().Sub(c.openedAt) < c.delay {
			return false, nil
		}
		out = append(out, c.moveTo(HalfOpen, false))
	}
	if c.state == HalfOpen {
		if c.permits == 0 {
			return false, out
		}
		c.permits--
	}
	return true, out
}

// record feeds one outcome to the state machine and releases a probe
// permit while HalfOpen.
func (c *circuit) record(success bool) []transition {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []transition
	switch c.state {
	case Closed:
		if success {
			c.failures = 0
			return nil
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
		default:
			c.permits++
		}
	}
	return out
}

// open forces Open; a no-op when already Open.
func (c *circuit) open(manual bool) []transition {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == Open {
		return nil
	}
	return []transition{c.moveTo(Open, manual)}
}

// close forces Closed; a no-op when already Closed.
func (c *circuit) close() []transition {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == Closed {
		return nil
	}
	return []transition{c.moveTo(Closed, true)}
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
