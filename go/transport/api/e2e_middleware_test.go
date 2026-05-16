package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

func TestE2E_Middleware_ChainOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string

	makeMW := func(name string) api.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				order = append(order, name+"-before")
				mu.Unlock()
				next.ServeHTTP(w, r)
				mu.Lock()
				order = append(order, name+"-after")
				mu.Unlock()
			})
		}
	}

	chained := api.Chain(makeMW("mw1"), makeMW("mw2"))
	handler := chained(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []string{"mw1-before", "mw2-before", "mw2-after", "mw1-after"}, order)
}

func TestE2E_Middleware_Logger(t *testing.T) {
	var mu sync.Mutex
	var captured []any

	logFn := func(msg any, keyvals ...any) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, msg)
		captured = append(captured, keyvals...)
	}

	r := api.NewRouter(api.WithMiddleware(api.Logger(logFn)))
	r.Handle("GET", "/test", func(w http.ResponseWriter, req *http.Request) {
		api.JSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test")
	require.NoError(t, err)
	resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()

	// Logger emits: "http request", "method", <m>, "path", <p>, "status", <s>, "duration", <d>
	require.True(t, len(captured) >= 9, "expected at least 9 captured values, got %d", len(captured))
	assert.Equal(t, "http request", captured[0])
	assert.Equal(t, "method", captured[1])
	assert.Equal(t, "GET", captured[2])
	assert.Equal(t, "path", captured[3])
	assert.Equal(t, "/test", captured[4])
	assert.Equal(t, "status", captured[5])
	assert.Equal(t, 200, captured[6])
	assert.Equal(t, "duration", captured[7])
	// captured[8] is duration string like "0ms"
	dur, ok := captured[8].(string)
	assert.True(t, ok, "duration should be string")
	assert.True(t, strings.HasSuffix(dur, "ms"), "duration should end with ms")
}

func TestE2E_Middleware_Recovery(t *testing.T) {
	var mu sync.Mutex
	var recovered any

	r := api.NewRouter(api.WithMiddleware(
		api.Recovery(func(v any, req *http.Request) {
			mu.Lock()
			recovered = v
			mu.Unlock()
		}),
	))
	r.Handle("GET", "/panic", func(w http.ResponseWriter, req *http.Request) {
		panic("boom")
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/panic")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	mu.Lock()
	assert.Equal(t, "boom", recovered)
	mu.Unlock()
}

func TestE2E_Middleware_CORS_Preflight(t *testing.T) {
	corsCfg := api.CORSConfig{
		AllowOrigins:     []string{"https://example.com"},
		AllowMethods:     []string{"GET", "POST"},
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
		MaxAge:           3600,
	}

	// CORS must wrap the handler directly so OPTIONS is intercepted before
	// the ServeMux rejects it as 405. This mirrors real usage where CORS
	// wraps the entire mux as outer middleware.
	inner := api.NewRouter()
	inner.Handle("GET", "/data", func(w http.ResponseWriter, req *http.Request) {
		api.JSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})
	handler := api.CORS(corsCfg)(inner)

	srv := httptest.NewServer(handler)
	defer srv.Close()

	t.Run("preflight OPTIONS returns 204 with headers", func(t *testing.T) {
		req, _ := http.NewRequest("OPTIONS", srv.URL+"/data", nil)
		req.Header.Set("Origin", "https://example.com")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
		assert.Equal(t, "https://example.com", resp.Header.Get("Access-Control-Allow-Origin"))
		assert.Contains(t, resp.Header.Get("Access-Control-Allow-Methods"), "GET")
		assert.Contains(t, resp.Header.Get("Access-Control-Allow-Headers"), "Authorization")
		assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))
		assert.Equal(t, "3600", resp.Header.Get("Access-Control-Max-Age"))
	})

	t.Run("normal request gets Allow-Origin", func(t *testing.T) {
		req, _ := http.NewRequest("GET", srv.URL+"/data", nil)
		req.Header.Set("Origin", "https://example.com")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "https://example.com", resp.Header.Get("Access-Control-Allow-Origin"))
	})
}

func TestE2E_Middleware_RequestID(t *testing.T) {
	var capturedID string

	r := api.NewRouter(api.WithMiddleware(api.RequestID()))
	r.Handle("GET", "/check", func(w http.ResponseWriter, req *http.Request) {
		capturedID = api.GetRequestID(req)
		api.JSON(w, http.StatusOK, map[string]string{"id": capturedID})
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/check")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	headerID := resp.Header.Get("X-Request-ID")
	assert.NotEmpty(t, headerID, "X-Request-ID header should be set")
	assert.Equal(t, headerID, capturedID, "GetRequestID should match response header")
}

func TestE2E_Middleware_Auth(t *testing.T) {
	authFn := func(r *http.Request) (any, error) {
		token := r.Header.Get("Authorization")
		if token == "Bearer secret" {
			return map[string]string{"role": "admin"}, nil
		}
		return nil, errors.New("invalid token")
	}

	r := api.NewRouter(api.WithMiddleware(api.Auth(authFn)))
	r.Handle("GET", "/secure", func(w http.ResponseWriter, req *http.Request) {
		claims := api.ClaimsFromContext(req.Context())
		api.JSON(w, http.StatusOK, claims)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	t.Run("missing token returns 401", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/secure")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		var apiErr api.APIError
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
		assert.Equal(t, "unauthorized", apiErr.Code)
	})

	t.Run("valid token returns 200 with claims", func(t *testing.T) {
		req, _ := http.NewRequest("GET", srv.URL+"/secure", nil)
		req.Header.Set("Authorization", "Bearer secret")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var claims map[string]string
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&claims))
		assert.Equal(t, "admin", claims["role"])
	})
}

func TestE2E_Middleware_GroupInheritsParent(t *testing.T) {
	var mu sync.Mutex
	var order []string

	parentMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			order = append(order, "parent")
			mu.Unlock()
			next.ServeHTTP(w, r)
		})
	}

	childMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			order = append(order, "child")
			mu.Unlock()
			next.ServeHTTP(w, r)
		})
	}

	r := api.NewRouter(api.WithMiddleware(parentMW))
	g := r.Group("/sub", childMW)
	g.Handle("GET", "/test", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/sub/test")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"parent", "child"}, order,
		"parent middleware should run before child")
}
