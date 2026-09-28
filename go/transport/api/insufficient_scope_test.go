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

// TestInsufficientScopeChallenge pins the RFC 6750 challenge: the
// bearer scheme, the error code, and the scopes space-delimited, with
// anything that is not a scope-token left out rather than quoted.
func TestInsufficientScopeChallenge(t *testing.T) {
	cases := map[string]struct {
		scopes []string
		want   string
	}{
		"several":         {[]string{"items:read", "items:admin"}, `Bearer error="insufficient_scope", scope="items:read items:admin"`},
		"none":            {nil, `Bearer error="insufficient_scope"`},
		"invalid dropped": {[]string{`a"b`, "c d", `e\f`, "", "ok"}, `Bearer error="insufficient_scope", scope="ok"`},
		"all invalid":     {[]string{"x\r\ny"}, `Bearer error="insufficient_scope"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, api.InsufficientScopeChallenge(tc.scopes))
		})
	}
}

// TestWriteInsufficientScope pins the answer every kit HTTP surface
// gives the class: 403, the challenge header, code insufficient_scope.
func TestWriteInsufficientScope(t *testing.T) {
	rec := httptest.NewRecorder()
	api.WriteInsufficientScope(rec, []string{"items:admin"}, "missing scope items:admin")

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, `Bearer error="insufficient_scope", scope="items:admin"`, rec.Header().Get("WWW-Authenticate"))
	var ae api.APIError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ae))
	assert.Equal(t, api.CodeInsufficientScope, ae.Code)
	assert.Equal(t, "missing scope items:admin", ae.Message)
}

// TestErrInsufficientScopeIsPermissionDenied pins that the class
// degrades to a permission denial for code that predates it.
func TestErrInsufficientScopeIsPermissionDenied(t *testing.T) {
	assert.True(t, errors.Is(api.ErrInsufficientScope, api.ErrPermissionDenied))
}
