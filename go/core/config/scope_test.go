package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/config"
)

func TestScopePath_System(t *testing.T) {
	opts := config.Options{SystemConfigPath: "/etc/tool/config.yaml"}
	p, err := config.ScopePath(opts, config.ScopeSystem)
	require.NoError(t, err)
	assert.Equal(t, "/etc/tool/config.yaml", p)
}

func TestScopePath_User(t *testing.T) {
	opts := config.Options{UserConfigPath: "/home/u/.config/tool/config.yaml"}
	p, err := config.ScopePath(opts, config.ScopeUser)
	require.NoError(t, err)
	assert.Equal(t, "/home/u/.config/tool/config.yaml", p)
}

func TestScopePath_Project(t *testing.T) {
	opts := config.Options{ProjectConfigPath: "/repo/.tool.yaml"}
	p, err := config.ScopePath(opts, config.ScopeProject)
	require.NoError(t, err)
	assert.Equal(t, "/repo/.tool.yaml", p)
}

func TestScopePath_EmptyPath(t *testing.T) {
	tests := []struct {
		name  string
		scope config.Scope
		opts  config.Options
	}{
		{"system", config.ScopeSystem, config.Options{}},
		{"user", config.ScopeUser, config.Options{}},
		{"project", config.ScopeProject, config.Options{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.ScopePath(tt.opts, tt.scope)
			assert.ErrorIs(t, err, config.ErrEmptyScope)
		})
	}
}

func TestScopePath_UnknownScope(t *testing.T) {
	opts := config.Options{
		SystemConfigPath:  "/etc/config",
		UserConfigPath:    "/home/config",
		ProjectConfigPath: "/repo/config",
	}
	_, err := config.ScopePath(opts, config.Scope(99))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown scope")
}
