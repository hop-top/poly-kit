package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

func projectedSpecRouter(t *testing.T) *api.Router {
	t.Helper()
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{
		Title: "Fixture", Version: "1.0.0",
	}))
	cfg := api.ProjectionConfig{
		Descriptors: fixtureDescriptors(),
		Executor:    &stubExecutor{},
		ToolName:    "fix",
	}
	api.MountCommandProjection(r, cfg)
	api.DescribeCommandProjection(r, cfg)
	return r
}

// fetchSpec returns the served OpenAPI document as a generic map.
func fetchSpec(t *testing.T, r *api.Router) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	require.Equal(t, http.StatusOK, rec.Code, "spec must be served")

	var doc map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	return doc
}

func TestOpenAPIContainsEachProjectedOperation(t *testing.T) {
	doc := fetchSpec(t, projectedSpecRouter(t))
	paths, ok := doc["paths"].(map[string]any)
	require.True(t, ok, "spec must carry paths")

	// Every invocable command appears at its method.
	for path, method := range map[string]string{
		"/v1/commands/list":          "get",
		"/v1/commands/widget/add":    "post",
		"/v1/commands/widget/delete": "post",
		"/v1/commands":               "get",
	} {
		entry, found := paths[path].(map[string]any)
		require.True(t, found, "spec must describe %s", path)
		assert.Contains(t, entry, method, "%s must be %s", path, method)
	}

	// Withheld commands are absent: the spec describes what can be
	// called, discovery describes what exists.
	for _, path := range []string{
		"/v1/commands/shell", "/v1/commands/nuke", "/v1/commands/secret",
	} {
		assert.NotContains(t, paths, path,
			"non-invocable command must not appear in the spec")
	}
}

func TestOpenAPIReadCommandUsesQueryParams(t *testing.T) {
	doc := fetchSpec(t, projectedSpecRouter(t))
	paths := doc["paths"].(map[string]any)
	op := paths["/v1/commands/list"].(map[string]any)["get"].(map[string]any)

	params, ok := op["parameters"].([]any)
	require.True(t, ok, "a read command must declare query parameters")

	byName := map[string]map[string]any{}
	for _, p := range params {
		pm := p.(map[string]any)
		byName[pm["name"].(string)] = pm
	}

	require.Contains(t, byName, "limit")
	assert.Equal(t, "query", byName["limit"]["in"])
	require.Contains(t, byName, "filter")
	assert.Equal(t, true, byName["filter"]["required"],
		"a required flag must be required in the spec")
	// `list` declares no positionals, so no arg parameter is
	// emitted: the projection describes what the command takes,
	// not a fixed envelope.
	assert.NotContains(t, byName, "arg")
}

func TestOpenAPIReadCommandWithArgsDeclaresArgParam(t *testing.T) {
	// A read command that DOES declare positionals carries them as
	// a repeated query parameter, since a GET has no body.
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{
		Title: "Fixture", Version: "1.0.0",
	}))
	cfg := api.ProjectionConfig{
		Descriptors: []api.CommandDescriptor{{
			Path:       []string{"show"},
			SideEffect: api.SideEffectRead,
			Args:       []api.CommandArg{{Name: "id", Required: true}},
			Invocable:  true,
		}},
		Executor: &stubExecutor{},
	}
	api.MountCommandProjection(r, cfg)
	api.DescribeCommandProjection(r, cfg)

	doc := fetchSpec(t, r)
	paths := doc["paths"].(map[string]any)
	op := paths["/v1/commands/show"].(map[string]any)["get"].(map[string]any)

	params := op["parameters"].([]any)
	found := false
	for _, p := range params {
		pm := p.(map[string]any)
		if pm["name"] == "arg" {
			found = true
			assert.Equal(t, "query", pm["in"])
			assert.Equal(t, true, pm["required"],
				"a required positional makes the arg parameter required")
		}
	}
	assert.True(t, found, "declared positionals must appear as arg")
}

func TestOpenAPIWriteCommandUsesRequestBody(t *testing.T) {
	doc := fetchSpec(t, projectedSpecRouter(t))
	paths := doc["paths"].(map[string]any)
	op := paths["/v1/commands/widget/add"].(map[string]any)["post"].(map[string]any)

	assert.Contains(t, op, "requestBody",
		"a write command must take a body, not query parameters")
}

func TestOpenAPIDestructiveOperationIsMarked(t *testing.T) {
	doc := fetchSpec(t, projectedSpecRouter(t))
	paths := doc["paths"].(map[string]any)
	op := paths["/v1/commands/widget/delete"].(map[string]any)["post"].(map[string]any)

	assert.Contains(t, op["summary"], "[destructive]",
		"danger must be visible in a generated client's method list")

	params, ok := op["parameters"].([]any)
	require.True(t, ok, "a confirmed command must declare the header")
	found := false
	for _, p := range params {
		pm := p.(map[string]any)
		if pm["name"] == api.ConfirmTokenHeader {
			found = true
			assert.Equal(t, "header", pm["in"])
			assert.Equal(t, true, pm["required"])
		}
	}
	assert.True(t, found, "confirmation header must be in the spec")
}

func TestOpenAPIOperationIDsAreStable(t *testing.T) {
	doc := fetchSpec(t, projectedSpecRouter(t))
	paths := doc["paths"].(map[string]any)

	op := paths["/v1/commands/widget/add"].(map[string]any)["post"].(map[string]any)
	assert.Equal(t, "commands_widget_add", op["operationId"])

	disc := paths["/v1/commands"].(map[string]any)["get"].(map[string]any)
	assert.Equal(t, "commands_discover", disc["operationId"])
}

func TestMinimalSpecServedWithoutWithOpenAPI(t *testing.T) {
	// Projection still mounts without WithOpenAPI, so something
	// must describe the routes it serves.
	r := api.NewRouter()
	cfg := api.ProjectionConfig{
		Descriptors: fixtureDescriptors(),
		Executor:    &stubExecutor{},
		ToolName:    "fix",
		ToolVersion: "2.0.0",
	}
	api.MountCommandProjection(r, cfg)
	api.DescribeCommandProjection(r, cfg) // no-op: no huma API
	api.MountMinimalProjectionSpec(r, cfg)

	doc := fetchSpec(t, r)
	assert.Equal(t, "3.1.0", doc["openapi"])

	info := doc["info"].(map[string]any)
	assert.Equal(t, "fix", info["title"])
	assert.Equal(t, "2.0.0", info["version"])

	paths := doc["paths"].(map[string]any)
	assert.Contains(t, paths, "/v1/commands/list")
	assert.Contains(t, paths, "/v1/commands/widget/add")
	assert.Contains(t, paths, "/v1/commands")
	assert.NotContains(t, paths, "/v1/commands/shell")
}

func TestMinimalSpecSkippedWhenHumaOwnsThePath(t *testing.T) {
	// Registering a second handler on the same pattern would panic
	// in ServeMux; the guard must hold.
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{
		Title: "Fixture", Version: "1.0.0",
	}))
	cfg := api.ProjectionConfig{
		Descriptors: fixtureDescriptors(),
		Executor:    &stubExecutor{},
	}
	assert.NotPanics(t, func() {
		api.MountCommandProjection(r, cfg)
		api.DescribeCommandProjection(r, cfg)
		api.MountMinimalProjectionSpec(r, cfg)
	})
}

func TestProjectionHonorsRouterAuthMiddleware(t *testing.T) {
	// Auth is the router's, not the projection's: projected routes
	// must sit behind exactly the same middleware as adopter routes.
	denied := api.NewRouter(api.WithMiddleware(
		api.Auth(func(r *http.Request) (any, error) {
			if r.Header.Get("Authorization") == "" {
				return nil, assertAuthErr{}
			}
			return "claims", nil
		}),
	))
	api.MountCommandProjection(denied, api.ProjectionConfig{
		Descriptors: fixtureDescriptors(),
		Executor:    &stubExecutor{},
	})

	// Unauthenticated: refused before the command runs.
	rec := httptest.NewRecorder()
	denied.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/commands/list?filter=x", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// Discovery is behind the same gate.
	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// Authenticated: passes through.
	req := httptest.NewRequest(http.MethodGet, "/v1/commands/list?filter=x", nil)
	req.Header.Set("Authorization", "Bearer x")
	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

type assertAuthErr struct{}

func (assertAuthErr) Error() string { return "unauthorized" }
