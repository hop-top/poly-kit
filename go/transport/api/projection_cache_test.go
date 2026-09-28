package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

func cachedExecutor(private bool) *stubExecutor {
	return &stubExecutor{result: api.CommandResult{
		Data:  map[string]any{"n": 1},
		Cache: &api.CacheDirective{ETag: "abc123", MaxAge: 90*time.Second + 400*time.Millisecond, Private: private},
	}}
}

func getCached(r http.Handler, path string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCacheDirectiveRendersETagAndCacheControl(t *testing.T) {
	for _, c := range []struct {
		private bool
		want    string
	}{
		{false, "public, max-age=90"},
		{true, "private, max-age=90"},
	} {
		r := newProjectedRouter(t, cachedExecutor(c.private))
		rec := getCached(r, "/v1/commands/list", nil)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, `W/"abc123"`, rec.Header().Get("ETag"))
		assert.Equal(t, c.want, rec.Header().Get("Cache-Control"))
		assert.Contains(t, rec.Body.String(), `"n":1`)
		// The directive is response metadata, never part of the body.
		assert.NotContains(t, rec.Body.String(), "abc123")
	}
}

func TestIfNoneMatchAnswers304(t *testing.T) {
	for _, inm := range []string{
		`W/"abc123"`,
		`"abc123"`,
		`"other", W/"abc123"`,
		`*`,
	} {
		t.Run(inm, func(t *testing.T) {
			ex := cachedExecutor(false)
			rec := getCached(newProjectedRouter(t, ex), "/v1/commands/list",
				map[string]string{"If-None-Match": inm})
			require.Equal(t, http.StatusNotModified, rec.Code)
			assert.Empty(t, rec.Body.String())
			assert.Equal(t, `W/"abc123"`, rec.Header().Get("ETag"))
			assert.Equal(t, "public, max-age=90", rec.Header().Get("Cache-Control"))
		})
	}
}

func TestIfNoneMatchMismatchAnswersBody(t *testing.T) {
	for _, inm := range []string{`"abc12"`, `W/"xabc123"`, `abc123`, `""`} {
		rec := getCached(newProjectedRouter(t, cachedExecutor(false)), "/v1/commands/list",
			map[string]string{"If-None-Match": inm})
		require.Equal(t, http.StatusOK, rec.Code, inm)
		assert.Contains(t, rec.Body.String(), `"n":1`, inm)
	}
}

func TestNoCacheHeadersWithoutDirective(t *testing.T) {
	ex := &stubExecutor{}
	rec := getCached(newProjectedRouter(t, ex), "/v1/commands/list",
		map[string]string{"If-None-Match": "*"})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("ETag"))
	assert.Empty(t, rec.Header().Get("Cache-Control"))
}

// A failed run, or a route that is not a read, never renders the
// directive, whatever the executor sets.
func TestCacheDirectiveOnlyOnSuccessfulGET(t *testing.T) {
	ex := cachedExecutor(false)
	ex.result.ExitCode = 1
	rec := getCached(newProjectedRouter(t, ex), "/v1/commands/list",
		map[string]string{"If-None-Match": "*"})
	assert.NotEqual(t, http.StatusNotModified, rec.Code)
	assert.Empty(t, rec.Header().Get("ETag"))

	ex = cachedExecutor(false)
	req := httptest.NewRequest(http.MethodPost, "/v1/commands/widget/add",
		strings.NewReader(`{"args":["gadget"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-None-Match", "*")
	w := httptest.NewRecorder()
	newProjectedRouter(t, ex).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Empty(t, w.Header().Get("ETag"))
}
