package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// unsized hides the concrete reader type so httptest.NewRequest
// reports an unknown Content-Length (-1), the shape a chunked
// HTTP/1.1 or an HTTP/2 body arrives in.
type unsized struct{ io.Reader }

func sizedRequest(n int) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", n)))
}

func unsizedRequest(n int) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/", unsized{strings.NewReader(strings.Repeat("x", n))})
}

// readAllHandler reads the whole body, the way a JSON decoder or an
// io.ReadAll in a handler would, and renders the cap refusal the way
// kit handlers do.
func readAllHandler(got *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if limit, over := api.AsBodyTooLarge(err); over {
			api.WriteBodyTooLarge(w, limit)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		*got = len(b)
		w.WriteHeader(http.StatusOK)
	})
}

func assertBodyTooLarge(t *testing.T, rec *httptest.ResponseRecorder, limit int64) {
	t.Helper()
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	var body api.APIError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, api.CodeBodyTooLarge, body.Code)
	assert.Equal(t, http.StatusRequestEntityTooLarge, body.Status)
	assert.Contains(t, body.Message, "exceeds")
	assert.Contains(t, body.Message, strconv.FormatInt(limit, 10))
}

func TestBodyLimit(t *testing.T) {
	const limit = 64
	cases := []struct {
		name     string
		req      *http.Request
		wantCode int
		wantHook int
	}{
		{"sized at limit passes", sizedRequest(limit), http.StatusOK, 0},
		{"sized one over is refused", sizedRequest(limit + 1), http.StatusRequestEntityTooLarge, 1},
		{"unsized at limit passes", unsizedRequest(limit), http.StatusOK, 0},
		{"unsized one over is refused", unsizedRequest(limit + 1), http.StatusRequestEntityTooLarge, 1},
		{"unsized far over is refused", unsizedRequest(10 * limit), http.StatusRequestEntityTooLarge, 1},
		{"empty body passes", httptest.NewRequest(http.MethodPost, "/", nil), http.StatusOK, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var hooks []int64
			got := -1
			h := api.BodyLimit(limit, api.OnBodyTooLarge(func(_ *http.Request, l int64) {
				hooks = append(hooks, l)
			}))(readAllHandler(&got))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, c.req)

			assert.Equal(t, c.wantCode, rec.Code, rec.Body.String())
			assert.Len(t, hooks, c.wantHook, "the refusal hook fires exactly once per refused request")
			for _, l := range hooks {
				assert.Equal(t, int64(limit), l)
			}
			if c.wantCode == http.StatusOK {
				assert.LessOrEqual(t, got, limit)
			} else {
				assertBodyTooLarge(t, rec, limit)
			}
		})
	}
}

func TestBodyLimitSizedRefusalSkipsHandler(t *testing.T) {
	// A declared Content-Length over the cap is refused before the
	// handler runs: nothing downstream sees the request at all.
	called := false
	h := api.BodyLimit(8)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sizedRequest(9))
	assertBodyTooLarge(t, rec, 8)
	assert.False(t, called)
}

func TestBodyLimitZeroIsDefault(t *testing.T) {
	assert.Equal(t, int64(1<<20), api.DefaultMaxBodyBytes)
	assert.Equal(t, api.DefaultMaxBodyBytes, api.MaxBodyBytesOrDefault(0))
	assert.Equal(t, int64(5), api.MaxBodyBytesOrDefault(5))
	assert.Equal(t, int64(-1), api.MaxBodyBytesOrDefault(-1))

	got := -1
	h := api.BodyLimit(0)(readAllHandler(&got))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sizedRequest(int(api.DefaultMaxBodyBytes)))
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, unsizedRequest(int(api.DefaultMaxBodyBytes)+1))
	assertBodyTooLarge(t, rec, api.DefaultMaxBodyBytes)
}

func TestBodyLimitNegativeDisables(t *testing.T) {
	got := -1
	h := api.BodyLimit(-1)(readAllHandler(&got))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, unsizedRequest(int(api.DefaultMaxBodyBytes)+1))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, int(api.DefaultMaxBodyBytes)+1, got)
}

// jsonBodyOfSize returns a projection POST body of exactly n bytes:
// the padding sits inside an argument, so a JSON decoder must read
// every byte to finish the value.
func jsonBodyOfSize(t *testing.T, n int) string {
	t.Helper()
	const prefix, suffix = `{"args":["`, `"]}`
	pad := n - len(prefix) - len(suffix)
	require.Positive(t, pad)
	return prefix + strings.Repeat("x", pad) + suffix
}

func TestProjectionBodyLimit(t *testing.T) {
	const limit = 256
	cases := []struct {
		name     string
		body     func(string) io.Reader
		size     int
		wantCode int
	}{
		{"sized at limit runs", func(s string) io.Reader { return strings.NewReader(s) }, limit, http.StatusOK},
		{"sized one over is 413", func(s string) io.Reader { return strings.NewReader(s) }, limit + 1, http.StatusRequestEntityTooLarge},
		{"unsized at limit runs", func(s string) io.Reader { return unsized{strings.NewReader(s)} }, limit, http.StatusOK},
		{"unsized one over is 413", func(s string) io.Reader { return unsized{strings.NewReader(s)} }, limit + 1, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ex := &stubExecutor{}
			r := api.NewRouter(api.WithMiddleware(api.BodyLimit(limit)))
			api.MountCommandProjection(r, api.ProjectionConfig{
				Descriptors: fixtureDescriptors(),
				Executor:    ex,
			})

			req := httptest.NewRequest(http.MethodPost, "/v1/commands/widget/add",
				c.body(jsonBodyOfSize(t, c.size)))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			assert.Equal(t, c.wantCode, rec.Code, rec.Body.String())
			if c.wantCode == http.StatusOK {
				assert.Equal(t, 1, ex.calls)
				return
			}
			assertBodyTooLarge(t, rec, limit)
			assert.Zero(t, ex.calls, "an oversized body must never reach the executor")
		})
	}
}

// A listener whose protocol carries errors elsewhere (a JSON-RPC body,
// a Connect error) renders the refusal itself; the code, the status
// and the recorded refusal stay the body limit's.
func TestBodyLimitRefusalWriter(t *testing.T) {
	var got *api.APIError
	refuse := func(w http.ResponseWriter, _ *http.Request, e *api.APIError) {
		got = e
		w.WriteHeader(e.Status)
		_, _ = io.WriteString(w, "protocol-shaped")
	}
	var fired int64
	mw := api.BodyLimit(8, api.BodyTooLargeRefusal(refuse),
		api.OnBodyTooLarge(func(_ *http.Request, limit int64) { fired = limit }))
	var n int
	rec := httptest.NewRecorder()
	mw(readAllHandler(&n)).ServeHTTP(rec, sizedRequest(9))

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Equal(t, "protocol-shaped", rec.Body.String())
	require.NotNil(t, got)
	assert.Equal(t, api.CodeBodyTooLarge, got.Code)
	assert.Contains(t, got.Message, "8")
	assert.Equal(t, int64(8), fired, "the hook still observes the refusal")

	rec = httptest.NewRecorder()
	mw(readAllHandler(&n)).ServeHTTP(rec, sizedRequest(8))
	assert.Equal(t, http.StatusOK, rec.Code, "at the cap is admitted")
}
