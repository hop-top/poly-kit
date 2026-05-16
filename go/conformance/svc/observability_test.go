package svc

import (
	"sync"
	"testing"
)

func TestCounter_IncAndSnapshot(t *testing.T) {
	c := NewCounter()
	c.Inc("ok", "1")
	c.Inc("ok", "1")
	c.Inc("err", "2")
	snap := c.Snapshot()
	if snap["ok|1"] != 2 || snap["err|2"] != 1 {
		t.Errorf("snapshot: %+v", snap)
	}
}

func TestCounter_Concurrent(t *testing.T) {
	c := NewCounter()
	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			c.Inc("k")
		}()
	}
	wg.Wait()
	if got := c.Snapshot()["k"]; got != n {
		t.Errorf("concurrent inc: got %d want %d", got, n)
	}
}

func TestGauge(t *testing.T) {
	g := NewGauge()
	g.Set(42)
	if g.Get() != 42 {
		t.Errorf("gauge: got %d", g.Get())
	}
}

func TestHistogram(t *testing.T) {
	h := NewHistogram()
	h.Observe(1.0)
	h.Observe(2.0)
	h.Observe(3.0)
	count, sum := h.Snapshot()
	if count != 3 || sum != 6.0 {
		t.Errorf("histogram: count=%d sum=%f", count, sum)
	}
}
