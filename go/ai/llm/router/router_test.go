package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubRouter returns a fixed score.
type stubRouter struct {
	score float64
	err   error
}

func (s *stubRouter) Score(_ context.Context, _ string) (float64, error) {
	return s.score, s.err
}

func TestRoutingError(t *testing.T) {
	err := NewRoutingError("test message")
	assert.Equal(t, "routing: test message", err.Error())
}

func TestModelPair(t *testing.T) {
	mp := ModelPair{Strong: "gpt-4", Weak: "gpt-3.5"}
	assert.Equal(t, "gpt-4", mp.Strong)
	assert.Equal(t, "gpt-3.5", mp.Weak)
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	r := &stubRouter{score: 0.5}

	require.NoError(t, reg.Register("test", r))

	got, err := reg.Get("test")
	require.NoError(t, err)
	assert.Equal(t, r, got)
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	reg := NewRegistry()
	r := &stubRouter{score: 0.5}

	require.NoError(t, reg.Register("test", r))
	err := reg.Register("test", r)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestRegistry_GetUnknown(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Get("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown router")
}

func TestRegistry_Names(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("alpha", &stubRouter{})
	_ = reg.Register("beta", &stubRouter{})

	names := reg.Names()
	assert.Len(t, names, 2)
	assert.Contains(t, names, "alpha")
	assert.Contains(t, names, "beta")
}
