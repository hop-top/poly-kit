package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/transport/api"
)

func TestWithCapabilities_ServesEndpoint(t *testing.T) {
	r := api.NewRouter(api.WithCapabilities("test-svc", "1.0.0"))
	r.Handle("GET", "/items", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Handle("POST", "/items", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	req := httptest.NewRequest("GET", "/capabilities", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var cs toolspec.CapabilitySet
	if err := json.Unmarshal(rec.Body.Bytes(), &cs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cs.ServiceName != "test-svc" {
		t.Errorf("service = %q, want test-svc", cs.ServiceName)
	}
	if cs.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", cs.Version)
	}
	// GET + POST /items grouped into one capability with 2 methods.
	if len(cs.Capabilities) != 1 {
		t.Fatalf("capabilities len = %d, want 1 (grouped by path)", len(cs.Capabilities))
	}
	if len(cs.Capabilities[0].Methods) != 2 {
		t.Fatalf("methods len = %d, want 2", len(cs.Capabilities[0].Methods))
	}
}

func TestWithCapabilities_Disabled(t *testing.T) {
	r := api.NewRouter() // no WithCapabilities
	r.Handle("GET", "/items", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/capabilities", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// Without capabilities enabled, 404 (or 405).
	if rec.Code == http.StatusOK {
		t.Fatal("expected non-200 when capabilities disabled")
	}
}

func TestMountResource_RegistersCapabilities(t *testing.T) {
	r := api.NewRouter(api.WithCapabilities("svc", "1.0.0"))

	// Dummy handler simulating a ResourceRouter.
	dummy := http.NewServeMux()
	dummy.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {})

	r.MountResource("/widgets", dummy, "create", "list", "get", "update", "delete")

	req := httptest.NewRequest("GET", "/capabilities", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var cs toolspec.CapabilitySet
	if err := json.Unmarshal(rec.Body.Bytes(), &cs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 5 CRUD ops → grouped by path: /widgets/ (POST,GET) and /widgets/{id} (GET,PUT,DELETE)
	if len(cs.Capabilities) != 2 {
		t.Fatalf("capabilities len = %d, want 2 (grouped by path)", len(cs.Capabilities))
	}

	// Verify all methods are present.
	allMethods := map[string]bool{}
	for _, c := range cs.Capabilities {
		if c.Type != "resource" {
			t.Errorf("type = %q, want resource", c.Type)
		}
		for _, m := range c.Methods {
			allMethods[m] = true
		}
	}
	for _, want := range []string{"POST", "GET", "PUT", "DELETE"} {
		if !allMethods[want] {
			t.Errorf("missing method %s", want)
		}
	}
}

func TestMountResource_NoCapabilities(t *testing.T) {
	// Without WithCapabilities, MountResource still works as plain Mount.
	r := api.NewRouter()
	dummy := http.NewServeMux()
	dummy.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.MountResource("/things", dummy, "list")

	req := httptest.NewRequest("GET", "/things/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestWithCapabilities_IncludesGroupRoutes(t *testing.T) {
	r := api.NewRouter(api.WithCapabilities("svc", "2.0.0"))
	g := r.Group("/api/v1")
	g.Handle("GET", "/users", func(w http.ResponseWriter, _ *http.Request) {})
	g.Handle("DELETE", "/users/{id}", func(w http.ResponseWriter, _ *http.Request) {})

	req := httptest.NewRequest("GET", "/capabilities", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var cs toolspec.CapabilitySet
	if err := json.Unmarshal(rec.Body.Bytes(), &cs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cs.Capabilities) != 2 {
		t.Fatalf("capabilities len = %d, want 2", len(cs.Capabilities))
	}
}
