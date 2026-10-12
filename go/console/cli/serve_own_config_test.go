package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The project file a stranger can drop in whatever directory the
// daemon starts from: it moves the socket and turns on an
// unauthenticated api on every interface.
const hostileServicesBlock = `services:
  socket:
    path: %q
  api:
    enabled: true
    addr: 0.0.0.0:0
    insecure_remote: true
`

// ownConfigFixture lays out the sources serve discovers on its own: a
// project file under the working directory (one of the kit project
// markers), the user file, and a <TOOL>_SERVICES_* variable. It
// returns the socket path the project file names.
func ownConfigFixture(t *testing.T, marker string) string {
	t.Helper()
	isolateHome(t)
	cwd := filepath.Join(os.Getenv("HOME"), "work", "proj")
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(cwd, marker)), 0o755))
	hostileSock := tmpSocket(t)
	block := []byte(fmt.Sprintf(hostileServicesBlock, hostileSock))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, marker), block, 0o600))
	t.Chdir(cwd)

	userConfig(t, "services:\n  api:\n    addr: 127.0.0.1:47181\n")
	t.Setenv("TOOL_SERVICES_API_INSECURE_REMOTE", "true")
	return hostileSock
}

// adopterLayers is what a tool's own loader leaves in the root's
// Viper: socket only, at its own path, the api off at its own address.
func adopterLayers(t *testing.T, r *Root, sock string) {
	t.Helper()
	require.NoError(t, r.Viper.MergeConfigMap(map[string]any{
		"services": map[string]any{
			"socket": map[string]any{"enabled": true, "path": sock},
			"api":    map[string]any{"enabled": false, "addr": "127.0.0.1:47182"},
		},
	}))
}

// serviceListArgs is the listing; serveAliasListArgs is the same
// listing through the deprecated root alias.
var (
	serviceListArgs    = []string{"service", "list"}
	serveAliasListArgs = []string{"serve", "--list"}
)

// listEnabled runs the listing argv names (`service list` or the
// deprecated `serve --list`) and returns each service's enabled state
// as the listing reports it.
func listEnabled(t *testing.T, r *Root, argv []string) map[string]bool {
	t.Helper()
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.SetArgs(append(append([]string{}, argv...), "--format", "json"))
	require.NoError(t, r.Execute(context.Background()))
	var rows []serveListRow
	require.NoError(t, json.Unmarshal(out.Bytes(), &rows), out.String())
	got := map[string]bool{}
	for _, row := range rows {
		got[row.Service] = row.Enabled
	}
	return got
}

// dryRunServices runs a bare `service start --dry-run` (the start path
// up to the point a service would bind) and returns the services it
// would start.
func dryRunServices(t *testing.T, r *Root) []string {
	t.Helper()
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.SetArgs([]string{"service", "start", "--dry-run", "--format", "json"})
	require.NoError(t, r.Execute(context.Background()))
	var plan struct {
		Args struct {
			Services []string `json:"services"`
		} `json:"args"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &plan), out.String())
	return plan.Args.Services
}

var projectMarkerFiles = []string{
	filepath.Join(".kit", "config.yaml"),
	".kit.yaml",
	"kit.yaml",
}

func TestOwnServiceConfigIgnoresServesOwnSources(t *testing.T) {
	for _, marker := range projectMarkerFiles {
		t.Run(marker, func(t *testing.T) {
			ownConfigFixture(t, marker)
			adopterSock := tmpSocket(t)

			for _, form := range []string{"service start", "service list", "serve --list"} {
				r := authRoot(t, WithAPI(APIConfig{}), WithSocket(SocketConfig{}), WithOwnServiceConfig())
				adopterLayers(t, r, adopterSock)

				switch form {
				case "service start":
					assert.Equal(t, []string{SocketServiceName}, dryRunServices(t, r),
						"service start starts what the tool's own layering says, socket only")
				case "service list":
					assert.Equal(t, map[string]bool{APIServiceName: false, SocketServiceName: true},
						listEnabled(t, r, serviceListArgs), "service list reads the tool's own layering")
				default:
					assert.Equal(t, map[string]bool{APIServiceName: false, SocketServiceName: true},
						listEnabled(t, r, serveAliasListArgs), "the deprecated alias reads the same layering")
				}
				assert.Equal(t, adopterSock, r.Viper.GetString("services.socket.path"),
					"%s: a project file in the working directory does not move the socket", form)
				assert.False(t, r.Viper.GetBool("services.api.enabled"),
					"%s: a project file does not enable the api", form)
				assert.Equal(t, "127.0.0.1:47182", r.Viper.GetString("services.api.addr"),
					"%s: neither the project file nor the user file is merged above the tool's layers", form)
				assert.False(t, r.Viper.GetBool("services.api.insecure_remote"),
					"%s: no <TOOL>_SERVICES_* variable is bound by kit", form)
			}
		})
	}
}

// TestServiceConfigSourcesDefaultStillDiscoversFiles pins the default:
// without WithOwnServiceConfig, service start and service list layer
// the discovered files and variables over whatever the tool put at the
// config-file rung.
func TestServiceConfigSourcesDefaultStillDiscoversFiles(t *testing.T) {
	for _, marker := range projectMarkerFiles {
		t.Run(marker, func(t *testing.T) {
			hostileSock := ownConfigFixture(t, marker)

			r := authRoot(t, WithAPI(APIConfig{}), WithSocket(SocketConfig{}))
			adopterLayers(t, r, tmpSocket(t))
			assert.ElementsMatch(t, []string{APIServiceName, SocketServiceName}, dryRunServices(t, r))
			assert.Equal(t, hostileSock, r.Viper.GetString("services.socket.path"))
			assert.True(t, r.Viper.GetBool("services.api.insecure_remote"))

			r = authRoot(t, WithAPI(APIConfig{}), WithSocket(SocketConfig{}))
			adopterLayers(t, r, tmpSocket(t))
			assert.Equal(t, map[string]bool{APIServiceName: true, SocketServiceName: true}, listEnabled(t, r, serviceListArgs))
		})
	}
}

// TestOwnServiceConfigUserFileNotRemerged isolates the user file: with
// no project file, the tool's address stands under the option and the
// user file's wins without it.
func TestOwnServiceConfigUserFileNotRemerged(t *testing.T) {
	isolateHome(t)
	t.Chdir(t.TempDir())
	userConfig(t, "services:\n  api:\n    addr: 127.0.0.1:47181\n")

	r := authRoot(t, WithAPI(APIConfig{}), WithOwnServiceConfig())
	adopterLayers(t, r, tmpSocket(t))
	require.NoError(t, r.loadServiceConfig())
	assert.Equal(t, "127.0.0.1:47182", r.Viper.GetString("services.api.addr"))

	r = authRoot(t, WithAPI(APIConfig{}))
	adopterLayers(t, r, tmpSocket(t))
	require.NoError(t, r.loadServiceConfig())
	assert.Equal(t, "127.0.0.1:47181", r.Viper.GetString("services.api.addr"),
		"default: the user file is layered over the config-file rung")
}
