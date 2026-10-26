package service_test

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/internal/testhome"
	"hop.top/kit/go/console/cli/service"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
)

// keySources gives a root both ways of issuing credentials, each in a
// directory of the test's own.
func keySources(t *testing.T) []func(*cli.Root) {
	t.Helper()
	dir := t.TempDir()
	return []func(*cli.Root){
		cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(dir, "identity")}),
		service.WithAPIKeys(service.APIKeysConfig{Path: filepath.Join(dir, "keys", "apikeys.db")}),
	}
}

// tokenPaths are the token leaves a served caller could aim at.
var tokenPaths = [][]string{
	{"token", "key", "create"}, {"token", "key", "list"}, {"token", "key", "revoke"},
	{"token", "create"}, {"token", "verify"}, {"token", "decode"}, {"token", "claims"},
}

// TestTokenIsNeverServedOverREST pins the reserved-command protection
// on the api service: every token leaf is withheld from the projection,
// described as not invocable, and a call to one mints nothing.
func TestTokenIsNeverServedOverREST(t *testing.T) {
	testhome.Isolate(t)
	r := newServeRoot(t, append(keySources(t), service.WithAPI(service.APIConfig{Addr: "127.0.0.1:0"}))...)
	base, stop := serveAPIInBackground(t, r, []string{"serve", "api"})
	defer stop()

	resp, err := http.Get(base + "/v1/commands")
	require.NoError(t, err)
	var doc api.DiscoveryDocument
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))
	_ = resp.Body.Close()
	listed := 0
	for _, e := range doc.Commands {
		if e.Name == "token" || strings.HasPrefix(e.Name, "token ") {
			listed++
			assert.False(t, e.Invocable, "%s is invocable over REST", e.Name)
		}
	}
	assert.NotZero(t, listed, "the token leaves exist and are described as withheld")

	for _, p := range tokenPaths {
		url := base + "/v1/commands/" + strings.Join(p, "/")
		resp, err := http.Post(url, "application/json", strings.NewReader(`{"flags":{"sub":"mallory"}}`))
		require.NoError(t, err)
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.GreaterOrEqual(t, resp.StatusCode, 400, "%s answered %d: %s", url, resp.StatusCode, raw)
		assert.NotContains(t, string(raw), "kit_", "%s minted a key", url)
	}
}

// TestTokenIsNeverServedOverTheSocket pins the same on the socket, for
// a tool with no REST api at all: the key sources alone mount token,
// and it stays kit-reserved.
func TestTokenIsNeverServedOverTheSocket(t *testing.T) {
	testhome.Isolate(t)
	path := shortSocketPath(t)
	r := newServeRoot(t, append(keySources(t), service.WithSocket(service.SocketConfig{Path: path}))...)
	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	for _, p := range tokenPaths {
		resp := callSocket(t, path, socket.Request{Path: p, Flags: map[string]any{"sub": "mallory"}})
		assert.False(t, resp.Ok, "%v ran over the socket", p)
		if resp.Result != nil {
			assert.NotContains(t, resp.Result.Stdout, "kit_", "%v minted a key", p)
		}
	}
}

// TestAdoptersOwnTokenCommandIsStillServedOverTheSocket pins the other
// side of the socket's withholding: it covers kit's own token command
// only. A tool with no key source mounts none, and a token command of
// its own is the adopter's to serve.
func TestAdoptersOwnTokenCommandIsStillServedOverTheSocket(t *testing.T) {
	testhome.Isolate(t)
	path := shortSocketPath(t)
	r := newServeRoot(t, service.WithSocket(service.SocketConfig{Path: path}))
	require.False(t, r.IsReserved("token"), "no key source, no kit token command")
	own := &cobra.Command{Use: "token", Short: "the adopter's own"}
	own.AddCommand(&cobra.Command{
		Use:  "hello",
		RunE: func(cmd *cobra.Command, _ []string) error { cmd.Print("hi"); return nil },
	})
	r.Cmd.AddCommand(own)
	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	resp := callSocket(t, path, socket.Request{Path: []string{"token", "hello"}})
	require.True(t, resp.Ok, "%+v", resp.Error)
	assert.Equal(t, "hi", resp.Result.Stdout)
}

// managedSocketRoot is a socket-only tool carrying every kit-reserved
// verb that manages served state, plus status and one adopter command,
// with the socket counting calls per caller.
func managedSocketRoot(t *testing.T, opts ...func(*cli.Root)) (*cli.Root, string) {
	t.Helper()
	testhome.Isolate(t)
	path := shortSocketPath(t)
	all := append(keySources(t),
		service.WithSocket(service.SocketConfig{Path: path}),
		service.WithAuditCommand(), service.WithQuotaCommand(), cli.WithStatus(cli.StatusConfig{}))
	r := newServeRoot(t, append(all, opts...)...)
	r.Cmd.AddCommand(&cobra.Command{
		Use:  "ping",
		RunE: func(cmd *cobra.Command, _ []string) error { cmd.Print("pong"); return nil },
	})
	return r, path
}

// TestSocketWithholdsEveryManagementVerb pins, verb by verb, that no
// kit-reserved command managing served state runs over the socket:
// each is refused NOT_ENABLED before it runs.
func TestSocketWithholdsEveryManagementVerb(t *testing.T) {
	r, path := managedSocketRoot(t)
	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	for _, req := range []socket.Request{
		{Path: []string{"quota", "show"}},
		{Path: []string{"quota", "reset"}, Flags: map[string]any{"all": true}},
		{Path: []string{"audit", "verify"}},
		{Path: []string{"token", "key", "create"}, Flags: map[string]any{"sub": "mallory"}},
		{Path: []string{"token", "create"}, Flags: map[string]any{"sub": "mallory"}},
		{Path: []string{"token", "verify"}, Args: []string{"x.y.z"}},
	} {
		resp := callSocket(t, path, req)
		require.False(t, resp.Ok, "%v ran over the socket", req.Path)
		require.NotNil(t, resp.Error, "%v", req.Path)
		assert.Equal(t, socket.CodeNotEnabled, resp.Error.Code, "%v: %s", req.Path, resp.Error.Message)
	}
}

// TestSocketCallerCannotClearItsOwnQuota pins the consequence that
// matters: with a per-caller call limit on the socket, a caller that
// has used its calls cannot reset them over the socket and go on.
func TestSocketCallerCannotClearItsOwnQuota(t *testing.T) {
	r, path := managedSocketRoot(t)
	r.Viper.Set("services.socket.quota.ops", 3)
	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	ping := socket.Request{Path: []string{"ping"}}
	resp := callSocket(t, path, ping)
	require.True(t, resp.Ok, "%+v", resp.Error)
	reset := callSocket(t, path, socket.Request{Path: []string{"quota", "reset"}, Flags: map[string]any{"all": true}})
	require.False(t, reset.Ok, "quota reset ran over the socket")
	for i := 0; i < 2; i++ {
		resp = callSocket(t, path, ping)
		require.True(t, resp.Ok, "call %d: %+v", i+2, resp.Error)
	}
	resp = callSocket(t, path, ping)
	require.False(t, resp.Ok, "the limit still holds: the refused reset cleared nothing")
	assert.Equal(t, socket.CodeQuotaExceeded, resp.Error.Code)
}

// TestSocketServesOnlyIntrospectionFromTheReservedSet pins what the
// socket still serves of kit's reserved verbs — the tool describing
// itself — beside every adopter command, so a reserved verb added to
// kit later is withheld unless it is named here on purpose.
func TestSocketServesOnlyIntrospectionFromTheReservedSet(t *testing.T) {
	r, path := managedSocketRoot(t)
	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	svc, ok := service.ServeRegistry(r).Lookup(service.SocketServiceName)
	require.True(t, ok)
	bridged, ok := svc.(interface{ Bridge() *cmdsurface.Bridge })
	require.True(t, ok)
	b := bridged.Bridge()
	require.NotNil(t, b)

	servedReserved := map[string]bool{}
	adopter := false
	for _, leaf := range b.Leaves() {
		if !leaf.Enabled[cmdsurface.SurfaceSocket] {
			continue
		}
		word := leaf.Path[0]
		if r.IsReserved(word) {
			servedReserved[word] = true
		}
		if word == "ping" {
			adopter = true
		}
	}
	got := make([]string, 0, len(servedReserved))
	for w := range servedReserved {
		got = append(got, w)
	}
	sort.Strings(got)
	assert.Equal(t, []string{"status"}, got, "only introspection is served from the reserved set")
	assert.True(t, adopter, "adopter commands are served")
	for _, withheld := range []string{"audit", "quota", "token"} {
		assert.True(t, slices.Contains(r.ReservedSubcommands(), withheld), "%s is mounted", withheld)
	}

	resp := callSocket(t, path, socket.Request{Path: []string{"status"}})
	assert.True(t, resp.Ok, "status runs over the socket: %+v", resp.Error)
}
