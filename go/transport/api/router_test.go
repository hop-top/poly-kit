package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"hop.top/kit/go/transport/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouter_Handle(t *testing.T) {
	r := api.NewRouter()
	r.Handle("GET", "/hello", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/hello", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
}

func TestRouter_PathParam(t *testing.T) {
	r := api.NewRouter()
	var got string
	r.Handle("GET", "/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		got = api.PathParam(r, "id")
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/items/42", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "42", got)
}

func TestRouter_Group(t *testing.T) {
	r := api.NewRouter()
	g := r.Group("/api/v1")
	g.Handle("GET", "/users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("users"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "users", rec.Body.String())
}

func TestRouter_GroupMiddleware(t *testing.T) {
	var order []string
	mwA := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "A")
			next.ServeHTTP(w, r)
		})
	}
	mwB := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "B")
			next.ServeHTTP(w, r)
		})
	}

	r := api.NewRouter(api.WithMiddleware(mwA))
	g := r.Group("/sub", mwB)
	g.Handle("GET", "/x", func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "handler")
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/sub/x", nil)
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"A", "B", "handler"}, order)
}

func TestRouter_Mount(t *testing.T) {
	sub := http.NewServeMux()
	sub.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("alive"))
	})

	r := api.NewRouter()
	r.Mount("/ext", sub)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ext/health", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "alive", rec.Body.String())
}

func TestRouter_Mount_EmptyPrefix(t *testing.T) {
	sub := http.NewServeMux()
	sub.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})

	r := api.NewRouter()
	r.Mount("", sub)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ping", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "pong", rec.Body.String())
}

func TestRouter_MethodNotAllowed(t *testing.T) {
	r := api.NewRouter()
	r.Handle("POST", "/items", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/items", nil)
	r.ServeHTTP(rec, req)

	// ServeMux returns 405 for wrong method.
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestRouter_NotFound(t *testing.T) {
	r := api.NewRouter()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/nope", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}
