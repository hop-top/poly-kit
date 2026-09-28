package cmdsurface

// TestingHoldSlots takes every in-flight slot of b's capacity gate, as
// if that many calls were running, and returns the function giving
// them back. External _test packages use it to make the next call
// queue or be refused without a runner that blocks.
func TestingHoldSlots(b *Bridge) (release func()) {
	c := b.capacity
	held := make([]*capTicket, 0, c.maxInflight)
	for range c.maxInflight {
		t, _ := c.take()
		if t == nil || t.queued() {
			panic("TestingHoldSlots: the gate is already busy")
		}
		held = append(held, t)
	}
	return func() {
		for _, t := range held {
			t.release()
		}
	}
}
