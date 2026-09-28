package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// unsizedBody hides the reader type: httptest reports an unknown
// Content-Length and net/http sends it chunked.
type unsizedBody struct{ io.Reader }

// addBody is a projection POST body for "add" of exactly n bytes; the
// padding sits inside an argument so a decoder reads every byte.
func addBody(t *testing.T, n int) string {
	t.Helper()
	const prefix, suffix = `{"args":["`, `"]}`
	require.Greater(t, n, len(prefix)+len(suffix))
	return prefix + strings.Repeat("x", n-len(prefix)-len(suffix)) + suffix
}

func requireTooLarge(t *testing.T, status int, body []byte) {
	t.Helper()
	require.Equal(t, http.StatusRequestEntityTooLarge, status, string(body))
	var e api.APIError
	require.NoError(t, json.Unmarshal(body, &e))
	assert.Equal(t, api.CodeBodyTooLarge, e.Code)
}

func TestAPIServiceBodyLimit(t *testing.T) {
	const limit = 256
	cases := []struct {
		name string
		size int
		wrap func(string) io.Reader
		over bool
	}{
		{"sized at limit", limit, func(s string) io.Reader { return strings.NewReader(s) }, false},
		{"sized one over", limit + 1, func(s string) io.Reader { return strings.NewReader(s) }, true},
		{"unsized at limit", limit, func(s string) io.Reader { return unsizedBody{strings.NewReader(s)} }, false},
		{"unsized one over", limit + 1, func(s string) io.Reader { return unsizedBody{strings.NewReader(s)} }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateHome(t)
			rec := &auditRecorder{}
			r := authRoot(t, WithAPI(APIConfig{MaxBodyBytes: limit}), WithAuditSinks(rec.spec()))
			h := projectionHandler(t, r)

			req := loopbackRequest(http.MethodPost, "/v1/commands/add", c.wrap(addBody(t, c.size)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Request-ID", "req-big")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if !c.over {
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				return
			}
			requireTooLarge(t, w.Code, w.Body.Bytes())
			inv, _, err := rec.last(t)
			assert.ErrorIs(t, err, cmdsurface.ErrBodyTooLarge)
			assert.Equal(t, []string{"add"}, inv.Path, "the addressed command is recorded")
			assert.Equal(t, "req-big", inv.Meta.RequestID, "the refusal carries the request id")
			assert.Equal(t, cmdsurface.SurfaceREST, inv.Meta.Surface)
			for i := range rec.errs {
				assert.ErrorIs(t, rec.errs[i], cmdsurface.ErrBodyTooLarge,
					"an oversized call must never run: the only record is the refusal")
			}
		})
	}
}

func TestAPIServiceBodyLimitCoversStreamRoutes(t *testing.T) {
	// A streaming route decodes the same body: over the cap it is a
	// 413 before anything is admitted, audited against the command
	// the stream addresses, not a "stream" command that does not exist.
	const limit = 256
	for name, wrap := range map[string]func(string) io.Reader{
		"sized":   func(s string) io.Reader { return strings.NewReader(s) },
		"unsized": func(s string) io.Reader { return unsizedBody{strings.NewReader(s)} },
	} {
		t.Run(name, func(t *testing.T) {
			isolateHome(t)
			rec := &auditRecorder{}
			r := authRoot(t, WithAPI(APIConfig{MaxBodyBytes: limit}), WithAuditSinks(rec.spec()))
			h := projectionHandler(t, r)

			req := loopbackRequest(http.MethodPost, "/v1/commands/add/stream", wrap(addBody(t, limit+1)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			requireTooLarge(t, w.Code, w.Body.Bytes())
			inv, _, err := rec.last(t)
			assert.ErrorIs(t, err, cmdsurface.ErrBodyTooLarge)
			assert.Equal(t, []string{"add"}, inv.Path, "the streamed command is recorded")
			for i := range rec.errs {
				assert.ErrorIs(t, rec.errs[i], cmdsurface.ErrBodyTooLarge,
					"an oversized stream must never run: the only record is the refusal")
			}
		})
	}
}

func TestAPIServiceBodyLimitCoversAdopterRoutes(t *testing.T) {
	// The cap is chain-wide, not a projection feature: an adopter's
	// own Handlers route reading its body is capped the same way.
	isolateHome(t)
	var readErr error
	r := authRoot(t, WithAPI(APIConfig{
		MaxBodyBytes: 64,
		Handlers: func(rt *api.Router) {
			rt.Handle(http.MethodPost, "/upload", func(w http.ResponseWriter, req *http.Request) {
				_, readErr = io.ReadAll(req.Body)
				if limit, over := api.AsBodyTooLarge(readErr); over {
					api.WriteBodyTooLarge(w, limit)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
		},
	}))
	h := projectionHandler(t, r)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, loopbackRequest(http.MethodPost, "/upload", unsizedBody{strings.NewReader(strings.Repeat("x", 65))}))
	requireTooLarge(t, w.Code, w.Body.Bytes())

	w = httptest.NewRecorder()
	h.ServeHTTP(w, loopbackRequest(http.MethodPost, "/upload", strings.NewReader(strings.Repeat("x", 64))))
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAPIServiceBodyLimitDefault(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{}))
	got, err := apiSvc(t, r).maxBodyBytes()
	require.NoError(t, err)
	assert.Equal(t, api.DefaultMaxBodyBytes, got)

	h := projectionHandler(t, r)
	w := httptest.NewRecorder()
	req := loopbackRequest(http.MethodPost, "/v1/commands/add",
		strings.NewReader(addBody(t, int(api.DefaultMaxBodyBytes)+1)))
	h.ServeHTTP(w, req)
	requireTooLarge(t, w.Code, w.Body.Bytes())
}

func TestAPIServiceBodyLimitConfigKey(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{MaxBodyBytes: 4096}))
	a := apiSvc(t, r)

	// The service key wins over the Go option.
	r.Viper.Set("services.api.body_limit.max_bytes", 128)
	got, err := a.maxBodyBytes()
	require.NoError(t, err)
	assert.Equal(t, int64(128), got)

	h := projectionHandler(t, r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, loopbackRequest(http.MethodPost, "/v1/commands/add",
		strings.NewReader(addBody(t, 129))))
	requireTooLarge(t, w.Code, w.Body.Bytes())

	// A numeric string (the environment's form) is accepted; zero is
	// the default.
	for raw, want := range map[any]int64{"2048": 2048, 0: api.DefaultMaxBodyBytes} {
		r.Viper.Set("services.api.body_limit.max_bytes", raw)
		got, err := a.maxBodyBytes()
		require.NoError(t, err, "%v", raw)
		assert.Equal(t, want, got, "%v", raw)
	}

	// Anything that is not a whole, non-negative byte count is
	// refused at validation, naming the key.
	for _, bad := range []any{"1MiB", 1.5, true, -1} {
		r.Viper.Set("services.api.body_limit.max_bytes", bad)
		err := a.Validate()
		require.Error(t, err, "%v", bad)
		assert.Contains(t, err.Error(), "services.api.body_limit.max_bytes")
	}
}

func TestAPIServiceBodyLimitResolution(t *testing.T) {
	// Per key, specificity before source: services.api, then
	// services.all, then the Go option, then the kit default.
	cases := []struct {
		name string
		code int64
		keys map[string]any
		want int64
	}{
		{"kit default", 0, nil, api.DefaultMaxBodyBytes},
		{"code option", 4096, nil, 4096},
		{"code disables", -1, nil, -1},
		{"services.all over code", 4096, map[string]any{"services.all.body_limit.max_bytes": 2048}, 2048},
		{"service over services.all", 4096, map[string]any{
			"services.all.body_limit.max_bytes": 2048,
			"services.api.body_limit.max_bytes": 1024,
		}, 1024},
		{"keys merge one by one", 4096, map[string]any{
			"services.all.body_limit.max_bytes": 2048,
			"services.api.body_limit.enabled":   true,
		}, 2048},
		{"enabled false disables", 4096, map[string]any{"services.api.body_limit.enabled": false}, -1},
		{"services.all enabled false disables", 0, map[string]any{"services.all.body_limit.enabled": "false"}, -1},
		{"service enabled beats services.all", 0, map[string]any{
			"services.all.body_limit.enabled": false,
			"services.api.body_limit.enabled": true,
		}, api.DefaultMaxBodyBytes},
		{"config enables over a disabling code option", -1, map[string]any{"services.api.body_limit.enabled": true}, api.DefaultMaxBodyBytes},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateHome(t)
			r := authRoot(t, WithAPI(APIConfig{MaxBodyBytes: c.code}))
			for k, v := range c.keys {
				r.Viper.Set(k, v)
			}
			got, err := apiSvc(t, r).maxBodyBytes()
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestAPIServiceBodyLimitDisabledServesUncapped(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{}))
	r.Viper.Set("services.api.body_limit.enabled", false)
	h := projectionHandler(t, r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, loopbackRequest(http.MethodPost, "/v1/commands/add",
		unsizedBody{strings.NewReader(addBody(t, int(api.DefaultMaxBodyBytes)+1))}))
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestAPIServiceBodyLimitRejectsUnknownKeys(t *testing.T) {
	for _, k := range []string{"services.api.body_limit.max_byte", "services.all.body_limit.limit"} {
		t.Run(k, func(t *testing.T) {
			isolateHome(t)
			r := authRoot(t, WithAPI(APIConfig{}))
			r.Viper.Set(k, 10)
			err := apiSvc(t, r).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), k)
		})
	}
}

func TestAPIServiceBodyLimitOverTheWire(t *testing.T) {
	// Through the real serve path and a real client: a chunked body
	// over the cap is refused, one at the cap runs.
	isolateHome(t)
	const limit = 512
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0", MaxBodyBytes: limit}))
	base, stop := serveAPI(t, r)
	defer stop()

	send := func(body io.Reader) (int, []byte) {
		req, err := http.NewRequest(http.MethodPost, base+"/v1/commands/add", body)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}

	status, body := send(unsizedBody{strings.NewReader(addBody(t, limit))})
	assert.Equal(t, http.StatusOK, status, string(body))

	status, body = send(unsizedBody{strings.NewReader(addBody(t, 64*limit))})
	requireTooLarge(t, status, body)

	status, body = send(strings.NewReader(addBody(t, limit+1)))
	requireTooLarge(t, status, body)
}
