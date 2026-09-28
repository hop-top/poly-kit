package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// corsServe runs one request through CORS(cfg) in front of a handler
// that answers 200, and reports whether the handler ran.
func corsServe(cfg api.CORSConfig, method, origin string, hdr map[string]string) (*httptest.ResponseRecorder, bool) {
	reached := false
	h := api.CORS(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(method, "/x", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, reached
}

var preflightPOST = map[string]string{"Access-Control-Request-Method": http.MethodPost}

// No origin listed grants none — never every origin, which is what the
// underlying library reads an empty list as.
func TestCORS_NoOriginsGrantsNone(t *testing.T) {
	rec, reached := corsServe(api.CORSConfig{}, http.MethodGet, "https://a.example", nil)
	assert.True(t, reached)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

	rec, _ = corsServe(api.CORSConfig{}, http.MethodOptions, "https://a.example", preflightPOST)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

// Only OPTIONS with Access-Control-Request-Method is a preflight; it is
// answered and stops. Any other OPTIONS reaches the handler.
func TestCORS_PreflightIsAnsweredHere(t *testing.T) {
	cfg := api.CORSConfig{AllowOrigins: []string{"https://a.example"}}
	rec, reached := corsServe(cfg, http.MethodOptions, "https://a.example", preflightPOST)
	assert.False(t, reached)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "https://a.example", rec.Header().Get("Access-Control-Allow-Origin"))

	rec, reached = corsServe(cfg, http.MethodOptions, "https://a.example", nil)
	assert.True(t, reached, "an OPTIONS without Access-Control-Request-Method is the route's")
	assert.Equal(t, http.StatusOK, rec.Code)
}

// Credentials are granted to a listed origin only, and a grant is the
// origin itself, never "*".
func TestCORS_CredentialsOnlyForListedOrigins(t *testing.T) {
	cfg := api.CORSConfig{AllowOrigins: []string{"https://a.example"}, AllowCredentials: true}
	rec, _ := corsServe(cfg, http.MethodGet, "https://a.example", nil)
	assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "https://a.example", rec.Header().Get("Access-Control-Allow-Origin"))

	rec, _ = corsServe(cfg, http.MethodGet, "https://b.example", nil)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

// An origin that does not parse is never granted.
func TestCORS_UnparsedOriginNeverGranted(t *testing.T) {
	cfg := api.CORSConfig{AllowOrigins: []string{"https://*.example"}}
	rec, _ := corsServe(cfg, http.MethodGet, "https://a.example", nil)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_ExposeHeaders(t *testing.T) {
	cfg := api.CORSConfig{AllowOrigins: []string{"*"}, ExposeHeaders: []string{"mcp-session-id"}}
	rec, _ := corsServe(cfg, http.MethodPost, "https://a.example", nil)
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Mcp-Session-Id", rec.Header().Get("Access-Control-Expose-Headers"))
	assert.Contains(t, rec.Header().Values("Vary"), "Origin")
}

func TestParseOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://app.example":         "https://app.example",
		" HTTPS://App.Example:443 ":   "https://app.example",
		"http://localhost:80":         "http://localhost",
		"http://localhost:3000":       "http://localhost:3000",
		"https://app.example:8443":    "https://app.example:8443",
		"http://[::1]:8080":           "http://[::1]:8080",
		"chrome-extension://abcdefgh": "chrome-extension://abcdefgh",
	} {
		got, err := api.ParseOrigin(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{
		"", "null", "app.example", "https://app.example/", "https://app.example/x",
		"https://app.example?q", "https://app.example#f", "https://u@app.example",
		"https://*.app.example", "*", "mailto:x@app.example",
	} {
		_, err := api.ParseOrigin(in)
		assert.Error(t, err, in)
	}
}

func TestCORSConfig_Validate(t *testing.T) {
	ok := []api.CORSConfig{
		{},
		{AllowOrigins: []string{"*"}},
		{AllowOrigins: []string{"https://a.example", "http://localhost:3000"}, AllowCredentials: true},
		{AllowOrigins: []string{"*"}, AllowHeaders: []string{"*"}, ExposeHeaders: []string{"*"}},
		{AllowMethods: []string{"GET", "PROPFIND"}, AllowHeaders: []string{"X-Custom"}, MaxAge: 600},
	}
	for _, c := range ok {
		assert.NoError(t, c.Validate(), "%+v", c)
	}
	bad := map[string]api.CORSConfig{
		"credentials with *":         {AllowOrigins: []string{"*"}, AllowCredentials: true},
		"* beside origins":           {AllowOrigins: []string{"*", "https://a.example"}},
		"malformed origin":           {AllowOrigins: []string{"a.example"}},
		"method not a token":         {AllowMethods: []string{"GE T"}},
		"header not a name":          {AllowHeaders: []string{"X:Y"}},
		"exposed not a name":         {ExposeHeaders: []string{"X Y"}},
		"credentialed exposure by *": {ExposeHeaders: []string{"*"}, AllowCredentials: true},
		"negative max age":           {MaxAge: -1},
	}
	for name, c := range bad {
		assert.Error(t, c.Validate(), name)
	}
}
