package cel_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/policy/cel"
)

func TestEvaluator_CompileAndEval(t *testing.T) {
	ev, err := cel.New()
	require.NoError(t, err)
	require.NoError(t, ev.Compile("p1", `principal.role == "admin"`))

	ok, err := ev.Eval("p1", map[string]any{
		"principal": map[string]any{"role": "admin"},
		"resource":  map[string]any{},
		"context":   map[string]any{},
		"payload":   map[string]any{},
	})
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = ev.Eval("p1", map[string]any{
		"principal": map[string]any{"role": "user"},
		"resource":  map[string]any{},
		"context":   map[string]any{},
		"payload":   map[string]any{},
	})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestEvaluator_CompileError(t *testing.T) {
	ev, err := cel.New()
	require.NoError(t, err)
	err = ev.Compile("bad", `??? not valid`)
	require.Error(t, err)
}

func TestEvaluator_EvalUnknownProgram(t *testing.T) {
	ev, err := cel.New()
	require.NoError(t, err)
	_, err = ev.Eval("missing", map[string]any{})
	require.Error(t, err)
}

func TestEvaluator_NonBoolReturn(t *testing.T) {
	ev, err := cel.New()
	require.NoError(t, err)
	require.NoError(t, ev.Compile("p1", `principal.role`))
	_, err = ev.Eval("p1", map[string]any{
		"principal": map[string]any{"role": "admin"},
		"resource":  map[string]any{},
		"context":   map[string]any{},
		"payload":   map[string]any{},
	})
	require.Error(t, err)
}
