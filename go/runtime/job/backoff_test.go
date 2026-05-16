package job_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/runtime/job"
)

func TestBackoffCompute_ExponentialGrowth(t *testing.T) {
	b := job.BackoffStrategy{
		Initial: 1 * time.Second,
		Max:     1 * time.Hour,
		Factor:  2.0,
		Jitter:  0, // no jitter for deterministic test
	}

	assert.Equal(t, 1*time.Second, b.Compute(0))
	assert.Equal(t, 2*time.Second, b.Compute(1))
	assert.Equal(t, 4*time.Second, b.Compute(2))
	assert.Equal(t, 8*time.Second, b.Compute(3))
}

func TestBackoffCompute_MaxCap(t *testing.T) {
	b := job.BackoffStrategy{
		Initial: 1 * time.Second,
		Max:     10 * time.Second,
		Factor:  2.0,
		Jitter:  0,
	}

	// 2^10 = 1024s >> 10s cap
	got := b.Compute(10)
	assert.Equal(t, 10*time.Second, got)
}

func TestBackoffCompute_JitterRange(t *testing.T) {
	b := job.BackoffStrategy{
		Initial: 10 * time.Second,
		Max:     1 * time.Hour,
		Factor:  2.0,
		Jitter:  0.25,
	}

	base := 10 * time.Second // attempt 0
	lo := float64(base) * 0.75
	hi := float64(base) * 1.25

	// Run multiple times to verify jitter stays in range.
	for range 100 {
		got := b.Compute(0)
		assert.GreaterOrEqual(t, float64(got), lo,
			"below lower bound")
		assert.LessOrEqual(t, float64(got), hi,
			"above upper bound")
	}
}

func TestDefaultBackoff(t *testing.T) {
	d := job.DefaultBackoff()
	assert.Equal(t, 30*time.Second, d.Initial)
	assert.Equal(t, 15*time.Minute, d.Max)
	assert.Equal(t, 2.0, d.Factor)
	assert.Equal(t, 0.25, d.Jitter)
}

func TestBackoffCompute_ZeroValues_UsesDefaults(t *testing.T) {
	b := job.BackoffStrategy{} // all zero
	got := b.Compute(0)

	// Jitter=0 means no jitter; Initial defaults to 30s.
	assert.Equal(t, 30*time.Second, got)
}
