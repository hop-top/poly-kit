package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServiceConfigSourcesMarkAServiceOptionalFromTheEnvironment pins
// that services.<name>.optional resolves through the same layered
// sources as the other lifecycle keys.
func TestServiceConfigSourcesMarkAServiceOptionalFromTheEnvironment(t *testing.T) {
	isolateHome(t)
	t.Setenv("TOOL_SERVICES_SOCKET_ENABLED", "true")
	t.Setenv("TOOL_SERVICES_SOCKET_OPTIONAL", "true")
	r := authRoot(t, WithSocket(SocketConfig{Path: tmpSocket(t)}))
	require.NoError(t, r.loadServiceConfig())

	configs := serveConfigs(r.Viper, r.serveReg.Names(), nil, nil)
	applyServiceOptional(r.Viper, r.serveReg, configs)
	assert.True(t, configs[SocketServiceName].Optional)
}

// TestServiceOptionalDefaultsToRequired pins the default: a service
// that declares nothing and is configured with no optional key is
// required.
func TestServiceOptionalDefaultsToRequired(t *testing.T) {
	isolateHome(t)
	t.Setenv("TOOL_SERVICES_SOCKET_ENABLED", "true")
	r := authRoot(t, WithSocket(SocketConfig{Path: tmpSocket(t)}))
	require.NoError(t, r.loadServiceConfig())

	configs := serveConfigs(r.Viper, r.serveReg.Names(), nil, nil)
	applyServiceOptional(r.Viper, r.serveReg, configs)
	assert.False(t, configs[SocketServiceName].Optional)
}
