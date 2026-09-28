package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// TestProjectionServesAuthRequiredUnderRouterAuth pins that the api
// service owns authentication for its projection. A command declaring
// kit/auth-required is served whether or not APIConfig.Auth is set:
// with Auth, the router authenticates every route; without it,
// Validate confines the service to loopback. The bridge-level mount
// refuses such a command when nothing authenticates it, so the
// service must say the router does.
func TestProjectionServesAuthRequiredUnderRouterAuth(t *testing.T) {
	r := New(Config{Name: "fix", Version: "9.9.9", DisableValidate: true},
		WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
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

	h := projectionHandler(t, r)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/commands/rotate", nil))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "rotated")
}
