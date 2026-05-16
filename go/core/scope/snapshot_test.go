package scope_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/scope"
)

func TestSnapshot_DeepCopy(t *testing.T) {
	orig := scope.New().
		SetMode(scope.Warn).
		Allow("/tmp/**").
		Deny("/tmp/secret/**")
	cp := orig.Snapshot()

	// Mutating cp must not affect orig.
	cp.Allow("/etc/**").SetMode(scope.Strict)

	assert.Equal(t, scope.Warn, orig.Mode(), "original mode preserved")
	assert.Len(t, orig.Rules(), 2, "original rule count preserved")
	assert.Equal(t, scope.Strict, cp.Mode())
	assert.Len(t, cp.Rules(), 3)
}

func TestSnapshot_RulePatternsAreIndependent(t *testing.T) {
	orig := scope.New().Allow("/tmp/**")
	cp := orig.Snapshot()

	cpRules := cp.Rules()
	require.Len(t, cpRules, 1)
	cpRules[0].Patterns[0] = "mutated"

	origRules := orig.Rules()
	assert.Equal(t, scope.Pattern("/tmp/**"), origRules[0].Patterns[0],
		"defensive copy must shield original from mutation via returned slice")
}

func TestSetDefault_RestoresPrev(t *testing.T) {
	prev := scope.Default()
	custom := scope.New().Allow("/scratch/**")
	restore := scope.SetDefault(custom)

	assert.Same(t, custom, scope.Default(), "Default returns the swapped policy")

	restore()
	assert.Same(t, prev, scope.Default(), "restore reverts to prior policy")
}

func TestSetDefault_NestedSwap(t *testing.T) {
	prev := scope.Default()

	a := scope.New().Allow("/a/**")
	restoreA := scope.SetDefault(a)
	assert.Same(t, a, scope.Default())

	b := scope.New().Allow("/b/**")
	restoreB := scope.SetDefault(b)
	assert.Same(t, b, scope.Default())

	restoreB()
	assert.Same(t, a, scope.Default(), "nested restore goes back one level")

	restoreA()
	assert.Same(t, prev, scope.Default(), "outer restore goes back to original")
}

func TestSetDefault_ForTestIsolation(t *testing.T) {
	// Pattern documented in the README: snapshot, swap, mutate, restore on cleanup.
	restore := scope.SetDefault(scope.New().Allow("/scratch/**"))
	t.Cleanup(restore)

	dec, err := scope.Default().Check("/scratch/x", scope.Read)
	require.NoError(t, err)
	assert.Equal(t, scope.Allowed, dec)
}
