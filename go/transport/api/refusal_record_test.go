package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// observedRefusal serves req through h behind an ObserveRefusal
// observer, as the tracing and metrics middleware does from slot 5,
// and returns the status and the refusal code the observer read.
func observedRefusal(t *testing.T, h http.Handler, req *http.Request) (int, string) {
	t.Helper()
	var code string
	rec := httptest.NewRecorder()
	http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, refused := api.ObserveRefusal(r)
		h.ServeHTTP(w, r)
		code = refused()
	}).ServeHTTP(rec, req)
	return rec.Code, code
}

func TestBodyLimitRecordsItsRefusal(t *testing.T) {
	var ran int
	h := api.BodyLimit(8)(readAllHandler(&ran))

	status, code := observedRefusal(t, h, sizedRequest(64))
	assert.Equal(t, http.StatusRequestEntityTooLarge, status)
	assert.Equal(t, api.CodeBodyTooLarge, code, "a declared length over the cap")

	status, code = observedRefusal(t, h, unsizedRequest(64))
	assert.Equal(t, http.StatusRequestEntityTooLarge, status)
	assert.Equal(t, api.CodeBodyTooLarge, code, "a body of unknown length crossing the cap")

	status, code = observedRefusal(t, h, sizedRequest(4))
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, code, "a body under the cap is no refusal")
}

func TestHostCheckRecordsItsRefusal(t *testing.T) {
	h := api.HostCheck(api.HostCheckConfig{Allow: api.LoopbackHosts})(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "attacker.example"
	status, code := observedRefusal(t, h, req)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, api.CodeHostRejected, code)

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "127.0.0.1"
	status, code = observedRefusal(t, h, req)
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, code)
}

func TestOriginCheckRecordsItsRefusal(t *testing.T) {
	mw, err := api.OriginCheck(api.OriginCheckConfig{})
	require.NoError(t, err)
	h := mw(okHandler)

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/", nil)
	req.Header.Set("Origin", "https://attacker.example")
	status, code := observedRefusal(t, h, req)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, api.CodeOriginRejected, code)

	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/", nil)
	status, code = observedRefusal(t, h, req)
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, code, "a request without Origin is not a browser's")
}

func TestAuthRecordsItsRefusal(t *testing.T) {
	h := api.Auth(func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") != "Bearer good" {
			return nil, errors.New("bad credential")
		}
		return api.Claims{Subject: "alice"}, nil
	})(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	status, code := observedRefusal(t, h, req)
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Equal(t, api.CodeUnauthenticated, code)

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer good")
	status, code = observedRefusal(t, h, req)
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, code, "a verified request is no refusal")
}

// The refusal is the unauthenticated class of the middleware
// contract: 401, code unauthenticated, and a WWW-Authenticate
// challenge, which an AuthChallenge option names.
func TestAuthRefusalIsUnauthenticatedWithAChallenge(t *testing.T) {
	deny := func(*http.Request) (any, error) { return nil, errors.New("missing bearer token") }

	rec := httptest.NewRecorder()
	api.Auth(deny)(okHandler).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, api.DefaultAuthChallenge, rec.Header().Get("WWW-Authenticate"))
	var body api.APIError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, api.APIError{Status: 401, Code: "unauthenticated", Message: "missing bearer token"}, body)

	rec = httptest.NewRecorder()
	api.Auth(deny, api.AuthChallenge(`Basic realm="tool"`))(okHandler).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, `Basic realm="tool"`, rec.Header().Get("WWW-Authenticate"))
}
