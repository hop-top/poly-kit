package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// userConfig writes the tool's user config file under the isolated
// XDG_CONFIG_HOME.
func userConfig(t *testing.T, yaml string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "tool")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o600))
}

// withConfigArgs sets the root's -c tokens the way the command line
// would.
func withConfigArgs(t *testing.T, r *Root, tokens ...string) {
	t.Helper()
	for _, tok := range tokens {
		require.NoError(t, r.Cmd.PersistentFlags().Set("config", tok))
	}
}

func TestServiceConfigSources(t *testing.T) {
	cases := []struct {
		name  string
		env   map[string]string
		file  string
		cargs []string
		want  int64
	}{
		{name: "kit default", want: api.DefaultMaxBodyBytes},
		{name: "environment", env: map[string]string{"TOOL_SERVICES_API_BODY_LIMIT_MAX_BYTES": "4096"}, want: 4096},
		{name: "-c key=value", cargs: []string{"services.api.body_limit.max_bytes=4096"}, want: 4096},
		{name: "user config file", file: "services:\n  api:\n    body_limit:\n      max_bytes: 4096\n", want: 4096},
		{name: "services.all from the environment", env: map[string]string{"TOOL_SERVICES_ALL_BODY_LIMIT_MAX_BYTES": "2048"}, want: 2048},
		{
			name: "environment beats the file",
			env:  map[string]string{"TOOL_SERVICES_API_BODY_LIMIT_MAX_BYTES": "4096"},
			file: "services:\n  api:\n    body_limit:\n      max_bytes: 1024\n",
			want: 4096,
		},
		{
			name:  "-c beats the environment",
			env:   map[string]string{"TOOL_SERVICES_API_BODY_LIMIT_MAX_BYTES": "4096"},
			cargs: []string{"services.api.body_limit.max_bytes=8192"},
			want:  8192,
		},
		{
			name:  "the service's key from the file beats services.all from -c",
			file:  "services:\n  api:\n    body_limit:\n      max_bytes: 1024\n",
			cargs: []string{"services.all.body_limit.max_bytes=8192"},
			want:  1024,
		},
		{
			name:  "keys merge across sources",
			file:  "services:\n  all:\n    body_limit:\n      max_bytes: 1024\n",
			cargs: []string{"services.api.body_limit.enabled=true"},
			want:  1024,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateHome(t)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if c.file != "" {
				userConfig(t, c.file)
			}
			r := authRoot(t, WithAPI(APIConfig{}))
			withConfigArgs(t, r, c.cargs...)
			require.NoError(t, r.loadServiceConfig())
			got, err := apiSvc(t, r).maxBodyBytes()
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestServiceConfigSourcesExtraFile(t *testing.T) {
	isolateHome(t)
	userConfig(t, "services:\n  api:\n    body_limit:\n      max_bytes: 1024\n")
	extra := filepath.Join(t.TempDir(), "extra.yaml")
	require.NoError(t, os.WriteFile(extra, []byte("services:\n  api:\n    body_limit:\n      max_bytes: 2048\n"), 0o600))
	r := authRoot(t, WithAPI(APIConfig{}))
	withConfigArgs(t, r, extra)
	require.NoError(t, r.loadServiceConfig())
	got, err := apiSvc(t, r).maxBodyBytes()
	require.NoError(t, err)
	assert.Equal(t, int64(2048), got, "a -c file wins over the discovered files")
}

func TestServiceConfigSourcesLeaveOtherKeysAlone(t *testing.T) {
	isolateHome(t)
	t.Setenv("TOOL_COLOR", "red")
	userConfig(t, "color: blue\nservices:\n  api:\n    addr: 127.0.0.1:9\n")
	r := authRoot(t, WithAPI(APIConfig{}))
	withConfigArgs(t, r, "color=green")
	require.NoError(t, r.loadServiceConfig())
	assert.False(t, r.Viper.IsSet("color"), "only services.* keys are layered")
	assert.Equal(t, "127.0.0.1:9", r.Viper.GetString("services.api.addr"))
}

func TestServiceConfigSourcesBadFileIsAUsageError(t *testing.T) {
	isolateHome(t)
	userConfig(t, "services: [unclosed\n")
	r := authRoot(t, WithAPI(APIConfig{}))
	oe := usageErr(t, r.loadServiceConfig())
	assert.Contains(t, oe.Message, "config.yaml")
}

func TestServiceConfigSourcesConfigureAServiceFromTheEnvironment(t *testing.T) {
	isolateHome(t)
	t.Setenv("TOOL_SERVICES_SOCKET_ENABLED", "true")
	t.Setenv("TOOL_SERVICES_SOCKET_READY_TIMEOUT", "7s")
	r := authRoot(t, WithSocket(SocketConfig{Path: tmpSocket(t)}))
	require.NoError(t, r.loadServiceConfig())
	cfg, configured := serveConfigs(r.Viper, r.serveReg.Names(), nil, nil)[SocketServiceName]
	require.True(t, configured, "a block set only by the environment configures the service")
	assert.True(t, cfg.Enabled)
	assert.Equal(t, 7*time.Second, cfg.ReadyTimeout)
}

func TestServeRefusesUnknownServiceKeysFromEverySource(t *testing.T) {
	cases := map[string]struct {
		env   map[string]string
		cargs []string
		file  string
		want  string
	}{
		"env, unknown key in a block": {
			env:  map[string]string{"TOOL_SERVICES_API_BODY_LIMIT_MAXBYTES": "5"},
			want: `services.api.body_limit.maxbytes: unknown key "maxbytes"`,
		},
		"-c, service key under services.all": {
			cargs: []string{"services.all.addr=0.0.0.0:1"},
			want:  "services.all.addr: not a middleware key",
		},
		"file, another service's block": {
			file: "services:\n  socket:\n    compression:\n      level: 9\n",
			want: `services.socket.compression.level: unknown key "level"`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			isolateHome(t)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if c.file != "" {
				userConfig(t, c.file)
			}
			r := authRoot(t, WithAPI(APIConfig{}))
			withConfigArgs(t, r, c.cargs...)
			oe := usageErr(t, runServeExpect(t, r, []string{"serve", "api", "--addr", "127.0.0.1:0"}, 2*time.Second))
			assert.Contains(t, oe.Message, c.want)
		})
	}
}

func TestServeAddrFlagBeatsTheConfigKey(t *testing.T) {
	isolateHome(t)
	t.Setenv("TOOL_SERVICES_API_ADDR", "0.0.0.0:0")
	r := authRoot(t, WithAPI(APIConfig{}))
	oe := usageErr(t, runServeExpect(t, r, []string{"serve", "api"}, 2*time.Second))
	assert.Contains(t, oe.Message, "not a loopback address", "the environment's address is in force")

	r = authRoot(t, WithAPI(APIConfig{}))
	base, stop := serveAPI(t, r, "--addr", "127.0.0.1:0")
	defer stop()
	assert.Contains(t, base, "127.0.0.1:", "--addr wins over services.api.addr for one run")
}
