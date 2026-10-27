package service_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/internal/testhome"
	"hop.top/kit/go/console/cli/service"
	"hop.top/kit/go/core/upgrade"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/socket"
)

// migratePaths are the migrate leaves a served caller could aim at.
var migratePaths = [][]string{
	{"migrate", "status"}, {"migrate", "run"}, {"migrate", "rollback"}, {"migrate", "history"},
}

// mountMigrate mounts kit's migrate tree the way an adopter does:
// after cli.New, so the root's reserved names never saw it.
func mountMigrate(r *cli.Root) {
	r.Cmd.AddCommand(upgrade.MigrateCommand(upgrade.NewMigrator("test", "1.0.0"), r.Viper))
}

// TestMigrateCommandIsNeverServedOverREST pins that an adopter mounting
// kit's migrate tree does not put it on REST: no leaf is invocable, and
// a call to rollback, which would restore a backup over the live
// database, is refused before it runs.
func TestMigrateCommandIsNeverServedOverREST(t *testing.T) {
	testhome.Isolate(t)
	r := newServeRoot(t, service.WithAPI(service.APIConfig{Addr: "127.0.0.1:0"}))
	mountMigrate(r)
	base, stop := serveAPIInBackground(t, r, []string{"serve", "api"})
	defer stop()

	resp, err := http.Get(base + "/v1/commands")
	require.NoError(t, err)
	var doc api.DiscoveryDocument
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))
	_ = resp.Body.Close()
	listed := 0
	for _, e := range doc.Commands {
		if strings.HasPrefix(e.Name, "migrate ") {
			listed++
			assert.False(t, e.Invocable, "%s is invocable over REST", e.Name)
			assert.Equal(t, "management-only", e.Reason, e.Name)
		}
	}
	assert.Equal(t, len(migratePaths), listed)

	for _, p := range migratePaths {
		url := base + "/v1/commands/" + strings.Join(p, "/")
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req, err := http.NewRequest(method, url, strings.NewReader(`{}`))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			assert.GreaterOrEqual(t, resp.StatusCode, 400, "%s %s answered %d: %s", method, url, resp.StatusCode, raw)
		}
	}
}

// TestMigrateCommandIsNeverServedOverTheSocket pins the same on the
// socket, whose bridge admits reserved verbs by name for introspection.
func TestMigrateCommandIsNeverServedOverTheSocket(t *testing.T) {
	testhome.Isolate(t)
	path := shortSocketPath(t)
	r := newServeRoot(t, service.WithSocket(service.SocketConfig{Path: path}))
	mountMigrate(r)
	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	for _, p := range migratePaths {
		resp := callSocket(t, path, socket.Request{Path: p})
		require.False(t, resp.Ok, "%v ran over the socket", p)
		assert.Equal(t, socket.CodeNotEnabled, resp.Error.Code, "%v: %s", p, resp.Error.Message)
	}
}
