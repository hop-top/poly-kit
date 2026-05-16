package breaker_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/breaker"
)

func TestList_SortedByName(t *testing.T) {
	names := []string{"z-test", "a-test", "m-test"}
	for _, n := range names {
		breaker.New(n)
	}
	t.Cleanup(func() {
		for _, n := range names {
			breaker.Unregister(n)
		}
	})

	got := breaker.List()
	require.GreaterOrEqual(t, len(got), 3)

	// extract just the ones we registered, in encounter order
	var ours []string
	for _, b := range got {
		switch b.Name() {
		case "a-test", "m-test", "z-test":
			ours = append(ours, b.Name())
		}
	}
	assert.Equal(t, []string{"a-test", "m-test", "z-test"}, ours)
}

func TestResetAll_ClosesEveryCircuit(t *testing.T) {
	names := []string{"reset-all-1", "reset-all-2"}
	bs := []breaker.Breaker{}
	for _, n := range names {
		bs = append(bs, breaker.New(n))
	}
	t.Cleanup(func() {
		for _, n := range names {
			breaker.Unregister(n)
		}
	})

	for _, b := range bs {
		b.Trip("setup")
		require.Equal(t, breaker.Open, b.State())
	}

	breaker.ResetAll()

	for _, b := range bs {
		assert.Equal(t, breaker.Closed, b.State())
	}
}

func TestSnapshot_ReturnsAllBreakerStats(t *testing.T) {
	const name = "snap-test"
	t.Cleanup(func() { breaker.Unregister(name) })

	b := breaker.New(name)
	b.Trip("snap-reason")

	snap := breaker.Snapshot()
	got, ok := snap[name]
	require.True(t, ok)
	assert.Equal(t, uint64(1), got.Trips)
	assert.Equal(t, "snap-reason", got.LastTripReason)
}

func TestUnregister_RemovesEntry(t *testing.T) {
	const name = "unreg-test"
	breaker.New(name)
	_, ok := breaker.Lookup(name)
	require.True(t, ok)

	breaker.Unregister(name)
	_, ok = breaker.Lookup(name)
	assert.False(t, ok)
}
