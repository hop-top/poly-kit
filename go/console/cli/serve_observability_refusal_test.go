package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/transport/api"
)

// refusalCounter is a provider whose HTTP middleware reads back the
// refusal code of every request, the way the metrics middleware does
// from HTTP-plane slot 5.
type refusalCounter struct {
	fakeObservability
	codesMu sync.Mutex
	codes   []string
}

func (c *refusalCounter) HTTPMiddleware(string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, refused := api.ObserveRefusal(r)
			next.ServeHTTP(w, r)
			if code := refused(); code != "" {
				c.codesMu.Lock()
				c.codes = append(c.codes, code)
				c.codesMu.Unlock()
			}
		})
	}
}

// refusalCounted serves req through the api service on a loopback
// address with a refusalCounter linked, and returns the status and the
// codes the counter saw.
func refusalCounted(t *testing.T, keys map[string]any, req *http.Request) (int, []string) {
	t.Helper()
	c := &refusalCounter{}
	r := projectionFixture(t)
	WithObservability(c)(r)
	r.Viper.Set("services.api.addr", "127.0.0.1:0")
	for k, v := range keys {
		r.Viper.Set(k, v)
	}
	rec := httptest.NewRecorder()
	projectionHandler(t, r).ServeHTTP(rec, req)
	return rec.Code, c.codes
}

func TestAPIHostRefusalIsCountedByTheMetricsSlot(t *testing.T) {
	req := loopbackRequest(http.MethodGet, "/v1/commands/list", nil)
	req.Host = "attacker.example"
	status, codes := refusalCounted(t, nil, req)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, []string{api.CodeHostRejected}, codes)
}

func TestAPIOriginRefusalIsCountedByTheMetricsSlot(t *testing.T) {
	req := loopbackRequest(http.MethodPost, "/v1/commands/add", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://attacker.example")
	status, codes := refusalCounted(t, nil, req)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, []string{api.CodeOriginRejected}, codes)
}

func TestAPIBodyLimitRefusalIsCountedByTheMetricsSlot(t *testing.T) {
	req := loopbackRequest(http.MethodPost, "/v1/commands/add", strings.NewReader(strings.Repeat("x", 64)))
	status, codes := refusalCounted(t, map[string]any{"services.api.body_limit.max_bytes": 16}, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, status)
	assert.Equal(t, []string{api.CodeBodyTooLarge}, codes)
}

func TestAPIAdmittedRequestRecordsNoRefusal(t *testing.T) {
	status, codes := refusalCounted(t, nil, loopbackRequest(http.MethodGet, "/v1/commands/list", nil))
	assert.Equal(t, http.StatusOK, status)
	assert.Empty(t, codes)
}

func TestAPIAuthRefusalIsCountedByTheMetricsSlot(t *testing.T) {
	c := &refusalCounter{}
	r := authRequiredFixture(func(*http.Request) (any, error) {
		return nil, errors.New("missing bearer token")
	})
	WithObservability(c)(r)
	rec := httptest.NewRecorder()
	projectionHandler(t, r).ServeHTTP(rec, loopbackRequest(http.MethodGet, "/v1/commands", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, []string{api.CodeUnauthenticated}, c.codes)
	assert.Equal(t, api.DefaultAuthChallenge, rec.Header().Get("WWW-Authenticate"))
}
