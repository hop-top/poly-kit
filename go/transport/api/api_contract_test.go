package api_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// apiContract is the part of contracts/parity/api.json the Go tests
// run beyond the OpenAPI cases (see openapi_contract_test.go). The Rust
// port runs the same tables; Go is the authority.
type apiContract struct {
	Constants struct {
		Health struct {
			Liveness  string `json:"liveness"`
			Readiness string `json:"readiness"`
		} `json:"health"`
		Topics               map[string]string `json:"topics"`
		DefaultMaxBodyBytes  int64             `json:"default_max_body_bytes"`
		DefaultCSP           string            `json:"default_csp"`
		DefaultHSTSSeconds   int64             `json:"default_hsts_max_age_seconds"`
		DefaultAuthChallenge string            `json:"default_auth_challenge"`
		RequestIDPattern     string            `json:"request_id_pattern"`
		MintedIDPattern      string            `json:"minted_request_id_pattern"`
		Codes                map[string]int    `json:"codes"`
	} `json:"constants"`
	ClientAddress []struct {
		Name    string              `json:"name"`
		Trusted []string            `json:"trusted"`
		Peer    string              `json:"peer"`
		Headers map[string][]string `json:"headers"`
		Want    struct {
			Client string `json:"client"`
			Peer   string `json:"peer"`
			HTTPS  bool   `json:"https"`
		} `json:"want"`
	} `json:"client_address"`
	TrustedProxies struct {
		Parse struct {
			Input     []string `json:"input"`
			Canonical []string `json:"canonical"`
		} `json:"parse"`
		Invalid []string `json:"invalid"`
	} `json:"trusted_proxies"`
	Hosts []struct {
		Name      string   `json:"name"`
		Allow     []string `json:"allow"`
		Host      string   `json:"host"`
		Permitted bool     `json:"permitted"`
	} `json:"hosts"`
	ListenerHosts []struct {
		Addr     string   `json:"addr"`
		Hosts    []string `json:"hosts"`
		Wildcard bool     `json:"wildcard"`
	} `json:"listener_hosts"`
	Origin []struct {
		Name      string            `json:"name"`
		Allow     []string          `json:"allow"`
		Method    string            `json:"method"`
		Host      string            `json:"host"`
		Headers   map[string]string `json:"headers"`
		Permitted bool              `json:"permitted"`
		Message   string            `json:"message"`
	} `json:"origin"`
	OriginInvalid []string `json:"origin_invalid"`
	RequestID     []struct {
		Name   string  `json:"name"`
		Header *string `json:"header"`
		Kept   bool    `json:"kept"`
	} `json:"request_id"`
}

func readAPIContract(t *testing.T) apiContract {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(apiContractPath))
	require.NoError(t, err)
	var c apiContract
	require.NoError(t, json.Unmarshal(raw, &c))
	return c
}

func TestAPIContractConstants(t *testing.T) {
	c := readAPIContract(t)
	assert.Equal(t, "/healthz", c.Constants.Health.Liveness)
	assert.Equal(t, api.LivenessPath, c.Constants.Health.Liveness)
	assert.Equal(t, api.ReadinessPath, c.Constants.Health.Readiness)
	assert.EqualValues(t, "kit.api.request.started", api.DefaultTopics.RequestStart)
	assert.Equal(t, string(api.DefaultTopics.RequestStart), c.Constants.Topics["request.started"])
	assert.Equal(t, string(api.DefaultTopics.RequestEnd), c.Constants.Topics["request.ended"])
	assert.Equal(t, api.DefaultMaxBodyBytes, c.Constants.DefaultMaxBodyBytes)
	assert.Equal(t, api.DefaultContentSecurityPolicy, c.Constants.DefaultCSP)
	assert.EqualValues(t, api.DefaultHSTSMaxAge.Seconds(), c.Constants.DefaultHSTSSeconds)
	assert.Equal(t, api.DefaultAuthChallenge, c.Constants.DefaultAuthChallenge)
	assert.Equal(t, c.Constants.Codes["host_rejected"], http.StatusForbidden)
	assert.Equal(t, c.Constants.Codes["body_too_large"], http.StatusRequestEntityTooLarge)
	assert.Equal(t, c.Constants.Codes["unauthenticated"], http.StatusUnauthorized)
	assert.Equal(t, c.Constants.Codes["rate_limited"], http.StatusTooManyRequests)
	assert.Equal(t, api.CodeHostRejected, "host_rejected")
	assert.Equal(t, api.CodeOriginRejected, "origin_rejected")
	assert.Equal(t, api.CodeBodyTooLarge, "body_too_large")
	assert.Equal(t, api.CodeUnauthenticated, "unauthenticated")
	assert.Equal(t, api.CodeRateLimited, "rate_limited")
}

func TestAPIContractClientAddress(t *testing.T) {
	for _, tt := range readAPIContract(t).ClientAddress {
		t.Run(tt.Name, func(t *testing.T) {
			prefixes, err := api.ParseTrustedProxies(tt.Trusted)
			require.NoError(t, err)
			var remote, peer string
			var https bool
			h := api.ClientAddress(api.ClientAddressConfig{TrustedProxies: prefixes})(http.HandlerFunc(
				func(_ http.ResponseWriter, r *http.Request) {
					remote, peer, https = r.RemoteAddr, api.PeerAddrFromContext(r.Context()), api.IsHTTPS(r)
				}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.Peer
			for k, vs := range tt.Headers {
				for _, v := range vs {
					req.Header.Add(k, v)
				}
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			host, _, err := net.SplitHostPort(remote)
			if err != nil {
				host = remote
			}
			assert.Equal(t, tt.Want.Client, host)
			assert.Equal(t, tt.Want.Peer, peer)
			assert.Equal(t, tt.Want.HTTPS, https)
		})
	}
}

func TestAPIContractTrustedProxies(t *testing.T) {
	c := readAPIContract(t).TrustedProxies
	got, err := api.ParseTrustedProxies(c.Parse.Input)
	require.NoError(t, err)
	var s []string
	for _, p := range got {
		s = append(s, p.String())
	}
	assert.Equal(t, c.Parse.Canonical, s)
	for _, bad := range c.Invalid {
		_, err := api.ParseTrustedProxies([]string{bad})
		assert.ErrorContains(t, err, bad)
	}
}

func TestAPIContractHosts(t *testing.T) {
	c := readAPIContract(t)
	for _, tt := range c.Hosts {
		t.Run(tt.Name, func(t *testing.T) {
			reached := false
			h := api.HostCheck(api.HostCheckConfig{Allow: tt.Allow})(http.HandlerFunc(
				func(http.ResponseWriter, *http.Request) { reached = true }))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tt.Host
			h.ServeHTTP(httptest.NewRecorder(), req)
			assert.Equal(t, tt.Permitted, reached)
		})
	}
	for _, tt := range c.ListenerHosts {
		t.Run("listener "+tt.Addr, func(t *testing.T) {
			hosts, wildcard := api.ListenerHosts(tt.Addr)
			assert.Equal(t, tt.Wildcard, wildcard)
			if len(tt.Hosts) == 0 {
				assert.Empty(t, hosts)
			} else {
				assert.Equal(t, tt.Hosts, hosts)
			}
		})
	}
}

func TestAPIContractOrigin(t *testing.T) {
	c := readAPIContract(t)
	for _, tt := range c.Origin {
		t.Run(tt.Name, func(t *testing.T) {
			mw, err := api.OriginCheck(api.OriginCheckConfig{Allow: tt.Allow})
			require.NoError(t, err)
			reached := false
			h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
			req := httptest.NewRequest(tt.Method, "/", nil)
			req.Host = tt.Host
			for k, v := range tt.Headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, tt.Permitted, reached)
			if !tt.Permitted {
				assert.Equal(t, http.StatusForbidden, rec.Code)
				var body api.APIError
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				assert.Equal(t, api.CodeOriginRejected, body.Code)
				assert.Equal(t, tt.Message, body.Message)
			}
		})
	}
	for _, bad := range c.OriginInvalid {
		_, err := api.OriginCheck(api.OriginCheckConfig{Allow: []string{bad}})
		assert.Error(t, err, bad)
	}
}

func TestAPIContractRequestID(t *testing.T) {
	c := readAPIContract(t)
	require.NotEmpty(t, c.RequestID)
	accepted := regexp.MustCompile(c.Constants.RequestIDPattern)
	minted := regexp.MustCompile(c.Constants.MintedIDPattern)
	for _, tt := range c.RequestID {
		t.Run(tt.Name, func(t *testing.T) {
			var seen string
			h := api.RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = api.GetRequestID(r)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.Header != nil {
				req.Header["X-Request-Id"] = []string{*tt.Header}
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			echoed := rec.Header().Get("X-Request-ID")
			assert.Equal(t, seen, echoed)
			if tt.Kept {
				assert.Equal(t, *tt.Header, echoed)
				return
			}
			assert.Regexp(t, minted, echoed)
			assert.Regexp(t, accepted, echoed)
			if tt.Header != nil {
				assert.NotEqual(t, *tt.Header, echoed)
			}
		})
	}
}
