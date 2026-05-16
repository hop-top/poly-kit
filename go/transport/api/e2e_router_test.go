package api_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

type e2eItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (i e2eItem) GetID() string { return i.ID }

func TestE2E_Router_HandleAndPathParam(t *testing.T) {
	r := api.NewRouter()

	r.Handle("GET", "/items/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := api.PathParam(req, "id")
		api.JSON(w, http.StatusOK, map[string]string{"id": id})
	})
	r.Handle("POST", "/items", func(w http.ResponseWriter, req *http.Request) {
		var item e2eItem
		if err := json.NewDecoder(req.Body).Decode(&item); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		api.JSON(w, http.StatusCreated, item)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	t.Run("GET with PathParam", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/items/abc")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var body map[string]string
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		assert.Equal(t, "abc", body["id"])
	})

	t.Run("POST creates item", func(t *testing.T) {
		resp, err := http.Post(srv.URL+"/items",
			"application/json",
			strings.NewReader(`{"id":"1","name":"test"}`))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var item e2eItem
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&item))
		assert.Equal(t, "1", item.ID)
		assert.Equal(t, "test", item.Name)
	})
}

func TestE2E_Router_GroupWithAuthMiddleware(t *testing.T) {
	authMW := api.Auth(func(r *http.Request) (any, error) {
		token := r.Header.Get("Authorization")
		if token != "Bearer valid" {
			return nil, errors.New("forbidden")
		}
		return map[string]string{"user": "admin"}, nil
	})

	r := api.NewRouter()

	// Public route
	r.Handle("GET", "/public", func(w http.ResponseWriter, req *http.Request) {
		api.JSON(w, http.StatusOK, map[string]string{"msg": "ok"})
	})

	// Admin group with auth
	admin := r.Group("/admin", authMW)
	admin.Handle("GET", "/dashboard", func(w http.ResponseWriter, req *http.Request) {
		claims := api.ClaimsFromContext(req.Context())
		api.JSON(w, http.StatusOK, claims)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	t.Run("public route no auth needed", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/public")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("admin without token returns 401", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/admin/dashboard")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("admin with valid token returns 200", func(t *testing.T) {
		req, _ := http.NewRequest("GET", srv.URL+"/admin/dashboard", nil)
		req.Header.Set("Authorization", "Bearer valid")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var claims map[string]string
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&claims))
		assert.Equal(t, "admin", claims["user"])
	})
}

func TestE2E_Router_Mount(t *testing.T) {
	r := api.NewRouter()

	sub := http.NewServeMux()
	sub.HandleFunc("GET /hello", func(w http.ResponseWriter, req *http.Request) {
		api.JSON(w, http.StatusOK, map[string]string{"msg": "mounted"})
	})

	r.Mount("/v2", sub)

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v2/hello")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "mounted", body["msg"])
}

func TestE2E_Router_404OnUnmatchedPath(t *testing.T) {
	r := api.NewRouter()
	r.Handle("GET", "/exists", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestE2E_Router_WrongMethod(t *testing.T) {
	r := api.NewRouter()
	r.Handle("POST", "/items", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	// GET on a POST-only route — Go 1.22+ ServeMux returns 405
	resp, err := http.Get(srv.URL + "/items")
	require.NoError(t, err)
	defer resp.Body.Close()

	// Go 1.22+ ServeMux returns 405 Method Not Allowed when the path matches
	// but the method doesn't. If this changes, adjust the assertion.
	body, _ := io.ReadAll(resp.Body)
	t.Logf("wrong method status=%d body=%q", resp.StatusCode, string(body))
	assert.Contains(t, []int{http.StatusMethodNotAllowed, http.StatusNotFound}, resp.StatusCode,
		"expected 405 or 404 for wrong method")
}
