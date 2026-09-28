package authn_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/authn"
)

// sign signs claims with key under alg, naming kid in the header.
func sign(t *testing.T, alg jose.SignatureAlgorithm, key any, kid string, claims map[string]any) string {
	t.Helper()
	opts := (&jose.SignerOptions{}).WithType("JWT")
	if kid != "" {
		opts = opts.WithHeader("kid", kid)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: key}, opts)
	require.NoError(t, err)
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	require.NoError(t, err)
	return raw
}

// valid is a claim set every verifier below accepts, at now.
func valid(now time.Time) map[string]any {
	return map[string]any{
		"sub":    "alice",
		"iss":    "https://issuer.test",
		"aud":    "kit-api",
		"tenant": "acme",
		"scopes": []string{"items:read", "items:write"},
		"iat":    now.Unix(),
		"exp":    now.Add(time.Hour).Unix(),
	}
}

func with(m map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		if kv[i+1] == nil {
			delete(out, kv[i].(string))
			continue
		}
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func clock(t time.Time) func() time.Time { return func() time.Time { return t } }

func mustKeypair(t *testing.T) *identity.Keypair {
	t.Helper()
	kp, err := identity.Generate()
	require.NoError(t, err)
	return kp
}

// TestJWTVerifiesTheToolsOwnIdentityTokens pins the round trip with
// core/identity: SignJWT's token verifies against IdentityKey, with the
// kid SignJWT embeds, and yields principal, tenant and scopes.
func TestJWTVerifiesTheToolsOwnIdentityTokens(t *testing.T) {
	kp := mustKeypair(t)
	now := time.Now()
	raw, err := kp.SignJWT(identity.Claims{
		Subject: "alice", Tenant: "acme", Scopes: []string{"items:read"},
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	v, err := authn.NewJWT([]authn.Key{authn.IdentityKey(kp)}, authn.Options{})
	require.NoError(t, err)
	tok, err := v.Verify(t.Context(), raw)
	require.NoError(t, err)
	assert.Equal(t, api.Claims{Subject: "alice", Tenant: "acme", Scopes: []string{"items:read"}}, tok.Claims())
	assert.Equal(t, kp.PublicKeyID(), tok.KeyID)
}

// TestTimeChecksTolerateClockSkew pins exp, nbf and iat with the skew:
// a token a few seconds past exp is accepted within the default
// minute and refused beyond it, and refused at once with no tolerance.
func TestTimeChecksTolerateClockSkew(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	pub := priv.Public()
	now := time.Now()
	keys := []authn.Key{{ID: "k1", Public: pub}}

	cases := []struct {
		name   string
		claims map[string]any
		skew   time.Duration
		ok     bool
		want   string
	}{
		{"fresh", valid(now), 0, true, ""},
		{"expired within skew", with(valid(now), "exp", now.Add(-10*time.Second).Unix()), 0, true, ""},
		{"expired beyond skew", with(valid(now), "exp", now.Add(-2*time.Minute).Unix()), 0, false, "expired"},
		{"expired, no tolerance", with(valid(now), "exp", now.Add(-10*time.Second).Unix()), -1, false, "expired"},
		{"nbf within skew", with(valid(now), "nbf", now.Add(10*time.Second).Unix()), 0, true, ""},
		{"nbf beyond skew", with(valid(now), "nbf", now.Add(5*time.Minute).Unix()), 0, false, "not valid yet"},
		{"iat in the future", with(valid(now), "iat", now.Add(5*time.Minute).Unix()), 0, false, "future"},
		{"custom skew", with(valid(now), "exp", now.Add(-2*time.Minute).Unix()), 5 * time.Minute, true, ""},
		{"no exp", with(valid(now), "exp", nil), 0, false, "no exp"},
		{"no sub", with(valid(now), "sub", nil), 0, false, "no sub"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := authn.NewJWT(keys, authn.Options{ClockSkew: c.skew, Now: clock(now)})
			require.NoError(t, err)
			_, err = v.Verify(t.Context(), sign(t, jose.EdDSA, priv, "k1", c.claims))
			if c.ok {
				assert.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, authn.ErrInvalidToken)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

// TestIssuerAndAudienceAreChecked pins that a configured issuer and
// audience must match, and that unset ones check nothing.
func TestIssuerAndAudienceAreChecked(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	keys := []authn.Key{{ID: "k1", Public: priv.Public()}}
	now := time.Now()
	raw := sign(t, jose.EdDSA, priv, "k1", with(valid(now), "aud", []string{"other", "kit-api"}))

	for _, c := range []struct {
		name string
		opts authn.Options
		want string
	}{
		{"both match", authn.Options{Issuer: "https://issuer.test", Audience: []string{"kit-api"}}, ""},
		{"unset", authn.Options{}, ""},
		{"any audience", authn.Options{Audience: []string{"nope", "other"}}, ""},
		{"wrong issuer", authn.Options{Issuer: "https://elsewhere.test"}, `issuer "https://issuer.test" is not "https://elsewhere.test"`},
		{"wrong audience", authn.Options{Audience: []string{"billing"}}, "does not include billing"},
		{"audience trailing slash", authn.Options{Audience: []string{"kit-api/"}}, ""},
		{"audience path is strict", authn.Options{Audience: []string{"kit-api/mcp"}}, "does not include"},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, err := authn.NewJWT(keys, c.opts)
			require.NoError(t, err)
			_, err = v.Verify(t.Context(), raw)
			if c.want == "" {
				assert.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, authn.ErrInvalidToken)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

// TestKidRotation pins that each key in the set verifies the tokens
// naming its kid, so a retired key kept in the set still verifies
// what it signed; that a token naming one kid and signed by another
// key is refused; and that a token signed by no trusted key is.
func TestKidRotation(t *testing.T) {
	oldKP, newKP, stranger := mustKeypair(t), mustKeypair(t), mustKeypair(t)
	v, err := authn.NewJWT([]authn.Key{authn.IdentityKey(newKP), authn.IdentityKey(oldKP)}, authn.Options{})
	require.NoError(t, err)
	now := time.Now()
	claims := identity.Claims{Subject: "alice", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()}

	for _, kp := range []*identity.Keypair{oldKP, newKP} {
		raw, err := kp.SignJWT(claims)
		require.NoError(t, err)
		tok, err := v.Verify(t.Context(), raw)
		require.NoError(t, err)
		assert.Equal(t, kp.PublicKeyID(), tok.KeyID)
	}

	// The old key's signature under the new key's kid.
	mismatched := sign(t, jose.EdDSA, oldKP.PrivateKey, newKP.PublicKeyID(),
		map[string]any{"sub": "alice", "exp": now.Add(time.Hour).Unix()})
	_, err = v.Verify(t.Context(), mismatched)
	require.ErrorIs(t, err, authn.ErrInvalidToken)
	assert.Contains(t, err.Error(), "signature")

	raw, err := stranger.SignJWT(claims)
	require.NoError(t, err)
	_, err = v.Verify(t.Context(), raw)
	require.ErrorIs(t, err, authn.ErrInvalidToken)
}

// TestUnsafeAlgorithmsAreRefused pins that "none" and HMAC tokens are
// refused before any key is consulted.
func TestUnsafeAlgorithmsAreRefused(t *testing.T) {
	kp := mustKeypair(t)
	v, err := authn.NewJWT([]authn.Key{authn.IdentityKey(kp)}, authn.Options{})
	require.NoError(t, err)
	now := time.Now()

	hs := sign(t, jose.HS256, []byte(strings.Repeat("s", 32)), kp.PublicKeyID(), valid(now))
	_, err = v.Verify(t.Context(), hs)
	require.ErrorIs(t, err, authn.ErrInvalidToken)

	// alg none: header {"alg":"none"}, the payload, an empty signature.
	parts := strings.Split(hs, ".")
	none := "eyJhbGciOiJub25lIn0." + parts[1] + "."
	_, err = v.Verify(t.Context(), none)
	require.ErrorIs(t, err, authn.ErrInvalidToken)

	_, err = v.Verify(t.Context(), "not-a-token")
	require.ErrorIs(t, err, authn.ErrInvalidToken)
	_, err = v.Verify(t.Context(), "")
	require.ErrorIs(t, err, authn.ErrNoToken)
}

// TestCheckIsTheRevocationHook pins that Options.Check sees the
// verified token and that its error refuses it.
func TestCheckIsTheRevocationHook(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	revoked := map[string]bool{"t-2": true}
	v, err := authn.NewJWT([]authn.Key{{ID: "k1", Public: priv.Public()}}, authn.Options{
		Check: func(_ context.Context, tok *authn.Token) error {
			if revoked[tok.ID] {
				return errors.New("revoked")
			}
			return nil
		},
	})
	require.NoError(t, err)
	now := time.Now()
	_, err = v.Verify(t.Context(), sign(t, jose.EdDSA, priv, "k1", with(valid(now), "jti", "t-1")))
	assert.NoError(t, err)
	_, err = v.Verify(t.Context(), sign(t, jose.EdDSA, priv, "k1", with(valid(now), "jti", "t-2")))
	require.ErrorIs(t, err, authn.ErrInvalidToken)
	assert.Contains(t, err.Error(), "revoked")
}

// TestScopesAndTenantClaims pins where scopes and the tenant are read
// from.
func TestScopesAndTenantClaims(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	keys := []authn.Key{{ID: "k1", Public: priv.Public()}}
	now := time.Now()
	base := with(valid(now), "scopes", nil, "tenant", nil)

	for _, c := range []struct {
		name   string
		claims map[string]any
		opts   authn.Options
		want   api.Claims
	}{
		{"scope string", with(base, "scope", "a b"), authn.Options{}, api.Claims{Subject: "alice", Scopes: []string{"a", "b"}}},
		{"scp string", with(base, "scp", "a b"), authn.Options{}, api.Claims{Subject: "alice", Scopes: []string{"a", "b"}}},
		{"scp list", with(base, "scp", []string{"a"}), authn.Options{}, api.Claims{Subject: "alice", Scopes: []string{"a"}}},
		{"scopes wins", with(base, "scopes", []string{"x"}, "scope", "y"), authn.Options{}, api.Claims{Subject: "alice", Scopes: []string{"x"}}},
		{"tenant claim", with(base, "tid", "t-9"), authn.Options{TenantClaim: "tid"}, api.Claims{Subject: "alice", Tenant: "t-9"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, err := authn.NewJWT(keys, c.opts)
			require.NoError(t, err)
			tok, err := v.Verify(t.Context(), sign(t, jose.EdDSA, priv, "k1", c.claims))
			require.NoError(t, err)
			assert.Equal(t, c.want, tok.Claims())
		})
	}
}

// TestOAuthScopeReachesScopesOf pins that the standard space-delimited
// scope claim an OAuth issuer mints comes out of the AuthFunc as scopes
// api.ScopesOf — what the permission gate reads — returns one by one.
func TestOAuthScopeReachesScopesOf(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	v, err := authn.NewJWT([]authn.Key{{ID: "k1", Public: priv.Public()}}, authn.Options{})
	require.NoError(t, err)
	raw := sign(t, jose.EdDSA, priv, "k1", with(valid(time.Now()), "scopes", nil, "scope", "items:export other"))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	claims, err := v.AuthFunc()(req)
	require.NoError(t, err)
	assert.Equal(t, []string{"items:export", "other"}, api.ScopesOf(claims))
}

// TestParsePublicKeyPEM pins the kid a key file yields: the identity
// fingerprint for Ed25519, so kit tokens match; a thumbprint otherwise.
func TestParsePublicKeyPEM(t *testing.T) {
	kp := mustKeypair(t)
	pemBytes, err := kp.MarshalPublicKey()
	require.NoError(t, err)
	k, err := authn.ParsePublicKeyPEM(pemBytes)
	require.NoError(t, err)
	assert.Equal(t, kp.PublicKeyID(), k.ID)

	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&rk.PublicKey)
	require.NoError(t, err)
	k, err = authn.ParsePublicKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	require.NoError(t, err)
	want, err := (&jose.JSONWebKey{Key: &rk.PublicKey}).Thumbprint(crypto.SHA256)
	require.NoError(t, err)
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(want), k.ID)

	// An external signer's own kid still verifies: the signature decides.
	v, err := authn.NewJWT([]authn.Key{k}, authn.Options{})
	require.NoError(t, err)
	_, err = v.Verify(t.Context(), sign(t, jose.RS256, rk, "their-kid", valid(time.Now())))
	assert.NoError(t, err)

	_, err = authn.ParsePublicKeyPEM([]byte("nope"))
	assert.Error(t, err)
}

// TestAuthFuncThroughTheAuthMiddleware pins the api.AuthFunc shape: no
// bearer token and a bad one are 401 unauthenticated; a good one
// reaches the handler as verified api.Claims.
func TestAuthFuncThroughTheAuthMiddleware(t *testing.T) {
	kp := mustKeypair(t)
	v, err := authn.NewJWT([]authn.Key{authn.IdentityKey(kp)}, authn.Options{})
	require.NoError(t, err)
	h := api.Auth(v.AuthFunc())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, api.Authenticated(r.Context()))
		p, tenant := api.IdentityOf(api.ClaimsFromContext(r.Context()))
		_, _ = w.Write([]byte(p + "/" + tenant + "/" + strings.Join(api.ScopesOf(api.ClaimsFromContext(r.Context())), ",")))
	}))
	now := time.Now()
	raw, err := kp.SignJWT(identity.Claims{Subject: "alice", Tenant: "acme", Scopes: []string{"s"},
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	require.NoError(t, err)

	for _, c := range []struct {
		header string
		status int
		body   string
	}{
		{"", http.StatusUnauthorized, "no bearer token"},
		{"Basic abc", http.StatusUnauthorized, "not a bearer token"},
		{"Bearer garbage", http.StatusUnauthorized, "invalid token"},
		{"bearer " + raw, http.StatusOK, "alice/acme/s"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		h.ServeHTTP(rec, req)
		assert.Equal(t, c.status, rec.Code, c.header)
		assert.Contains(t, rec.Body.String(), c.body, c.header)
	}
}

func TestNewJWTRefusesNoKeysAndUnsupportedKeys(t *testing.T) {
	_, err := authn.NewJWT(nil, authn.Options{})
	assert.Error(t, err)
	_, err = authn.NewJWT([]authn.Key{{ID: "x", Public: "not a key"}}, authn.Options{})
	assert.Error(t, err)
	ek, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	_, err = authn.NewJWT([]authn.Key{{ID: "x", Public: &ek.PublicKey}}, authn.Options{})
	assert.NoError(t, err)
}
