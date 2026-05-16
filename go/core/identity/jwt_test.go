package identity

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignVerifyJWT_Roundtrip(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	claims := Claims{
		Subject:  "user-123",
		Issuer:   "kit",
		Scopes:   []string{"read", "write"},
		IssuedAt: time.Now().Unix(),
	}

	token, err := kp.SignJWT(claims)
	require.NoError(t, err)

	got, err := VerifyJWT(token, kp.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, claims.Subject, got.Subject)
	assert.Equal(t, claims.Issuer, got.Issuer)
	assert.Equal(t, claims.Scopes, got.Scopes)
	assert.Equal(t, claims.IssuedAt, got.IssuedAt)
}

func TestVerifyJWT_Expired(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	claims := Claims{
		Subject:   "user-123",
		IssuedAt:  time.Now().Add(-2 * time.Hour).Unix(),
		ExpiresAt: time.Now().Add(-1 * time.Hour).Unix(),
	}

	token, err := kp.SignJWT(claims)
	require.NoError(t, err)

	_, err = VerifyJWT(token, kp.PublicKey)
	assert.ErrorContains(t, err, "expired")
}

func TestVerifyJWT_InvalidSignature(t *testing.T) {
	kp1, err := Generate()
	require.NoError(t, err)
	kp2, err := Generate()
	require.NoError(t, err)

	claims := Claims{Subject: "user-123", IssuedAt: time.Now().Unix()}
	token, err := kp1.SignJWT(claims)
	require.NoError(t, err)

	_, err = VerifyJWT(token, kp2.PublicKey)
	assert.ErrorContains(t, err, "invalid JWT signature")
}

func TestVerifyJWT_Malformed(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	_, err = VerifyJWT("not.a.valid.token", kp.PublicKey)
	assert.Error(t, err)

	_, err = VerifyJWT("onlyonepart", kp.PublicKey)
	assert.ErrorContains(t, err, "malformed")
}

func TestSignJWT_SpecialCharKID(t *testing.T) {
	// Verify that PublicKeyID (used as kid in JWT header) containing
	// special characters doesn't break JSON encoding of the header.
	kp, err := Generate()
	require.NoError(t, err)

	claims := Claims{
		Subject:  "user-special",
		IssuedAt: time.Now().Unix(),
	}

	token, err := kp.SignJWT(claims)
	require.NoError(t, err)

	// The kid is a hex fingerprint, but verify the full roundtrip works
	// and the header is valid JSON by verifying the token.
	got, err := VerifyJWT(token, kp.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, kp.PublicKeyID(), got.KeyID)

	// Additionally, ensure jwtHeaderJSON handles adversarial kid values
	// (quotes, backslashes) without producing invalid JSON.
	adversarial := []string{
		`"injected":"true"`,
		`key\with\\backslash`,
		"line\nbreak",
		`normal"quote`,
	}
	for _, kid := range adversarial {
		raw := jwtHeaderJSON(kid)
		var parsed map[string]string
		err := json.Unmarshal(raw, &parsed)
		require.NoError(t, err, "kid=%q produced invalid JSON", kid)
		assert.Equal(t, kid, parsed["kid"])
	}
}

func TestVerifyJWTWithOptions_IssuerMismatch(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	token, err := kp.SignJWT(Claims{
		Subject:  "user-1",
		Issuer:   "auth-svc",
		IssuedAt: time.Now().Unix(),
	})
	require.NoError(t, err)

	_, err = VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{Issuer: "other-svc"})
	assert.ErrorContains(t, err, "issuer")
}

func TestVerifyJWTWithOptions_AudienceMismatch(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	token, err := kp.SignJWT(Claims{
		Subject:  "user-1",
		Audience: Audience{"api-gateway"},
		IssuedAt: time.Now().Unix(),
	})
	require.NoError(t, err)

	_, err = VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{Audience: "web-app"})
	assert.ErrorContains(t, err, "audience")
}

func TestVerifyJWTWithOptions_NilOpts(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	token, err := kp.SignJWT(Claims{
		Subject:  "user-1",
		Issuer:   "auth-svc",
		Audience: Audience{"api"},
		IssuedAt: time.Now().Unix(),
	})
	require.NoError(t, err)

	got, err := VerifyJWTWithOptions(token, kp.PublicKey, nil)
	require.NoError(t, err)
	assert.Equal(t, "user-1", got.Subject)
}

func TestVerifyJWTWithOptions_EmptyOpts(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	token, err := kp.SignJWT(Claims{
		Subject:  "user-1",
		Issuer:   "auth-svc",
		Audience: Audience{"api"},
		IssuedAt: time.Now().Unix(),
	})
	require.NoError(t, err)

	got, err := VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{})
	require.NoError(t, err)
	assert.Equal(t, "user-1", got.Subject)
	assert.Equal(t, "auth-svc", got.Issuer)
	assert.Equal(t, Audience{"api"}, got.Audience)
}

func TestVerifyJWTWithOptions_BothValid(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	token, err := kp.SignJWT(Claims{
		Subject:  "user-1",
		Issuer:   "auth-svc",
		Audience: Audience{"api"},
		IssuedAt: time.Now().Unix(),
	})
	require.NoError(t, err)

	got, err := VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{
		Issuer:   "auth-svc",
		Audience: "api",
	})
	require.NoError(t, err)
	assert.Equal(t, "user-1", got.Subject)
}

func TestVerifyJWTWithOptions_MixedValidInvalid(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	token, err := kp.SignJWT(Claims{
		Subject:  "user-1",
		Issuer:   "auth-svc",
		Audience: Audience{"api"},
		IssuedAt: time.Now().Unix(),
	})
	require.NoError(t, err)

	// valid issuer, invalid audience
	_, err = VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{
		Issuer:   "auth-svc",
		Audience: "wrong",
	})
	assert.ErrorContains(t, err, "audience")

	// invalid issuer, valid audience
	_, err = VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{
		Issuer:   "wrong",
		Audience: "api",
	})
	assert.ErrorContains(t, err, "issuer")
}

func TestVerifyJWTWithOptions_EmptyClaimVsExpected(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	// token has no issuer set
	token, err := kp.SignJWT(Claims{
		Subject:  "user-1",
		IssuedAt: time.Now().Unix(),
	})
	require.NoError(t, err)

	_, err = VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{Issuer: "auth-svc"})
	assert.ErrorContains(t, err, "issuer")
}

func TestAudience_ArrayRoundtrip(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	claims := Claims{
		Subject:  "user-1",
		Audience: Audience{"api-gateway", "web-app"},
		IssuedAt: time.Now().Unix(),
	}

	token, err := kp.SignJWT(claims)
	require.NoError(t, err)

	got, err := VerifyJWT(token, kp.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, Audience{"api-gateway", "web-app"}, got.Audience)

	// verify opts matches one of the array elements
	_, err = VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{Audience: "web-app"})
	require.NoError(t, err)

	// mismatch against array
	_, err = VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{Audience: "unknown"})
	assert.ErrorContains(t, err, "audience")
}

func TestAudience_SingleStringJSON(t *testing.T) {
	// single-element audience marshals as string, not array
	a := Audience{"only"}
	data, err := json.Marshal(a)
	require.NoError(t, err)
	assert.Equal(t, `"only"`, string(data))

	var got Audience
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, Audience{"only"}, got)
}

func TestAudience_RawArrayToken(t *testing.T) {
	// simulate IdP token with "aud":["a","b"] at the JSON level
	kp, err := Generate()
	require.NoError(t, err)

	// build a token manually with array aud in payload
	kid := kp.PublicKeyID()
	headerB64 := base64Encode(jwtHeaderJSON(kid))

	payload := []byte(`{"sub":"u1","aud":["a","b"],"iat":` +
		fmt.Sprintf("%d", time.Now().Unix()) + `}`)
	payloadB64 := base64Encode(payload)

	sigInput := headerB64 + "." + payloadB64
	sig := ed25519.Sign(kp.PrivateKey, []byte(sigInput))
	token := sigInput + "." + base64Encode(sig)

	got, err := VerifyJWT(token, kp.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, Audience{"a", "b"}, got.Audience)
}

func TestSignJWT_ClaimsPreserved(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	claims := Claims{
		Subject:   "svc-456",
		Issuer:    "kit-auth",
		Scopes:    []string{"admin"},
		IssuedAt:  1700000000,
		ExpiresAt: 9999999999,
	}

	token, err := kp.SignJWT(claims)
	require.NoError(t, err)

	got, err := VerifyJWT(token, kp.PublicKey)
	require.NoError(t, err)
	// SignJWT sets KeyID from PublicKeyID; update expected claims.
	claims.KeyID = kp.PublicKeyID()
	assert.Equal(t, claims, *got)
}
