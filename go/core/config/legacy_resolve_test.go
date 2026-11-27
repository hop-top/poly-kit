package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

func TestEffectiveUserPath(t *testing.T) {
	canon, legacyJSON, legacyYAML := legacyEnv(t)
	opts := config.Options{
		UserConfigPath:        canon,
		LegacyUserConfigPaths: []string{legacyJSON, legacyYAML},
	}

	p, legacy, err := config.EffectiveUserPath(opts)
	require.NoError(t, err)
	assert.Equal(t, canon, p, "nothing exists: canonical")
	assert.False(t, legacy)

	writeFile(t, legacyYAML, "name: y\n")
	p, legacy, err = config.EffectiveUserPath(opts)
	require.NoError(t, err)
	assert.Equal(t, legacyYAML, p)
	assert.True(t, legacy)

	writeFile(t, legacyJSON, `{"name":"j"}`)
	p, _, err = config.EffectiveUserPath(opts)
	require.NoError(t, err)
	assert.Equal(t, legacyJSON, p, "first existing candidate wins")

	writeFile(t, canon, "name: c\n")
	p, legacy, err = config.EffectiveUserPath(opts)
	require.NoError(t, err)
	assert.Equal(t, canon, p, "canonical shadows legacy")
	assert.False(t, legacy)
}

func TestEffectiveUserPathErrors(t *testing.T) {
	canon, legacyJSON, legacyYAML := legacyEnv(t)
	require.NoError(t, os.MkdirAll(legacyJSON, 0o755))
	writeFile(t, legacyYAML, "name: y\n")
	_, _, err := config.EffectiveUserPath(config.Options{
		UserConfigPath:        canon,
		LegacyUserConfigPaths: []string{legacyJSON, legacyYAML},
	})
	require.Error(t, err, "directory candidate: error, no fall-through")
	assert.Contains(t, err.Error(), filepath.Base(legacyJSON))

	p, legacy, err := config.EffectiveUserPath(config.Options{UserConfigPath: canon})
	require.NoError(t, err)
	assert.Equal(t, canon, p)
	assert.False(t, legacy, "no legacy paths: untouched")
}
