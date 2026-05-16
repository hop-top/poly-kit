package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/transport/api"
)

func TestE2E_Capabilities_MultipleResources(t *testing.T) {
	r := api.NewRouter(api.WithCapabilities("myapp", "2.1.0"))

	// Mount two resource routers with different ops.
	widgetSvc := newE2EMockService()
	widgets := api.ResourceRouter[e2eItem](widgetSvc)
	r.MountResource("/api/widgets", widgets, "create", "list", "get", "update", "delete")

	gadgetSvc := newE2EMockService()
	gadgets := api.ResourceRouter[e2eItem](gadgetSvc,
		api.WithRouteFilter[e2eItem]("list", "get"),
	)
	r.MountResource("/api/gadgets", gadgets, "list", "get")

	// Also register a plain endpoint.
	r.Handle("GET", "/health", func(w http.ResponseWriter, _ *http.Request) {
		api.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// GET /capabilities
	req := httptest.NewRequest("GET", "/capabilities", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var cs toolspec.CapabilitySet
	err := json.Unmarshal(rec.Body.Bytes(), &cs)
	require.NoError(t, err)

	assert.Equal(t, "myapp", cs.ServiceName)
	assert.Equal(t, "2.1.0", cs.Version)

	// Expect: /health (endpoint), /api/widgets/ + /api/widgets/{id} (resource),
	// /api/gadgets/ + /api/gadgets/{id} (resource) = 5 grouped entries.
	assert.Equal(t, 5, len(cs.Capabilities),
		"expected 5 capability entries (1 endpoint + 2 widget paths + 2 gadget paths)")

	// Verify types.
	typeCount := map[string]int{}
	for _, c := range cs.Capabilities {
		typeCount[c.Type]++
	}
	assert.Equal(t, 1, typeCount["endpoint"])
	assert.Equal(t, 4, typeCount["resource"])

	// Verify widget paths have correct method counts.
	pathMethods := map[string][]string{}
	for _, c := range cs.Capabilities {
		pathMethods[c.Path] = c.Methods
	}

	// /api/widgets/ → POST, GET
	widgetBase := pathMethods["/api/widgets/"]
	sort.Strings(widgetBase)
	assert.Equal(t, []string{"GET", "POST"}, widgetBase)

	// /api/widgets/{id} → GET, PUT, DELETE
	widgetID := pathMethods["/api/widgets/{id}"]
	sort.Strings(widgetID)
	assert.Equal(t, []string{"DELETE", "GET", "PUT"}, widgetID)

	// /api/gadgets/ → GET only
	gadgetBase := pathMethods["/api/gadgets/"]
	assert.Equal(t, []string{"GET"}, gadgetBase)

	// /api/gadgets/{id} → GET only
	gadgetID := pathMethods["/api/gadgets/{id}"]
	assert.Equal(t, []string{"GET"}, gadgetID)
}

func TestE2E_Capabilities_EmptyRouter(t *testing.T) {
	r := api.NewRouter(api.WithCapabilities("empty", "0.0.1"))

	req := httptest.NewRequest("GET", "/capabilities", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var cs toolspec.CapabilitySet
	err := json.Unmarshal(rec.Body.Bytes(), &cs)
	require.NoError(t, err)

	assert.Equal(t, "empty", cs.ServiceName)
	assert.Empty(t, cs.Capabilities)
}
