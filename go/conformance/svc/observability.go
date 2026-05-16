package svc

import (
	"sync"
	"sync/atomic"
)

// Metrics is the in-process metrics sink. v1 ships a thin counters
// surface; the Prometheus client_golang exporter wires in via a
// follow-up that does not change this API.
//
// Names match the design §11 prom names (kit_conf_svc_*) so the
// follow-up exporter can publish them directly.
type Metrics struct {
	GradeRequestsTotal *Counter // labels: status, tier
	GradeDurationMS    *Histogram
	CassetteSizeBytes  *Histogram
	ScenariosLoaded    *Gauge
	JudgeCallsTotal    *Counter // labels: model, outcome
	JudgeTokensTotal   *Counter // labels: model, direction
	RateLimitHitsTotal *Counter // labels: window
	AuthFailuresTotal  *Counter // labels: reason
}

// NewMetrics returns a fresh in-process Metrics block.
func NewMetrics() *Metrics {
	return &Metrics{
		GradeRequestsTotal: NewCounter(),
		GradeDurationMS:    NewHistogram(),
		CassetteSizeBytes:  NewHistogram(),
		ScenariosLoaded:    NewGauge(),
		JudgeCallsTotal:    NewCounter(),
		JudgeTokensTotal:   NewCounter(),
		RateLimitHitsTotal: NewCounter(),
		AuthFailuresTotal:  NewCounter(),
	}
}

// Counter is a tagged counter. Labels are joined by '|' in the key
// space. Inc is concurrency-safe.
type Counter struct {
	mu   sync.RWMutex
	vals map[string]*atomic.Int64
}

// NewCounter constructs an empty Counter.
func NewCounter() *Counter { return &Counter{vals: make(map[string]*atomic.Int64)} }

// Inc adds 1 to the labeled cell.
func (c *Counter) Inc(labels ...string) { c.Add(1, labels...) }

// Add adds delta to the labeled cell.
func (c *Counter) Add(delta int64, labels ...string) {
	key := joinLabels(labels)
	c.mu.RLock()
	cell, ok := c.vals[key]
	c.mu.RUnlock()
	if !ok {
		c.mu.Lock()
		cell, ok = c.vals[key]
		if !ok {
			cell = new(atomic.Int64)
			c.vals[key] = cell
		}
		c.mu.Unlock()
	}
	cell.Add(delta)
}

// Snapshot returns a map of label-string → count for export.
func (c *Counter) Snapshot() map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]int64, len(c.vals))
	for k, v := range c.vals {
		out[k] = v.Load()
	}
	return out
}

// Gauge is a single int64 cell.
type Gauge struct {
	v atomic.Int64
}

// NewGauge constructs a Gauge initialized to 0.
func NewGauge() *Gauge { return &Gauge{} }

// Set replaces the gauge value.
func (g *Gauge) Set(v int64) { g.v.Store(v) }

// Get returns the current value.
func (g *Gauge) Get() int64 { return g.v.Load() }

// Histogram is a minimal stand-in for the eventual Prometheus
// histogram. It stores observation count + sum so the exporter can
// expose them; bucketing is deferred to the exporter.
type Histogram struct {
	mu    sync.Mutex
	count int64
	sum   float64
}

// NewHistogram constructs a Histogram.
func NewHistogram() *Histogram { return &Histogram{} }

// Observe records a sample.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	h.sum += v
}

// Snapshot returns (count, sum).
func (h *Histogram) Snapshot() (int64, float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count, h.sum
}

func joinLabels(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	out := labels[0]
	for _, l := range labels[1:] {
		out += "|" + l
	}
	return out
}
