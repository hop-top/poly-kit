package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

func originRequest(method, host, origin, fetchSite string) *http.Request {
	req := httptest.NewRequest(method, "/v1/commands/item/add", nil)
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	return req
}

func mustOriginCheck(t *testing.T, cfg api.OriginCheckConfig) http.Handler {
	t.Helper()
	mw, err := api.OriginCheck(cfg)
	require.NoError(t, err)
	return mw(okHandler)
}

func TestOriginCheck_RefusesCrossOriginWrite(t *testing.T) {
	h := mustOriginCheck(t, api.OriginCheckConfig{})

	for _, req := range []*http.Request{
		originRequest(http.MethodPost, "127.0.0.1:8080", "https://attacker.example", ""),
		originRequest(http.MethodPost, "127.0.0.1:8080", "https://attacker.example", "cross-site"),
		originRequest(http.MethodDelete, "localhost:8080", "http://localhost:3000", ""),
		originRequest(http.MethodPut, "localhost:8080", "null", ""),
		// A browser that sends Sec-Fetch-Site but no Origin.
		originRequest(http.MethodPost, "localhost:8080", "", "cross-site"),
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code, "%s %s", req.Method, req.Header)
		assert.Equal(t, api.CodeOriginRejected, refusalCode(t, rec))
	}
}

func TestOriginCheck_AllowsNonBrowserSameOriginAndSafe(t *testing.T) {
	h := mustOriginCheck(t, api.OriginCheckConfig{})

	for _, req := range []*http.Request{
		// No Origin: curl, SDKs, other servers.
		originRequest(http.MethodPost, "127.0.0.1:8080", "", ""),
		// Same origin as the (already validated) Host.
		originRequest(http.MethodPost, "localhost:8080", "http://localhost:8080", ""),
		originRequest(http.MethodPost, "localhost:8080", "http://localhost:8080", "same-origin"),
		// Safe methods are not state-changing.
		originRequest(http.MethodGet, "localhost:8080", "https://attacker.example", "cross-site"),
		originRequest(http.MethodHead, "localhost:8080", "https://attacker.example", ""),
		originRequest(http.MethodOptions, "localhost:8080", "https://attacker.example", ""),
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "%s %s", req.Method, req.Header)
	}
}

func TestOriginCheck_Allow(t *testing.T) {
	h := mustOriginCheck(t, api.OriginCheckConfig{
		Allow: []string{"https://app.example.com"},
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, originRequest(http.MethodPost, "api.example.com", "https://app.example.com", "cross-site"))
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, originRequest(http.MethodPost, "api.example.com", "https://other.example.com", "cross-site"))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestOriginCheck_WildcardDisables(t *testing.T) {
	h := mustOriginCheck(t, api.OriginCheckConfig{Allow: []string{"*"}})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, originRequest(http.MethodPost, "localhost:8080", "https://attacker.example", "cross-site"))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestOriginCheck_RejectsMalformedEntry(t *testing.T) {
	for _, bad := range []string{"app.example.com", "https://app.example.com/path", "https://app.example.com?q"} {
		_, err := api.OriginCheck(api.OriginCheckConfig{Allow: []string{bad}})
		assert.Error(t, err, bad)
	}
}

func TestOriginCheck_CustomRefusal(t *testing.T) {
	h := mustOriginCheck(t, api.OriginCheckConfig{
		Refuse: func(w http.ResponseWriter, _ *http.Request, e *api.APIError) {
			w.Header().Set("X-Refusal", e.Code)
			w.WriteHeader(e.Status)
		},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, originRequest(http.MethodPost, "localhost:8080", "https://attacker.example", ""))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, api.CodeOriginRejected, rec.Header().Get("X-Refusal"))
}
