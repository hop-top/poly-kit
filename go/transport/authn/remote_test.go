package authn_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/authn"
)

// keyServer is an issuer under test control: it serves a JWKS and an
// OpenID discovery document, and counts fetches. The keys are
// generated here, so this is the in-test key server; real providers
// are replayed from cassettes (oidc_cassette_test.go).
type keyServer struct {
	*httptest.Server
	mu        sync.Mutex
	keys      []jose.JSONWebKey
	issuer    string // "" means the server's own URL
	down      bool
	jwksHits  atomic.Int32
	discovery atomic.Int32
}

func newKeyServer(t *testing.T) *keyServer {
	t.Helper()
	ks := &keyServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		ks.jwksHits.Add(1)
		ks.mu.Lock()
		defer ks.mu.Unlock()
		if ks.down {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		pub := make([]jose.JSONWebKey, 0, len(ks.keys))
		for _, k := range ks.keys {
			pub = append(pub, k.Public())
		}
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: pub})
	})
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		ks.discovery.Add(1)
		ks.mu.Lock()
		iss := ks.issuer
		ks.mu.Unlock()
		if iss == "" {
			iss = ks.URL
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": iss, "jwks_uri": ks.URL + "/jwks.json"})
	})
	ks.Server = httptest.NewServer(mux)
	t.Cleanup(ks.Close)
	return ks
}

// add generates a signing key of the given kind under kid and returns
// its private half.
func (ks *keyServer) add(t *testing.T, kid string, alg jose.SignatureAlgorithm) jose.JSONWebKey {
	t.Helper()
	var priv any
	switch alg {
	case jose.RS256:
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		priv = k
	case jose.ES256:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		priv = k
	default:
		t.Fatalf("unsupported alg %s", alg)
	}
	jwk := jose.JSONWebKey{Key: priv, KeyID: kid, Algorithm: string(alg), Use: "sig"}
	ks.mu.Lock()
	ks.keys = append(ks.keys, jwk)
	ks.mu.Unlock()
	return jwk
}

// replace makes keys the served set.
func (ks *keyServer) replace(keys ...jose.JSONWebKey) {
	ks.mu.Lock()
	ks.keys = keys
	ks.mu.Unlock()
}

func (ks *keyServer) setDown(down bool) {
	ks.mu.Lock()
	ks.down = down
	ks.mu.Unlock()
}

// fakeClock is a settable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func signJWK(t *testing.T, k jose.JSONWebKey, claims map[string]any) string {
	return sign(t, jose.SignatureAlgorithm(k.Algorithm), k.Key, k.KeyID, claims)
}

// TestJWKSVerifiesAndCaches pins RSA and EC keys from the set, one
// fetch serving many verifications, and a refetch once Refresh passes.
func TestJWKSVerifiesAndCaches(t *testing.T) {
	ks := newKeyServer(t)
	rsaKey := ks.add(t, "r1", jose.RS256)
	ecKey := ks.add(t, "e1", jose.ES256)
	clk := &fakeClock{t: time.Now()}
	v, err := authn.NewJWKS(ks.URL+"/jwks.json", authn.Remote{Refresh: time.Hour},
		authn.Options{Issuer: "https://issuer.test", Audience: []string{"kit-api"}, Now: clk.now})
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		for _, k := range []jose.JSONWebKey{rsaKey, ecKey} {
			tok, err := v.Verify(t.Context(), signJWK(t, k, valid(clk.now())))
			require.NoError(t, err, k.KeyID)
			assert.Equal(t, k.KeyID, tok.KeyID)
			assert.Equal(t, "acme", tok.Tenant)
		}
	}
	assert.Equal(t, int32(1), ks.jwksHits.Load(), "one fetch serves every verification")

	clk.advance(2 * time.Hour)
	_, err = v.Verify(t.Context(), signJWK(t, rsaKey, valid(clk.now())))
	require.NoError(t, err)
	assert.Equal(t, int32(2), ks.jwksHits.Load(), "a set older than Refresh is fetched again")
}

// TestJWKSFollowsKeyRotation pins that a token naming a kid the cached
// set does not hold refetches the set, at most once per MinRefresh,
// and that a key the issuer retired stops verifying once the set is
// refetched.
func TestJWKSFollowsKeyRotation(t *testing.T) {
	ks := newKeyServer(t)
	oldKey := ks.add(t, "old", jose.ES256)
	clk := &fakeClock{t: time.Now()}
	v, err := authn.NewJWKS(ks.URL+"/jwks.json", authn.Remote{MinRefresh: time.Minute}, authn.Options{Now: clk.now})
	require.NoError(t, err)

	_, err = v.Verify(t.Context(), signJWK(t, oldKey, valid(clk.now())))
	require.NoError(t, err)
	require.Equal(t, int32(1), ks.jwksHits.Load())

	// The issuer rotates: a new key is published and the old retired.
	clk.advance(2 * time.Minute)
	newKey := ks.add(t, "new", jose.ES256)
	ks.replace(newKey)
	_, err = v.Verify(t.Context(), signJWK(t, newKey, valid(clk.now())))
	require.NoError(t, err, "an unknown kid refetches the set")
	assert.Equal(t, int32(2), ks.jwksHits.Load())

	_, err = v.Verify(t.Context(), signJWK(t, oldKey, valid(clk.now())))
	require.ErrorIs(t, err, authn.ErrInvalidToken, "the retired key is gone from the refetched set")
	assert.Contains(t, err.Error(), `kid "old"`)
	assert.Equal(t, int32(2), ks.jwksHits.Load(), "no refetch within MinRefresh")

	// Unknown kids cannot hammer the issuer.
	for i := 0; i < 5; i++ {
		_, err = v.Verify(t.Context(), sign(t, jose.ES256, newKey.Key, "made-up", valid(clk.now())))
		require.ErrorIs(t, err, authn.ErrInvalidToken)
	}
	assert.Equal(t, int32(2), ks.jwksHits.Load())
}

// TestJWKSUnavailable pins that a set that was never fetched refuses
// with ErrKeySetUnavailable, that the issuer is not retried within
// MinRefresh, and that a failed refetch keeps the last good set.
func TestJWKSUnavailable(t *testing.T) {
	ks := newKeyServer(t)
	key := ks.add(t, "k", jose.RS256)
	ks.setDown(true)
	clk := &fakeClock{t: time.Now()}
	v, err := authn.NewJWKS(ks.URL+"/jwks.json", authn.Remote{Refresh: time.Hour, MinRefresh: time.Minute},
		authn.Options{Now: clk.now})
	require.NoError(t, err)

	raw := signJWK(t, key, valid(clk.now()))
	_, err = v.Verify(t.Context(), raw)
	require.ErrorIs(t, err, authn.ErrKeySetUnavailable)
	assert.Contains(t, err.Error(), "status 503")
	_, err = v.Verify(t.Context(), raw)
	require.ErrorIs(t, err, authn.ErrKeySetUnavailable)
	assert.Equal(t, int32(1), ks.jwksHits.Load(), "no retry within MinRefresh")

	ks.setDown(false)
	clk.advance(2 * time.Minute)
	_, err = v.Verify(t.Context(), signJWK(t, key, valid(clk.now())))
	require.NoError(t, err)

	ks.setDown(true)
	clk.advance(2 * time.Hour)
	_, err = v.Verify(t.Context(), signJWK(t, key, valid(clk.now())))
	require.NoError(t, err, "a failed refetch keeps the last good set")
	assert.Equal(t, int32(3), ks.jwksHits.Load())
}

// TestOIDCDiscovery pins that discovery resolves the jwks_uri, that
// the token's iss must be the issuer, and that a discovery document
// naming another issuer is refused.
func TestOIDCDiscovery(t *testing.T) {
	ks := newKeyServer(t)
	key := ks.add(t, "k", jose.RS256)
	v, err := authn.NewOIDC(ks.URL, authn.Remote{}, authn.Options{Audience: []string{"kit-api"}})
	require.NoError(t, err)

	now := time.Now()
	tok, err := v.Verify(t.Context(), signJWK(t, key, with(valid(now), "iss", ks.URL)))
	require.NoError(t, err)
	assert.Equal(t, ks.URL, tok.Issuer)
	_, err = v.Verify(t.Context(), signJWK(t, key, valid(now)))
	require.ErrorIs(t, err, authn.ErrInvalidToken, "iss must be the discovered issuer")
	assert.Equal(t, int32(1), ks.discovery.Load(), "discovery runs once")

	other := newKeyServer(t)
	other.add(t, "k", jose.RS256)
	other.issuer = "https://impostor.test"
	v, err = authn.NewOIDC(other.URL, authn.Remote{}, authn.Options{})
	require.NoError(t, err)
	_, err = v.Verify(t.Context(), signJWK(t, key, with(valid(now), "iss", other.URL)))
	require.ErrorIs(t, err, authn.ErrKeySetUnavailable)
	assert.Contains(t, err.Error(), "impostor")
}

// TestRemoteURLsMustBeHTTPS pins that a key set is fetched over TLS,
// or plain HTTP to a loopback host only.
func TestRemoteURLsMustBeHTTPS(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://idp.example/jwks":      true,
		"http://127.0.0.1:8080/jwks":    true,
		"http://[::1]/jwks":             true,
		"http://localhost/jwks":         true,
		"http://idp.example/jwks":       false,
		"ftp://idp.example/jwks":        false,
		"/relative":                     false,
		"https://":                      false,
		"http://127.0.0.1.evil.example": false,
	} {
		err := authn.CheckURL(raw)
		assert.Equal(t, ok, err == nil, "%s: %v", raw, err)
	}
	_, err := authn.NewJWKS("http://idp.example/jwks", authn.Remote{}, authn.Options{})
	assert.Error(t, err)
	_, err = authn.NewOIDC("http://idp.example", authn.Remote{}, authn.Options{})
	assert.Error(t, err)
}
