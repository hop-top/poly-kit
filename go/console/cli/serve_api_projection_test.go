package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// projectionFixture builds a root whose commands exercise each
// projection rule. Internal test: it drives the api service's real
// handler, which is what proves projection is automatic rather than
// something a test wired up by hand.
func projectionFixture(t *testing.T) *Root {
	t.Helper()

	r := New(Config{Name: "fix", Version: "9.9.9", DisableValidate: true},
		WithAPI(APIConfig{Addr: ":0"}))

	read := &cobra.Command{
		Use:   "list",
		Short: "list things",
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = cmd.OutOrStdout().Write([]byte("listed"))
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	read.Flags().Int("limit", 10, "max rows")
	r.Cmd.AddCommand(read)

	write := &cobra.Command{
		Use:         "add",
		Short:       "add a thing",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{"kit/side-effect": "write-local"},
	}
	write.Flags().Bool("force", false, "skip checks")
	r.Cmd.AddCommand(write)

	// Destructive: reflected, but withheld from REST by the default
	// policy ceiling.
	r.Cmd.AddCommand(&cobra.Command{
		Use:         "purge",
		Short:       "purge the store",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{"kit/side-effect": "destructive-shared"},
	})

	// Interactive: cannot be served by a request/reply transport.
	r.Cmd.AddCommand(&cobra.Command{
		Use:         "shell",
		Short:       "interactive shell",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{"kit/side-effect": "interactive"},
	})

	// Hidden: not part of the supported surface.
	hidden := &cobra.Command{
		Use:         "internal",
		Short:       "internal",
		Hidden:      true,
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	r.Cmd.AddCommand(hidden)

	return r
}

// projectionHandler returns the api service's assembled handler.
func projectionHandler(t *testing.T, r *Root) http.Handler {
	t.Helper()
	svc, ok := r.serveReg.Lookup(APIServiceName)
	require.True(t, ok, "WithAPI must register the api service")
	a, ok := svc.(*apiService)
	require.True(t, ok)
	h, err := a.buildHandler(t.Context())
	require.NoError(t, err)
	return h
}

func TestProjectionMountsWithoutAdopterCode(t *testing.T) {
	// The whole point: WithAPI alone, no Expose, no MountREST.
	h := projectionHandler(t, projectionFixture(t))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands/list", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var res api.CommandResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, 0, res.ExitCode)
	assert.Contains(t, res.Stdout, "listed",
		"the projected route must actually run the command")
}

func TestProjectionMethodFollowsSideEffect(t *testing.T) {
	h := projectionHandler(t, projectionFixture(t))

	// Read is a GET.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands/list", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	// Write is a POST, and rejects GET.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands/add", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/commands/add",
		strings.NewReader(`{"flags":{"force":true}}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestProjectionWithholdsInteractiveAndDestructive(t *testing.T) {
	h := projectionHandler(t, projectionFixture(t))

	// Neither is mounted: withheld at mount, not refused per call.
	for _, path := range []string{
		"/v1/commands/shell", "/v1/commands/purge", "/v1/commands/internal",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, http.StatusNotFound, rec.Code, path)
	}
}

func TestProjectionDiscoveryCarriesReasons(t *testing.T) {
	h := projectionHandler(t, projectionFixture(t))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var doc api.DiscoveryDocument
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))

	byName := map[string]api.DiscoveryEntry{}
	for _, e := range doc.Commands {
		byName[e.Name] = e
	}

	require.Contains(t, byName, "list")
	assert.True(t, byName["list"].Invocable)

	// Every withheld command is still described, with a reason.
	for _, name := range []string{"shell", "purge", "internal"} {
		e, found := byName[name]
		require.True(t, found, "%s must still be described", name)
		assert.False(t, e.Invocable, name)
		assert.NotEmpty(t, e.Reason, "%s must say why", name)
	}

	assert.Equal(t, "interactive", byName["shell"].Reason)
	assert.Equal(t, "unauthorized-destructive", byName["purge"].Reason,
		"a destructive command refused by policy must say so")
	assert.Equal(t, "hidden-internal", byName["internal"].Reason)

	assert.Equal(t, "fix", doc.Tool)
	assert.Equal(t, "9.9.9", doc.Version)
}

func TestProjectionServesMinimalSpecWithoutOpenAPIConfig(t *testing.T) {
	// This fixture never set APIConfig.OpenAPI.
	h := projectionHandler(t, projectionFixture(t))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	paths := doc["paths"].(map[string]any)
	assert.Contains(t, paths, "/v1/commands/list")
	assert.NotContains(t, paths, "/v1/commands/shell")
}

func TestProjectionDescribesOperationsWhenOpenAPIConfigured(t *testing.T) {
	r := New(Config{Name: "fix", Version: "1.0.0", DisableValidate: true},
		WithAPI(APIConfig{
			Addr:    ":0",
			OpenAPI: &api.OpenAPIConfig{Title: "Fix", Version: "1.0.0"},
		}))
	read := &cobra.Command{
		Use:         "list",
		Short:       "list things",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	r.Cmd.AddCommand(read)

	h := projectionHandler(t, r)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	paths := doc["paths"].(map[string]any)
	require.Contains(t, paths, "/v1/commands/list")
	assert.Contains(t, paths["/v1/commands/list"].(map[string]any), "get")
}

func TestAdopterRoutesStillWork(t *testing.T) {
	// Projection is additive: an adopter's own routes keep working
	// exactly as before.
	r := New(Config{Name: "fix", Version: "1.0.0", DisableValidate: true},
		WithAPI(APIConfig{
			Addr: ":0",
			Handlers: func(router *api.Router) {
				router.Handle("GET", "/health",
					func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte("ok"))
					})
			},
		}))

	h := projectionHandler(t, r)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
}
