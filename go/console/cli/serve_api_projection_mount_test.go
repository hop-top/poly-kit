package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/transport/api"
)

// authRequiredFixture is an api service root with one command
// declaring kit/auth-required, authenticated by auth when non-nil.
func authRequiredFixture(auth api.AuthFunc) *Root {
	r := New(Config{Name: "fix", Version: "9.9.9", DisableValidate: true},
		WithAPI(APIConfig{Addr: "127.0.0.1:0", Auth: auth}))
	r.Cmd.AddCommand(&cobra.Command{
		Use:   "rotate",
		Short: "rotate the key",
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = cmd.OutOrStdout().Write([]byte("rotated"))
		},
		Annotations: map[string]string{
			"kit/side-effect":   "write-local",
			"kit/auth-required": "true",
		},
	})
	return r
}

// TestProjectionMountsAuthRequiredUnderRouterAuth pins that the api
// service owns authentication for its projection: a command declaring
// kit/auth-required is mounted whether or not APIConfig.Auth is set,
// since the bridge-level mount refuses such a command when nothing
// says who authenticates it.
func TestProjectionMountsAuthRequiredUnderRouterAuth(t *testing.T) {
	h := projectionHandler(t, authRequiredFixture(nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loopbackRequest(http.MethodGet, "/v1/commands", nil))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"rotate"`)
}

// TestProjectionAuthRequiredNeedsAVerifiedCaller pins that loopback is
// not authentication: without APIConfig.Auth the command is refused
// 401 unauthenticated whatever the request carries, and with Auth it
// runs only for a credential Auth accepts.
func TestProjectionAuthRequiredNeedsAVerifiedCaller(t *testing.T) {
	h := projectionHandler(t, authRequiredFixture(nil))
	for _, hdr := range []string{"", "Bearer anything"} {
		req := loopbackRequest(http.MethodPost, "/v1/commands/rotate", nil)
		if hdr != "" {
			req.Header.Set("Authorization", hdr)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"code":"unauthenticated"`)
		assert.NotEmpty(t, rec.Header().Get("WWW-Authenticate"))
		assert.NotContains(t, rec.Body.String(), "rotated")
	}

	verify := func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") != "Bearer good" {
			return nil, errors.New("bad credential")
		}
		return api.Claims{Subject: "alice"}, nil
	}
	h = projectionHandler(t, authRequiredFixture(verify))
	req := loopbackRequest(http.MethodPost, "/v1/commands/rotate", nil)
	req.Header.Set("Authorization", "Bearer bad")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())

	req = loopbackRequest(http.MethodPost, "/v1/commands/rotate", nil)
	req.Header.Set("Authorization", "Bearer good")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "rotated")
}
