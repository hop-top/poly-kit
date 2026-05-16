package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hop.top/kit/go/transport/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAPI_SpecServed(t *testing.T) {
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{
		Title:   "Test API",
		Version: "1.0.0",
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/openapi.json", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	ct := rec.Header().Get("Content-Type")
	assert.True(t, strings.Contains(ct, "application/json") ||
		strings.Contains(ct, "application/openapi+json"),
		"content type should be JSON variant, got: %s", ct)

	var spec map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&spec))
	assert.Equal(t, "3.1.0", spec["openapi"])

	info, ok := spec["info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Test API", info["title"])
	assert.Equal(t, "1.0.0", info["version"])
}

func TestOpenAPI_SpecNotServedByDefault(t *testing.T) {
	r := api.NewRouter()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/openapi.json", nil)
	r.ServeHTTP(rec, req)

	// Without OpenAPI enabled, 404.
	assert.NotEqual(t, http.StatusOK, rec.Code)
}

func TestOpenAPI_ResourceCRUDOperations(t *testing.T) {
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{
		Title:   "Widget API",
		Version: "0.1.0",
	}))

	svc := newMockService()
	h := api.ResourceRouter[widget](svc,
		api.WithPrefix[widget]("/widgets"),
		api.WithHumaAPI[widget](api.HumaAPI(r), "/api"),
	)
	r.Mount("/api", h)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/openapi.json", nil)
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var spec map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&spec))

	paths, ok := spec["paths"].(map[string]any)
	require.True(t, ok, "spec must have paths")

	// Resource should register /api/widgets/ and /api/widgets/{id}
	_, hasList := paths["/api/widgets/"]
	_, hasItem := paths["/api/widgets/{id}"]
	assert.True(t, hasList, "spec must include /api/widgets/")
	assert.True(t, hasItem, "spec must include /api/widgets/{id}")

	// Check CRUD methods on collection path.
	coll, _ := paths["/api/widgets/"].(map[string]any)
	assert.Contains(t, coll, "get", "list operation")
	assert.Contains(t, coll, "post", "create operation")

	// Check CRUD methods on item path.
	item, _ := paths["/api/widgets/{id}"].(map[string]any)
	assert.Contains(t, item, "get", "get operation")
	assert.Contains(t, item, "put", "update operation")
	assert.Contains(t, item, "delete", "delete operation")
}

func TestOpenAPI_ResourceRequestResponse(t *testing.T) {
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{
		Title:   "Widget API",
		Version: "0.1.0",
	}))

	svc := newMockService()
	h := api.ResourceRouter[widget](svc,
		api.WithPrefix[widget]("/widgets"),
		api.WithHumaAPI[widget](api.HumaAPI(r), "/api"),
	)
	r.Mount("/api", h)

	// Verify the resource still works via HTTP.
	srv := httptest.NewServer(r)
	defer srv.Close()

	// Create
	resp, err := http.Post(srv.URL+"/api/widgets/",
		"application/json",
		strings.NewReader(`{"name":"bolt"}`))
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// List
	resp, err = http.Get(srv.URL + "/api/widgets/")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Get
	resp, err = http.Get(srv.URL + "/api/widgets/new-1")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func TestOpenAPI_BackwardsCompatible(t *testing.T) {
	// Without WithHumaAPI, ResourceRouter works as before.
	svc := newMockService()
	h := api.ResourceRouter[widget](svc)

	body := `{"name":"compat"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var got widget
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "compat", got.Name)
}

func TestOpenAPI_HumaAPINilWithoutOption(t *testing.T) {
	r := api.NewRouter()
	assert.Nil(t, api.HumaAPI(r))
}

func TestOpenAPI_WithDescription(t *testing.T) {
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{
		Title:       "Desc API",
		Version:     "2.0.0",
		Description: "A test API with description",
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/openapi.json", nil)
	r.ServeHTTP(rec, req)

	var spec map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&spec))

	info := spec["info"].(map[string]any)
	assert.Equal(t, "A test API with description", info["description"])
}
