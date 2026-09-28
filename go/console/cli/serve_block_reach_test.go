package cli_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// A setting no service would apply is refused at exit 2, not
// ignored: the HTTP server timeouts and auth.mode mtls under the
// socket, which has no HTTP listener, and the result cache under any
// service but api. What the socket does apply — timeouts.command —
// and services.all defaults are accepted.
func TestServeRefusesSettingsNoServiceApplies(t *testing.T) {
	cases := map[string]struct {
		key  string
		val  any
		want string
	}{
		"socket read_header": {"services.socket.timeouts.read_header", "5s", "services.socket.timeouts.read_header: the socket service has no HTTP listener"},
		"socket read":        {"services.socket.timeouts.read", "5s", "services.socket.timeouts.read: the socket service has no HTTP listener"},
		"socket write":       {"services.socket.timeouts.write", "5s", "services.socket.timeouts.write: the socket service has no HTTP listener"},
		"socket idle":        {"services.socket.timeouts.idle", "5s", "services.socket.timeouts.idle: the socket service has no HTTP listener"},
		"socket auth mtls":   {"services.socket.auth.mode", "mtls", `services.socket.auth.mode: "mtls" needs an HTTP listener`},
		"socket cache":       {"services.socket.cache.enabled", true, "services.socket.cache: only the api service applies cache"},
		"mcp cache":          {"services.mcp.cache.backend", "memory", "services.mcp.cache: only the api service applies cache"},
		"rpc cache":          {"services.rpc.cache.enabled", false, "services.rpc.cache: only the api service applies cache"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := socketRoot(t, cli.SocketConfig{Path: shortSocketPath(t)})
			r.Viper.Set(c.key, c.val)
			err := runServeArgs(t, r, []string{"serve", "socket"}, 2*time.Second)
			require.Error(t, err)
			var oe *output.Error
			require.ErrorAs(t, err, &oe)
			assert.Equal(t, 2, oe.ExitCode)
			assert.Contains(t, err.Error(), c.want)
		})
	}

	r := socketRoot(t, cli.SocketConfig{Path: shortSocketPath(t)})
	r.Viper.Set("services.socket.timeouts.command", "30s")
	r.Viper.Set("services.all.timeouts.read", "5s")
	r.Viper.Set("services.all.auth.mode", "mtls")
	r.Viper.Set("services.all.cache.enabled", true)
	err := runServeArgs(t, r, []string{"serve", "socket"}, 2*time.Second)
	assert.NoError(t, err, "a key the socket applies and shared defaults are accepted")
}
