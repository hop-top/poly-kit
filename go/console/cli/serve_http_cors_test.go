package cli

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/transport/api"
)

const (
	corsApp   = "https://app.example"
	corsOther = "https://other.example"
)

// corsDo sends one request to the api service from origin, as a browser
// page on origin would, with extra headers.
func corsDo(t *testing.T, method, url, origin string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	require.NoError(t, err)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// preflight is a browser's CORS preflight for method with a JSON body.
func preflight(t *testing.T, url, origin, method string) *http.Response {
	t.Helper()
	return corsDo(t, http.MethodOptions, url, origin, map[string]string{
		"Access-Control-Request-Method":  method,
		"Access-Control-Request-Headers": "authorization,content-type",
	})
}

// accessControl is every Access-Control-* header of resp.
func accessControl(resp *http.Response) http.Header {
	out := http.Header{}
	for k, v := range resp.Header {
		if strings.HasPrefix(k, "Access-Control-") {
			out[k] = v
		}
	}
	return out
}

// varies reports whether resp's Vary names field.
func varies(resp *http.Response, field string) bool {
	for _, v := range resp.Header.Values("Vary") {
		for _, f := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(f), field) {
				return true
			}
		}
	}
	return false
}

// corsAPI serves the api service with the given configuration and
// returns its base URL; auth, when set, is its verifier.
func corsAPI(t *testing.T, set map[string]any, auth ...api.AuthFunc) string {
	t.Helper()
	r := guardRoot(t, set)
	if len(auth) > 0 {
		r = authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0", Auth: auth[0]}))
		for k, v := range set {
			r.Viper.Set(k, v)
		}
	}
	base, stop := serveAPI(t, r)
	t.Cleanup(stop)
	return base
}

// Slot 9 is off by default: no CORS header, and a preflight reaches
// the router like any OPTIONS.
func TestAPICORS_OffByDefault(t *testing.T) {
	base := corsAPI(t, nil)
	resp := preflight(t, base+"/v1/commands/add", corsApp, http.MethodPost)
	assert.Empty(t, accessControl(resp))
	resp = corsDo(t, http.MethodGet, base+"/v1/commands", corsApp, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, accessControl(resp))
}

func TestAPICORS_Preflight(t *testing.T) {
	base := corsAPI(t, map[string]any{
		"services.api.cors.allow_origins": []string{corsApp},
		"services.api.cors.max_age":       "10m",
	})

	t.Run("a listed origin is answered", func(t *testing.T) {
		resp := preflight(t, base+"/v1/commands/add", corsApp, http.MethodPost)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
		assert.Equal(t, corsApp, resp.Header.Get("Access-Control-Allow-Origin"))
		assert.Equal(t, http.MethodPost, resp.Header.Get("Access-Control-Allow-Methods"))
		assert.Equal(t, "authorization,content-type", resp.Header.Get("Access-Control-Allow-Headers"))
		assert.Equal(t, "600", resp.Header.Get("Access-Control-Max-Age"))
		assert.Empty(t, resp.Header.Get("Access-Control-Allow-Credentials"))
		assert.True(t, varies(resp, "Origin"))
		assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"), "slot 6 wraps slot 9")
	})

	t.Run("an unlisted origin gets no grant", func(t *testing.T) {
		resp := preflight(t, base+"/v1/commands/add", corsOther, http.MethodPost)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
		assert.Empty(t, accessControl(resp))
		assert.True(t, varies(resp, "Origin"))
	})

	t.Run("a method outside allow_methods gets no grant", func(t *testing.T) {
		resp := preflight(t, base+"/v1/commands/add", corsApp, "PROPFIND")
		assert.Empty(t, accessControl(resp))
	})

	t.Run("a header outside allow_headers gets no grant", func(t *testing.T) {
		resp := corsDo(t, http.MethodOptions, base+"/v1/commands/add", corsApp, map[string]string{
			"Access-Control-Request-Method":  http.MethodPost,
			"Access-Control-Request-Headers": "x-not-listed",
		})
		assert.Empty(t, accessControl(resp))
	})

	t.Run("the Host check still runs first", func(t *testing.T) {
		port := base[strings.LastIndex(base, ":")+1:]
		resp := guardDo(t, http.MethodOptions, base+"/v1/commands/add", "attacker.example:"+port, corsApp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		assert.Empty(t, accessControl(resp))
	})
}

// A preflight carries no credentials, so it is answered ahead of
// authentication; the request after it is authenticated as always,
// and a refusal is readable by the granted page.
func TestAPICORS_PreflightNeedsNoCredentials(t *testing.T) {
	base := corsAPI(t, map[string]any{"services.api.cors.allow_origins": []string{corsApp}},
		func(r *http.Request) (any, error) {
			if r.Header.Get("Authorization") != "Bearer ok" {
				return nil, errors.New("no")
			}
			return "caller", nil
		})

	resp := preflight(t, base+"/v1/commands/add", corsApp, http.MethodPost)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, corsApp, resp.Header.Get("Access-Control-Allow-Origin"))

	resp = corsDo(t, http.MethodGet, base+"/v1/commands", corsApp, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, corsApp, resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Contains(t, resp.Header.Get("Access-Control-Expose-Headers"), "Www-Authenticate")

	resp = corsDo(t, http.MethodGet, base+"/v1/commands", corsApp, map[string]string{"Authorization": "Bearer ok"})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAPICORS_SimpleRequest(t *testing.T) {
	base := corsAPI(t, map[string]any{"services.api.cors.allow_origins": []string{corsApp}})

	t.Run("a listed origin may read", func(t *testing.T) {
		resp := corsDo(t, http.MethodGet, base+"/v1/commands", corsApp, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, corsApp, resp.Header.Get("Access-Control-Allow-Origin"))
		exposed := resp.Header.Get("Access-Control-Expose-Headers")
		for _, h := range []string{"X-Request-Id", "Etag", "Retry-After", "Idempotent-Replayed"} {
			assert.Contains(t, exposed, h)
		}
		assert.Empty(t, resp.Header.Get("Access-Control-Allow-Credentials"))
		assert.True(t, varies(resp, "Origin"))
	})

	t.Run("an unlisted origin may not", func(t *testing.T) {
		resp := corsDo(t, http.MethodGet, base+"/v1/commands", corsOther, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode, "CORS decides reading, not serving")
		assert.Empty(t, accessControl(resp))
		assert.True(t, varies(resp, "Origin"), "a cache must not serve one origin's answer to another")
	})

	t.Run("a same-origin or non-browser request is untouched", func(t *testing.T) {
		resp := corsDo(t, http.MethodGet, base+"/v1/commands", "", nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Empty(t, accessControl(resp))
	})
}

func TestAPICORS_Credentials(t *testing.T) {
	base := corsAPI(t, map[string]any{
		"services.api.cors.allow_origins":     []string{corsApp},
		"services.api.cors.allow_credentials": true,
	})
	resp := preflight(t, base+"/v1/commands/add", corsApp, http.MethodPost)
	assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, corsApp, resp.Header.Get("Access-Control-Allow-Origin"), "never * with credentials")

	resp = corsDo(t, http.MethodGet, base+"/v1/commands", corsApp, nil)
	assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))

	resp = corsDo(t, http.MethodGet, base+"/v1/commands", corsOther, nil)
	assert.Empty(t, accessControl(resp), "credentials are granted to listed origins only")
}

// "*" grants reading to every page, without credentials; it never
// widens the Origin check.
func TestAPICORS_Wildcard(t *testing.T) {
	base := corsAPI(t, map[string]any{"services.api.cors.allow_origins": "*"})

	resp := preflight(t, base+"/v1/commands/add", corsOther, http.MethodPost)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))

	resp = corsDo(t, http.MethodGet, base+"/v1/commands", corsOther, nil)
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Credentials"))

	resp = corsDo(t, http.MethodPost, base+"/v1/commands/add", corsOther, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, api.CodeOriginRejected, guardCode(t, resp))
}

// An origin the cors block grants by name passes the Origin check
// (slot 8) too; every other cross-origin write is still refused, and
// switching cors off takes the admission away.
func TestAPICORS_OriginCheckAdmitsListedOrigins(t *testing.T) {
	base := corsAPI(t, map[string]any{
		"services.api.cors.allow_origins": []string{"HTTPS://App.Example:443"},
	})
	resp := corsDo(t, http.MethodPost, base+"/v1/commands/add", corsApp, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the origin is compared as a browser sends it")
	assert.Equal(t, corsApp, resp.Header.Get("Access-Control-Allow-Origin"))

	resp = corsDo(t, http.MethodPost, base+"/v1/commands/add", corsOther, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, api.CodeOriginRejected, guardCode(t, resp))

	base = corsAPI(t, map[string]any{
		"services.api.cors.allow_origins": []string{corsApp},
		"services.api.cors.enabled":       false,
	})
	resp = corsDo(t, http.MethodPost, base+"/v1/commands/add", corsApp, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Empty(t, accessControl(resp))
}

// services.all.cors is every HTTP listener's default, and the
// service's key replaces the shared one.
func TestAPICORS_SharedBlock(t *testing.T) {
	base := corsAPI(t, map[string]any{
		"services.all.cors.allow_origins": []string{corsOther},
		"services.api.cors.allow_origins": []string{corsApp},
		"services.all.cors.allow_methods": "GET POST",
	})
	resp := preflight(t, base+"/v1/commands/add", corsApp, http.MethodPost)
	assert.Equal(t, corsApp, resp.Header.Get("Access-Control-Allow-Origin"))
	resp = preflight(t, base+"/v1/commands/add", corsOther, http.MethodPost)
	assert.Empty(t, accessControl(resp), "lists replace, never merge")
	resp = preflight(t, base+"/v1/commands/add", corsApp, http.MethodDelete)
	assert.Empty(t, accessControl(resp), "the shared methods replace the api's")
}

func TestAPICORS_ConfigErrors(t *testing.T) {
	cases := map[string]struct {
		set  map[string]any
		want string
	}{
		"credentials with *": {
			map[string]any{"services.api.cors.allow_origins": "*", "services.api.cors.allow_credentials": true},
			`services.api.cors.allow_credentials: credentials cannot be allowed with the "*" origin`,
		},
		"credentials with * under all": {
			map[string]any{"services.all.cors.allow_origins": "*", "services.api.cors.allow_credentials": true},
			"in services.all.cors.allow_origins",
		},
		"origin without scheme": {
			map[string]any{"services.api.cors.allow_origins": []string{"app.example"}},
			`services.api.cors.allow_origins: origin "app.example"`,
		},
		"origin with a path": {
			map[string]any{"services.api.cors.allow_origins": []string{"https://app.example/"}},
			"path, query and fragment are not part of an origin",
		},
		"origin pattern": {
			map[string]any{"services.api.cors.allow_origins": []string{"https://*.example"}},
			"a wildcard matches nothing",
		},
		"null origin": {
			map[string]any{"services.api.cors.allow_origins": []string{"null"}},
			`origin "null"`,
		},
		"* beside origins": {
			map[string]any{"services.api.cors.allow_origins": []string{"*", corsApp}},
			`"*" grants every origin; list it alone`,
		},
		"on with no origin": {
			map[string]any{"services.api.cors.enabled": true},
			"services.api.cors.enabled: cors is on and grants no origin",
		},
		"on under all with no origin": {
			map[string]any{"services.all.cors.enabled": true},
			"services.all.cors.enabled: cors is on and grants no origin",
		},
		"method not a token": {
			map[string]any{"services.api.cors.allow_origins": corsApp, "services.api.cors.allow_methods": []string{"GE(T"}},
			"services.api.cors.allow_methods",
		},
		"header not a name": {
			map[string]any{"services.api.cors.allow_origins": corsApp, "services.api.cors.allow_headers": []string{"X:Y"}},
			"services.api.cors.allow_headers",
		},
		"negative max age": {
			map[string]any{"services.api.cors.allow_origins": corsApp, "services.api.cors.max_age": "-1s"},
			"services.api.cors.max_age",
		},
		"max age not a duration": {
			map[string]any{"services.api.cors.allow_origins": corsApp, "services.api.cors.max_age": 600},
			"services.api.cors.max_age",
		},
		"credentials not a bool": {
			map[string]any{"services.api.cors.allow_origins": corsApp, "services.api.cors.allow_credentials": "maybe"},
			"services.api.cors.allow_credentials",
		},
		"list where a map belongs": {
			map[string]any{"services.api.cors.allow_origins": map[string]any{"a": 1}},
			"services.api.cors.allow_origins",
		},
		"unknown key": {
			map[string]any{"services.api.cors.allowed_origins": []string{corsApp}},
			`unknown key "allowed_origins"`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := apiSvc(t, guardRoot(t, c.set)).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}

	t.Run("off, the grant is not checked", func(t *testing.T) {
		err := apiSvc(t, guardRoot(t, map[string]any{
			"services.api.cors.enabled":       false,
			"services.api.cors.allow_origins": []string{"not an origin"},
		})).Validate()
		assert.NoError(t, err)
	})
}

// The block acts on an HTTP listener alone: under the socket service it
// is refused, not ignored.
func TestCORSBlockIsHTTPOnly(t *testing.T) {
	assert.True(t, svcconfig.HTTPOnly(corsBlock))
	r := guardRoot(t, map[string]any{"services.socket.cors.allow_origins": []string{corsApp}})
	err := svcconfig.New(r.Viper).ValidateNoHTTP("socket")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services.socket.cors")
}
