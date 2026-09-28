package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

func TestSecurityHeaders_Defaults(t *testing.T) {
	h := api.SecurityHeaders(api.SecurityHeadersConfig{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			api.JSON(w, http.StatusOK, map[string]string{"ok": "yes"})
		}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
	assert.Equal(t, api.DefaultContentSecurityPolicy, rec.Header().Get("Content-Security-Policy"))
	assert.Empty(t, rec.Header().Get("Strict-Transport-Security"),
		"HSTS on a plaintext response is ignored by browsers and misleads readers")
}

func TestSecurityHeaders_HandlerCSPWins(t *testing.T) {
	// A handler serving its own document (huma's /docs page) sets
	// the policy that document needs; the default must not stack on
	// top of it.
	h := api.SecurityHeaders(api.SecurityHeadersConfig{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Security-Policy", "default-src 'self'")
			w.WriteHeader(http.StatusOK)
		}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, []string{"default-src 'self'"}, rec.Header().Values("Content-Security-Policy"))
}

func TestSecurityHeaders_HSTSOnlyOverTLS(t *testing.T) {
	h := api.SecurityHeaders(api.SecurityHeadersConfig{})(okHandler)

	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, "max-age=31536000", resp.Header.Get("Strict-Transport-Security"))

	plain := httptest.NewServer(h)
	defer plain.Close()
	resp, err = http.Get(plain.URL)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Empty(t, resp.Header.Get("Strict-Transport-Security"))
}

func TestSecurityHeaders_DocsPageKeepsItsPolicy(t *testing.T) {
	// The router serves exactly one HTML document: huma's /docs
	// page, which loads its renderer from a CDN under its own CSP.
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{Title: "t", Version: "1"}))
	h := api.SecurityHeaders(api.SecurityHeadersConfig{})(r)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body, _ := io.ReadAll(rec.Body)
	require.Contains(t, string(body), "<html")

	csp := rec.Header().Values("Content-Security-Policy")
	require.Len(t, csp, 1)
	assert.True(t, strings.Contains(csp[0], "script-src https://unpkg.com/"), csp[0])
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, api.DefaultContentSecurityPolicy, rec.Header().Get("Content-Security-Policy"))
}

func TestSecurityHeaders_OuterValueKept(t *testing.T) {
	// A layer outside this one that already chose a value keeps it.
	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Referrer-Policy", "same-origin")
			next.ServeHTTP(w, r)
		})
	}
	h := outer(api.SecurityHeaders(api.SecurityHeadersConfig{})(okHandler))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, []string{"same-origin"}, rec.Header().Values("Referrer-Policy"))
}
