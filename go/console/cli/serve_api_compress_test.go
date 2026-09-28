package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// compressFixture is configFixture plus a route whose JSON body is
// well above the default threshold, with keys applied to its viper.
func compressFixture(t *testing.T, cfg APIConfig, keys map[string]any) *Root {
	t.Helper()
	big := `{"items":"` + strings.Repeat("x", 4096) + `"}`
	cfg.Handlers = func(r *api.Router) {
		r.Handle(http.MethodGet, "/big", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(big))
		})
	}
	r := configFixture(t, cfg)
	for k, v := range keys {
		r.Viper.Set(k, v)
	}
	return r
}

func encodingOf(t *testing.T, h http.Handler, path string, hdr http.Header) (int, string) {
	t.Helper()
	req := loopbackRequest(http.MethodGet, path, nil)
	for k, v := range hdr {
		req.Header[k] = v
	}
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Header().Get("Content-Encoding")
}

func TestAPICompressionConfig(t *testing.T) {
	const (
		addr      = "services.api.addr"
		enabled   = "services.api.compression.enabled"
		minB      = "services.api.compression.min_bytes"
		allOn     = "services.all.compression.enabled"
		allMinB   = "services.all.compression.min_bytes"
		loopback  = "127.0.0.1:0"
		wildcard  = ":0"
		gzipCE    = "gzip"
		identity  = ""
		hugeFloor = 1 << 20
	)
	cases := []struct {
		name string
		keys map[string]any
		want string
	}{
		{name: "loopback defaults off", keys: map[string]any{addr: loopback}, want: identity},
		{name: "beyond loopback defaults off", keys: map[string]any{addr: wildcard}, want: identity},
		{name: "service key enables", keys: map[string]any{enabled: true}, want: gzipCE},
		{name: "services.all enables", keys: map[string]any{allOn: true}, want: gzipCE},
		{name: "service key beats services.all", keys: map[string]any{allOn: true, enabled: false}, want: identity},
		{name: "threshold above body", keys: map[string]any{enabled: true, minB: hugeFloor}, want: identity},
		{name: "services.all threshold applies", keys: map[string]any{enabled: true, allMinB: hugeFloor}, want: identity},
		{name: "service threshold beats services.all", keys: map[string]any{enabled: true, allMinB: hugeFloor, minB: 16}, want: gzipCE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := projectionHandler(t, compressFixture(t, APIConfig{}, tc.keys))
			code, ce := encodingOf(t, h, "/big", nil)
			require.Equal(t, http.StatusOK, code)
			assert.Equal(t, tc.want, ce)
		})
	}
}

// TestAPICompressionSlot pins the chain position: compression sits
// outside authentication, so with a low threshold even the refusal
// authentication writes goes through the encoder.
func TestAPICompressionSlot(t *testing.T) {
	cfg := APIConfig{Auth: func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") != "Bearer ok" {
			return nil, errors.New("no")
		}
		return api.Claims{Subject: "u"}, nil
	}}
	h := projectionHandler(t, compressFixture(t, cfg, map[string]any{
		"services.api.compression.enabled":   true,
		"services.api.compression.min_bytes": 16,
	}))

	code, ce := encodingOf(t, h, "/big", http.Header{"Authorization": {"Bearer ok"}})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "gzip", ce)

	code, ce = encodingOf(t, h, "/big", nil)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "gzip", ce, "the auth refusal is written inside the compression slot")
}

func TestAPICompressionValidation(t *testing.T) {
	cases := map[string]map[string]any{
		"negative threshold":           {"services.api.compression.min_bytes": -1},
		"unknown key in service block": {"services.api.compression.level": 9},
		"unknown key in services.all":  {"services.all.compression.min_byte": 10},
	}
	for name, keys := range cases {
		t.Run(name, func(t *testing.T) {
			keys["services.api.addr"] = "127.0.0.1:0"
			r := compressFixture(t, APIConfig{}, keys)
			svc, ok := r.serveReg.Lookup(APIServiceName)
			require.True(t, ok)
			err := svc.(*apiService).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "compression")
		})
	}

	t.Run("known keys pass", func(t *testing.T) {
		r := compressFixture(t, APIConfig{}, map[string]any{
			"services.api.addr":                  "127.0.0.1:0",
			"services.api.compression.enabled":   true,
			"services.all.compression.min_bytes": 0,
		})
		svc, _ := r.serveReg.Lookup(APIServiceName)
		require.NoError(t, svc.(*apiService).Validate())
	})
}
