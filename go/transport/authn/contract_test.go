package authn_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv/memory"
	"hop.top/kit/go/transport/authn"
)

var updateVectors = flag.Bool("update-authn-vectors", false,
	"regenerate contracts/authn-v1/vectors.json (new keys and signatures)")

const vectorsPath = "../../../contracts/authn-v1/vectors.json"

// vectorNow is the clock every vector verifies at.
const vectorNow int64 = 1790000000

type vectorFile struct {
	Version string         `json:"version"`
	Now     int64          `json:"now"`
	Tokens  []tokenVector  `json:"tokens"`
	PEM     []pemVector    `json:"pem"`
	APIKeys []apiKeyVector `json:"api_keys"`
}

type tokenVector struct {
	Name    string          `json:"name"`
	Alg     string          `json:"alg"`
	JWK     json.RawMessage `json:"jwk"`
	Token   string          `json:"token"`
	Options vectorOptions   `json:"options,omitempty"`
	Want    *vectorClaims   `json:"want,omitempty"`
	// Refusal is a substring of the error a refused token yields.
	Refusal string `json:"refusal,omitempty"`
	KeyID   string `json:"key_id,omitempty"`
}

type vectorOptions struct {
	Issuer   string   `json:"issuer,omitempty"`
	Audience []string `json:"audience,omitempty"`
}

type vectorClaims struct {
	Subject string   `json:"sub"`
	Tenant  string   `json:"tenant,omitempty"`
	Scopes  []string `json:"scopes,omitempty"`
}

type pemVector struct {
	Name string `json:"name"`
	PEM  string `json:"pem"`
	KID  string `json:"kid"`
}

type apiKeyVector struct {
	Name   string `json:"name"`
	Key    string `json:"key"`
	Domain string `json:"domain"`
	Hash   string `json:"hash"`
}

// TestAuthnContractVectors verifies the cross-language corpus Rust (and
// any later port) must also verify: one signed token per accepted
// algorithm, the refusals that matter, PEM key ids and API key hashes.
// With -update-authn-vectors it rewrites the corpus first.
func TestAuthnContractVectors(t *testing.T) {
	if *updateVectors {
		writeVectors(t)
	}
	data, err := os.ReadFile(filepath.FromSlash(vectorsPath))
	require.NoError(t, err)
	var f vectorFile
	require.NoError(t, json.Unmarshal(data, &f))
	require.Equal(t, "v1", f.Version)
	now := time.Unix(f.Now, 0)

	for _, v := range f.Tokens {
		t.Run(v.Name, func(t *testing.T) {
			var jwk jose.JSONWebKey
			require.NoError(t, json.Unmarshal(v.JWK, &jwk))
			key := authn.Key{ID: jwk.KeyID, Public: jwk.Key}
			ver, err := authn.NewJWT([]authn.Key{key}, authn.Options{
				Issuer: v.Options.Issuer, Audience: v.Options.Audience, Now: clock(now),
			})
			require.NoError(t, err)
			tok, err := ver.Verify(t.Context(), v.Token)
			if v.Refusal != "" {
				require.ErrorIs(t, err, authn.ErrInvalidToken)
				assert.Contains(t, err.Error(), v.Refusal)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, v.Want.Subject, tok.Subject)
			assert.Equal(t, v.Want.Tenant, tok.Tenant)
			assert.Equal(t, v.Want.Scopes, tok.Scopes)
			assert.Equal(t, v.KeyID, tok.KeyID)
		})
	}

	for _, v := range f.PEM {
		t.Run("pem/"+v.Name, func(t *testing.T) {
			k, err := authn.ParsePublicKeyPEM([]byte(v.PEM))
			require.NoError(t, err)
			assert.Equal(t, v.KID, k.ID)
		})
	}

	for _, v := range f.APIKeys {
		t.Run("apikey/"+v.Name, func(t *testing.T) {
			// The corpus key verifies only against a store holding its
			// hash: seed one record by hand, as another port's would be.
			parts := strings.Split(v.Key, "_")
			require.Len(t, parts, 3)
			store := memory.New()
			rec := `{"id":"` + parts[1] + `","principal":"p","created_at":"2026-09-28T12:00:00Z","hash":"` + v.Hash + `"}`
			require.NoError(t, store.Put(t.Context(), "apikey/"+parts[1], []byte(rec)))
			if v.Domain == "" || v.Domain == "kit-apikey-v1" {
				_, err := authn.NewAPIKeys(store, nil).Verify(t.Context(), v.Key)
				require.NoError(t, err)
				return
			}
			t.Skip("Go has no configurable hash domain yet")
		})
	}
}

func writeVectors(t *testing.T) {
	t.Helper()
	now := time.Unix(vectorNow, 0)
	good := func(extra ...any) map[string]any {
		return with(map[string]any{
			"sub": "alice", "iss": "https://issuer.test", "aud": "kit-api", "tenant": "acme",
			"scopes": []string{"items:read", "items:write"},
			"iat":    vectorNow - 10, "exp": vectorNow + 3600,
		}, extra...)
	}
	want := &vectorClaims{Subject: "alice", Tenant: "acme", Scopes: []string{"items:read", "items:write"}}
	opts := vectorOptions{Issuer: "https://issuer.test", Audience: []string{"kit-api"}}

	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	p256k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p384k, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	p521k, _ := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	rsak, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubJWK := func(pub crypto.PublicKey, kid string, alg jose.SignatureAlgorithm) json.RawMessage {
		b, err := jose.JSONWebKey{Key: pub, KeyID: kid, Algorithm: string(alg), Use: "sig"}.MarshalJSON()
		require.NoError(t, err)
		return b
	}
	var f vectorFile
	f.Version, f.Now = "v1", vectorNow
	add := func(name string, alg jose.SignatureAlgorithm, priv, pub crypto.PublicKey, claims map[string]any) {
		kid := "k-" + strings.ToLower(string(alg))
		f.Tokens = append(f.Tokens, tokenVector{
			Name: name, Alg: string(alg), JWK: pubJWK(pub, kid, alg),
			Token: sign(t, alg, priv, kid, claims), Options: opts, Want: want, KeyID: kid,
		})
	}
	add("eddsa", jose.EdDSA, edPriv, edPub, good())
	add("es256", jose.ES256, p256k, &p256k.PublicKey, good())
	add("es384", jose.ES384, p384k, &p384k.PublicKey, good())
	add("es512", jose.ES512, p521k, &p521k.PublicKey, good())
	for _, a := range []jose.SignatureAlgorithm{jose.RS256, jose.RS384, jose.RS512, jose.PS256, jose.PS384, jose.PS512} {
		add(strings.ToLower(string(a)), a, rsak, &rsak.PublicKey, good())
	}
	// OAuth-style scope claim, aud list, numeric tenant.
	add("scope-string", jose.EdDSA, edPriv, edPub,
		with(good(), "scopes", nil, "scope", "a b", "aud", []string{"x", "kit-api"}))
	f.Tokens[len(f.Tokens)-1].Want = &vectorClaims{Subject: "alice", Tenant: "acme", Scopes: []string{"a", "b"}}

	refuse := func(name, why string, claims map[string]any, o vectorOptions) {
		f.Tokens = append(f.Tokens, tokenVector{
			Name: name, Alg: "EdDSA", JWK: pubJWK(edPub, "k-eddsa", jose.EdDSA),
			Token: sign(t, jose.EdDSA, edPriv, "k-eddsa", claims), Options: o, Refusal: why,
		})
	}
	refuse("expired", "expired", good("exp", vectorNow-3600), opts)
	refuse("not-yet-valid", "not valid yet", good("nbf", vectorNow+3600), opts)
	refuse("iat-future", "future", good("iat", vectorNow+3600), opts)
	refuse("no-exp", "no exp", good("exp", nil), opts)
	refuse("no-sub", "no sub", good("sub", nil), opts)
	refuse("wrong-issuer", "issuer", good(), vectorOptions{Issuer: "https://elsewhere.test"})
	refuse("wrong-audience", "does not include", good(), vectorOptions{Audience: []string{"billing"}})
	refuse("audience-path-strict", "does not include", good(), vectorOptions{Audience: []string{"kit-api/mcp"}})

	good1 := sign(t, jose.EdDSA, edPriv, "k-eddsa", good())
	parts := strings.Split(good1, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sig[0] ^= 1
	f.Tokens = append(f.Tokens,
		tokenVector{Name: "tampered-signature", Alg: "EdDSA", JWK: pubJWK(edPub, "k-eddsa", jose.EdDSA),
			Token: parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sig), Refusal: "signature"},
		tokenVector{Name: "alg-none", Alg: "none", JWK: pubJWK(edPub, "k-eddsa", jose.EdDSA),
			Token: "eyJhbGciOiJub25lIn0." + parts[1] + ".", Refusal: "algorithm"},
		tokenVector{Name: "hs256", Alg: "HS256", JWK: pubJWK(edPub, "k-eddsa", jose.EdDSA),
			Token: sign(t, jose.HS256, []byte(strings.Repeat("s", 32)), "k-eddsa", good()), Refusal: "algorithm"},
	)

	pemOf := func(pub crypto.PublicKey) string {
		der, err := x509.MarshalPKIXPublicKey(pub)
		require.NoError(t, err)
		return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	}
	for _, e := range []struct {
		name string
		pub  crypto.PublicKey
	}{
		{"ed25519", edPub}, {"p256", &p256k.PublicKey}, {"p384", &p384k.PublicKey},
		{"p521", &p521k.PublicKey}, {"rsa2048", &rsak.PublicKey},
	} {
		name := e.name
		p := pemOf(e.pub)
		k, err := authn.ParsePublicKeyPEM([]byte(p))
		require.NoError(t, err)
		f.PEM = append(f.PEM, pemVector{Name: name, PEM: p, KID: k.ID})
	}

	for _, name := range []string{"first", "second"} {
		store := memory.New()
		keys := authn.NewAPIKeys(store, func() time.Time { return now })
		raw, rec, err := keys.Create(t.Context(), authn.NewAPIKey{Principal: "p"})
		require.NoError(t, err)
		f.APIKeys = append(f.APIKeys, apiKeyVector{Name: name, Key: raw, Domain: "kit-apikey-v1", Hash: rec.Hash})
	}

	out, err := json.MarshalIndent(f, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.FromSlash(vectorsPath), append(out, '\n'), 0o644))
}
