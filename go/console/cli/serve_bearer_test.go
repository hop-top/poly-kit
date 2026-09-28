package cli

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/transport/cmdsurface"
)

// bearerRoot is authRoot with an identity keypair of its own, a
// `secret` command that requires an authenticated caller, and keys.
func bearerRoot(t *testing.T, keys map[string]any, opts ...func(*Root)) *Root {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	opts = append([]func(*Root){WithIdentity(IdentityConfig{Dir: filepath.Join(home, "identity")})}, opts...)
	r := authRoot(t, opts...)
	r.Cmd.AddCommand(&cobra.Command{
		Use:   "secret",
		Short: "a secret",
		RunE:  func(cmd *cobra.Command, _ []string) error { cmd.Print("unlocked"); return nil },
		Annotations: map[string]string{
			"kit/side-effect":   "read",
			"kit/auth-required": "true",
		},
	})
	setKeys(r, keys)
	return r
}

// identityToken is a token r's identity keypair signed.
func identityToken(t *testing.T, r *Root, c identity.Claims) string {
	t.Helper()
	require.NotNil(t, r.Identity, "the root has an identity keypair")
	now := time.Now()
	if c.Subject == "" {
		c.Subject = "alice"
	}
	if c.IssuedAt == 0 {
		c.IssuedAt = now.Unix()
	}
	if c.ExpiresAt == 0 {
		c.ExpiresAt = now.Add(time.Hour).Unix()
	}
	raw, err := r.Identity.SignJWT(c)
	require.NoError(t, err)
	return raw
}

func bearerHdr(tok string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + tok}
}

// TestAPIBearerJWTMode pins services.api.auth.mode: jwt on the api
// service: a token the tool's identity signed is an established
// caller for kit/auth-required, attributed to its sub and tenant with
// its scopes; no token, an expired one and one for another audience
// are 401 unauthenticated and audited; and the mode replaces the code
// AuthFunc, which is never consulted.
func TestAPIBearerJWTMode(t *testing.T) {
	rec := &auditRecorder{}
	codeAuthCalls := 0
	codeAuth := func(*http.Request) (any, error) {
		codeAuthCalls++
		return nil, errors.New("code auth refuses everything")
	}
	r := bearerRoot(t, map[string]any{
		"services.api.auth.mode":         "jwt",
		"services.api.auth.jwt.audience": "kit-api",
	}, WithAPI(APIConfig{Addr: "127.0.0.1:0", Auth: codeAuth}), WithAuditSinks(rec.spec()))
	base, stop := serveAPI(t, r)
	defer stop()

	good := identityToken(t, r, identity.Claims{Tenant: "acme", Scopes: []string{"items:read"},
		Audience: identity.Audience{"kit-api"}})
	resp, body := get(t, base+"/v1/commands/secret", bearerHdr(good))
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	assert.Contains(t, string(body), "unlocked")
	inv, _, err := rec.last(t)
	require.NoError(t, err)
	assert.Equal(t, cmdsurface.EstablishedVerified, inv.Meta.Established)
	assert.Equal(t, "alice", inv.Meta.Caller)
	assert.Equal(t, "acme", inv.Meta.Tenant)
	assert.Equal(t, "items:read", inv.Meta.Extra["scopes"])
	assert.Zero(t, codeAuthCalls, "a configured mode replaces the code AuthFunc")

	for name, hdr := range map[string]map[string]string{
		"no token": nil,
		"expired": bearerHdr(identityToken(t, r, identity.Claims{Audience: identity.Audience{"kit-api"},
			IssuedAt: time.Now().Add(-3 * time.Hour).Unix(), ExpiresAt: time.Now().Add(-2 * time.Hour).Unix()})),
		"another audience": bearerHdr(identityToken(t, r, identity.Claims{Audience: identity.Audience{"billing"}})),
		"another signer":   bearerHdr(otherIdentityToken(t)),
	} {
		t.Run(name, func(t *testing.T) {
			before := rec.count()
			resp, body := get(t, base+"/v1/commands/list", hdr)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, string(body))
			assert.Equal(t, "Bearer", resp.Header.Get("WWW-Authenticate"))
			assert.Contains(t, string(body), `"unauthenticated"`)
			assert.Equal(t, before+1, rec.count(), "the refusal is audited")
			_, _, err := rec.last(t)
			assert.ErrorIs(t, err, cmdsurface.ErrAuthRefused)
		})
	}
	assert.Zero(t, codeAuthCalls)
}

func otherIdentityToken(t *testing.T) string {
	t.Helper()
	kp, err := identity.Generate()
	require.NoError(t, err)
	now := time.Now()
	raw, err := kp.SignJWT(identity.Claims{Subject: "mallory", Audience: identity.Audience{"kit-api"},
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	require.NoError(t, err)
	return raw
}

// TestAPIBearerModeAuthenticatesForExposure pins that a bearer mode
// satisfies the non-loopback authentication rule, as Auth and mtls do.
func TestAPIBearerModeAuthenticatesForExposure(t *testing.T) {
	r := bearerRoot(t, map[string]any{"services.api.auth.mode": "jwt"},
		WithAPI(APIConfig{Addr: "0.0.0.0:0", InsecureNoPolicy: true}))
	base, stop := serveAPI(t, r)
	defer stop()
	resp, body := get(t, loopbackHTTP(t, base)+"/v1/commands/list", bearerHdr(identityToken(t, r, identity.Claims{})))
	assert.Equal(t, http.StatusOK, resp.StatusCode, string(body))
}

// loopbackHTTP rewrites a wildcard-bound base URL to loopback.
func loopbackHTTP(t *testing.T, base string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(base, "http://"))
	require.NoError(t, err)
	return "http://127.0.0.1:" + port
}

// jwksServer serves one RSA key as a JWKS and an OpenID discovery
// document naming itself as issuer. It is the in-test key server.
func jwksServer(t *testing.T) (*httptest.Server, *rsa.PrivateKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &priv.PublicKey, KeyID: "r1", Algorithm: "RS256", Use: "sig"},
		}})
	})
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": srv.URL, "jwks_uri": srv.URL + "/jwks.json"})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, priv
}

func rsaToken(t *testing.T, priv *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: priv},
		(&jose.SignerOptions{}).WithHeader("kid", "r1"))
	require.NoError(t, err)
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	require.NoError(t, err)
	return raw
}

// TestAPIBearerRemoteModes pins jwks and oidc end to end on the api
// service against the in-test key server, and services.all as the
// shared default the service's own key overrides.
func TestAPIBearerRemoteModes(t *testing.T) {
	srv, priv := jwksServer(t)
	now := time.Now()
	claims := map[string]any{"sub": "svc-a", "aud": "kit-api", "tenant": "acme", "scope": "items:read items:write",
		"iss": srv.URL, "exp": now.Add(time.Hour).Unix()}

	for name, keys := range map[string]map[string]any{
		"jwks": {
			"services.api.auth.mode":          "jwks",
			"services.api.auth.jwks.url":      srv.URL + "/jwks.json",
			"services.api.auth.jwks.audience": "kit-api",
			"services.api.auth.jwks.issuer":   srv.URL,
		},
		"oidc from services.all": {
			"services.all.auth.mode":          "oidc",
			"services.all.auth.oidc.issuer":   srv.URL,
			"services.all.auth.oidc.audience": "kit-api",
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &auditRecorder{}
			r := bearerRoot(t, keys, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), WithAuditSinks(rec.spec()))
			base, stop := serveAPI(t, r)
			defer stop()

			resp, body := get(t, base+"/v1/commands/secret", bearerHdr(rsaToken(t, priv, claims)))
			require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
			inv, _, err := rec.last(t)
			require.NoError(t, err)
			assert.Equal(t, "svc-a", inv.Meta.Caller)
			assert.Equal(t, "acme", inv.Meta.Tenant)
			assert.Equal(t, "items:read,items:write", inv.Meta.Extra["scopes"])

			wrongIss := map[string]any{}
			for k, v := range claims {
				wrongIss[k] = v
			}
			wrongIss["iss"] = "https://elsewhere.test"
			resp, body = get(t, base+"/v1/commands/list", bearerHdr(rsaToken(t, priv, wrongIss)))
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, string(body))
		})
	}

	t.Run("the service's own mode overrides services.all", func(t *testing.T) {
		r := bearerRoot(t, map[string]any{
			"services.all.auth.mode":          "jwks",
			"services.all.auth.jwks.url":      srv.URL + "/jwks.json",
			"services.all.auth.jwks.audience": "kit-api",
			"services.api.auth.mode":          "jwt",
		}, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
		v, err := ResolveServeVerifier(r, APIServiceName)
		require.NoError(t, err, "the shared jwks block is a default the api service does not use")
		_, err = v.Verify(t.Context(), identityToken(t, r, identity.Claims{}))
		assert.NoError(t, err, "the api service verifies with its own jwt mode")
		_, err = v.Verify(t.Context(), rsaToken(t, priv, claims))
		assert.Error(t, err, "not with the shared jwks")

		// Under the shared mode, a shared block of another mode is a
		// leftover, and refused.
		r.Viper.Set("services.api.auth.mode", nil)
		r.Viper.Set("services.all.auth.jwt.issuer", "me")
		_, err = ResolveServeTLS(r, "rpc")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `services.all.auth.jwt.issuer: set, but services.rpc.auth.mode is not "jwt"`)
	})
}

// TestServeBearerConfigErrors pins every refusal at the configuration
// gate, exit 2, naming the key.
func TestServeBearerConfigErrors(t *testing.T) {
	dir := t.TempDir()
	notPEM := filepath.Join(dir, "not.pem")
	require.NoError(t, os.WriteFile(notPEM, []byte("nope"), 0o600))

	cases := []struct {
		name     string
		keys     map[string]any
		identity bool
		want     string
	}{
		{"unknown mode lists the modes", map[string]any{"services.api.auth.mode": "kerberos"}, true,
			`services.api.auth.mode: unknown mode "kerberos"; kit supports "mtls", "jwt", "jwks", "oidc"`},
		{"jwt with no key", map[string]any{"services.api.auth.mode": "jwt"}, false,
			`services.api.auth.mode: auth.mode "jwt" has no key to verify with`},
		{"jwt key file missing", map[string]any{"services.api.auth.mode": "jwt",
			"services.api.auth.jwt.public_key_files": filepath.Join(dir, "gone.pem")}, true,
			"services.api.auth.jwt.public_key_files:"},
		{"jwt key file not PEM", map[string]any{"services.api.auth.mode": "jwt",
			"services.api.auth.jwt.public_key_files": notPEM}, true, "no PEM block"},
		{"bad clock skew", map[string]any{"services.api.auth.mode": "jwt",
			"services.api.auth.jwt.clock_skew": "soon"}, true, "services.api.auth.jwt.clock_skew:"},
		{"negative clock skew", map[string]any{"services.api.auth.mode": "jwt",
			"services.api.auth.jwt.clock_skew": "-1s"}, true, "must not be negative"},
		{"unknown jwt key", map[string]any{"services.api.auth.mode": "jwt",
			"services.api.auth.jwt.key": "x"}, true, `unknown key "key"`},
		{"jwks without url", map[string]any{"services.api.auth.mode": "jwks"}, true,
			"services.api.auth.jwks.url: auth.mode \"jwks\" needs"},
		{"jwks over plain http", map[string]any{"services.api.auth.mode": "jwks",
			"services.api.auth.jwks.url": "http://idp.example/jwks", "services.api.auth.jwks.audience": "a"}, true,
			"plain http is accepted only to a loopback host"},
		{"jwks without audience", map[string]any{"services.api.auth.mode": "jwks",
			"services.api.auth.jwks.url": "https://idp.example/jwks"}, true, "services.api.auth.jwks.audience: required"},
		{"jwks bad refresh", map[string]any{"services.api.auth.mode": "jwks",
			"services.api.auth.jwks.url": "https://idp.example/jwks", "services.api.auth.jwks.audience": "a",
			"services.api.auth.jwks.refresh": "0s"}, true, "services.api.auth.jwks.refresh: must be positive"},
		{"oidc without issuer", map[string]any{"services.api.auth.mode": "oidc"}, true,
			"services.api.auth.oidc.issuer: auth.mode \"oidc\" needs"},
		{"oidc without audience", map[string]any{"services.api.auth.mode": "oidc",
			"services.api.auth.oidc.issuer": "https://idp.example"}, true, "services.api.auth.oidc.audience: required"},
		{"a block under another mode", map[string]any{"services.api.auth.mode": "jwt",
			"services.api.auth.oidc.issuer": "https://idp.example"}, true,
			`services.api.auth.oidc.issuer: set, but services.api.auth.mode is not "oidc"`},
		{"a block with no mode", map[string]any{"services.api.auth.jwt.issuer": "me"}, true,
			`services.api.auth.jwt.issuer: set, but services.api.auth.mode is not "jwt"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r *Root
			if tc.identity {
				r = bearerRoot(t, tc.keys, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
			} else {
				r = guardRoot(t, tc.keys)
			}
			oe := usageErr(t, runServeExpect(t, r, []string{"serve", "api"}, 2*time.Second))
			assert.Equal(t, 2, oe.ExitCode)
			assert.Contains(t, oe.Message, tc.want)
		})
	}
}

// TestServeBearerKeyFiles pins public_key_files: a key another kit
// tool's identity signed with verifies, beside the tool's own.
func TestServeBearerKeyFiles(t *testing.T) {
	other, err := identity.Generate()
	require.NoError(t, err)
	pub, err := other.MarshalPublicKey()
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "other.pub")
	require.NoError(t, os.WriteFile(file, pub, 0o600))

	r := bearerRoot(t, map[string]any{
		"services.api.auth.mode":                 "jwt",
		"services.api.auth.jwt.public_key_files": []string{file},
	}, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	v, err := ResolveServeVerifier(r, APIServiceName)
	require.NoError(t, err)
	require.NotNil(t, v)

	now := time.Now()
	fromOther, err := other.SignJWT(identity.Claims{Subject: "peer", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	require.NoError(t, err)
	tok, err := v.Verify(t.Context(), fromOther)
	require.NoError(t, err)
	assert.Equal(t, other.PublicKeyID(), tok.KeyID)
	_, err = v.Verify(t.Context(), identityToken(t, r, identity.Claims{}))
	assert.NoError(t, err, "the tool's own key is still trusted")

	none, err := ResolveServeVerifier(guardRoot(t, nil), APIServiceName)
	require.NoError(t, err)
	assert.Nil(t, none, "no bearer mode, no verifier")
}
