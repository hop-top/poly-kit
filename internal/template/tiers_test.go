package template_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/internal/template"
)

func TestLoadTiers_Missing(t *testing.T) {
	fsys := fstest.MapFS{}
	m, err := template.LoadTiers(fsys)
	require.NoError(t, err)
	assert.Empty(t, m)
}

func TestLoadTiers_Present(t *testing.T) {
	fsys := fstest.MapFS{
		"tiers.yaml": &fstest.MapFile{Data: []byte("files:\n  Makefile: [1, 2, 3, 4]\n  README.md: [4]\n")},
	}
	m, err := template.LoadTiers(fsys)
	require.NoError(t, err)
	assert.Equal(t, map[string][]int{
		"Makefile":  {1, 2, 3, 4},
		"README.md": {4},
	}, m)
}

func TestAppliesAtTier_Default(t *testing.T) {
	tierMap := map[string][]int{}
	assert.False(t, template.AppliesAtTier("unknown.txt", tierMap, 2))
}

func TestAppliesAtTier_Default_Tier4(t *testing.T) {
	tierMap := map[string][]int{}
	assert.True(t, template.AppliesAtTier("unknown.txt", tierMap, 4))
}

func TestAppliesAtTier_InMap(t *testing.T) {
	tierMap := map[string][]int{
		"Makefile": {1, 2, 3, 4},
	}
	assert.True(t, template.AppliesAtTier("Makefile", tierMap, 2))
}

func TestAppliesAtTier_Bootstrap(t *testing.T) {
	tierMap := map[string][]int{
		"README.md": {4},
	}
	// in-map: tier=0 bypasses filter even when file restricted to [4]
	assert.True(t, template.AppliesAtTier("README.md", tierMap, 0))
	// not-in-map: tier=0 bypasses default [4] filter too
	assert.True(t, template.AppliesAtTier("missing.txt", tierMap, 0))
}
