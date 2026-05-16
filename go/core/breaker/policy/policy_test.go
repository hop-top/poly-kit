package policy_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/failsafe-go/failsafe-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	bpolicy "hop.top/kit/go/core/breaker/policy"
)

func TestVolume_AllowsBelowMax(t *testing.T) {
	var count atomic.Int64
	count.Store(50)
	v := bpolicy.NewVolume[any]().
		WithMaxBytes(100).
		WithReader(count.Load).
		Build()

	exec := failsafe.With[any](v)
	err := exec.Run(func() error { return nil })
	assert.NoError(t, err)
}

func TestVolume_BlocksAtOrAboveMax(t *testing.T) {
	var count atomic.Int64
	count.Store(200)

	called := false
	v := bpolicy.NewVolume[any]().
		WithMaxBytes(100).
		WithReader(count.Load).
		OnExceeded(func(n int64) { called = true }).
		Build()

	exec := failsafe.With[any](v)
	err := exec.Run(func() error { return nil })
	require.Error(t, err)
	assert.ErrorIs(t, err, bpolicy.ErrThresholdExceeded)
	assert.True(t, called, "OnExceeded should fire")
}

func TestVolume_RequiresMaxAndReader(t *testing.T) {
	assert.Panics(t, func() {
		bpolicy.NewVolume[any]().Build()
	})
	assert.Panics(t, func() {
		bpolicy.NewVolume[any]().WithMaxBytes(10).Build()
	})
	assert.Panics(t, func() {
		var c atomic.Int64
		bpolicy.NewVolume[any]().WithReader(c.Load).Build()
	})
}

func TestCount_AllowsBelowMax(t *testing.T) {
	var c atomic.Int64
	c.Store(5)
	p := bpolicy.NewCount[any]().
		WithMaxOps(10).
		WithReader(c.Load).
		Build()

	exec := failsafe.With[any](p)
	err := exec.Run(func() error { return nil })
	assert.NoError(t, err)
}

func TestCount_BlocksAtOrAboveMax(t *testing.T) {
	var c atomic.Int64
	c.Store(10)
	p := bpolicy.NewCount[any]().
		WithMaxOps(10).
		WithReader(c.Load).
		Build()

	exec := failsafe.With[any](p)
	err := exec.Run(func() error { return nil })
	require.Error(t, err)
	assert.ErrorIs(t, err, bpolicy.ErrThresholdExceeded)
}

func TestThreshold_NotMistakenForOtherErrors(t *testing.T) {
	other := errors.New("not a threshold error")
	assert.NotErrorIs(t, other, bpolicy.ErrThresholdExceeded)
}
