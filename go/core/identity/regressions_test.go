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

// T-0729: JWT issuer/audience validation regressions.

func TestRegression_NilVerifyOptions(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	claims := Claims{
		Subject:  "user-1",
		Issuer:   "auth-svc",
		Audience: Audience{"api"},
		IssuedAt: time.Now().Unix(),
	}

	token, err := kp.SignJWT(claims)
	require.NoError(t, err)

	// nil opts must not panic
	got, err := VerifyJWTWithOptions(token, kp.PublicKey, nil)
	require.NoError(t, err)

	// must behave identically to VerifyJWT
	want, err := VerifyJWT(token, kp.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestRegression_ArrayAudienceFromExternalIdP(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	// simulate external IdP token with raw JSON array aud
	kid := kp.PublicKeyID()
	headerB64 := base64Encode(jwtHeaderJSON(kid))

	payload := []byte(fmt.Sprintf(
		`{"sub":"ext-user","aud":["service-a","service-b"],"iat":%d}`,
		time.Now().Unix(),
	))
	payloadB64 := base64Encode(payload)

	sigInput := headerB64 + "." + payloadB64
	sig := ed25519.Sign(kp.PrivateKey, []byte(sigInput))
	token := sigInput + "." + base64Encode(sig)

	// match first element
	got, err := VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{Audience: "service-a"})
	require.NoError(t, err)
	assert.Equal(t, "ext-user", got.Subject)

	// match second element
	got, err = VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{Audience: "service-b"})
	require.NoError(t, err)
	assert.Equal(t, "ext-user", got.Subject)

	// no match
	_, err = VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{Audience: "service-c"})
	assert.ErrorContains(t, err, "audience")
}

func TestRegression_SingleStringAudienceBackcompat(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	// raw JSON with "aud":"single" (string, not array)
	kid := kp.PublicKeyID()
	headerB64 := base64Encode(jwtHeaderJSON(kid))

	payload := []byte(fmt.Sprintf(
		`{"sub":"u1","aud":"single","iat":%d}`,
		time.Now().Unix(),
	))
	payloadB64 := base64Encode(payload)

	sigInput := headerB64 + "." + payloadB64
	sig := ed25519.Sign(kp.PrivateKey, []byte(sigInput))
	token := sigInput + "." + base64Encode(sig)

	got, err := VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{Audience: "single"})
	require.NoError(t, err)
	assert.Equal(t, Audience{"single"}, got.Audience)

	// mismatch
	_, err = VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{Audience: "other"})
	assert.ErrorContains(t, err, "audience")
}

func TestRegression_AudienceMarshalRoundtrip(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	claims := Claims{
		Subject:  "svc-1",
		Audience: Audience{"a", "b"},
		IssuedAt: time.Now().Unix(),
	}

	token, err := kp.SignJWT(claims)
	require.NoError(t, err)

	// extract raw payload and confirm JSON contains array form
	parts := splitToken(t, token)
	raw, err := base64Decode(parts[1])
	require.NoError(t, err)

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))

	audRaw := string(m["aud"])
	assert.Equal(t, `["a","b"]`, audRaw, "multi-element aud must serialize as array")

	// verify with opts matching second element
	got, err := VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{Audience: "b"})
	require.NoError(t, err)
	assert.Equal(t, Audience{"a", "b"}, got.Audience)
}

func TestRegression_IssuerMismatchWithValidAudience(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	token, err := kp.SignJWT(Claims{
		Subject:  "user-1",
		Issuer:   "correct-issuer",
		Audience: Audience{"api"},
		IssuedAt: time.Now().Unix(),
	})
	require.NoError(t, err)

	_, err = VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{
		Issuer:   "wrong-issuer",
		Audience: "api",
	})
	assert.ErrorContains(t, err, "issuer")
}

func TestRegression_EmptyAudienceVsExpected(t *testing.T) {
	kp, err := Generate()
	require.NoError(t, err)

	// token has no audience claim
	token, err := kp.SignJWT(Claims{
		Subject:  "user-1",
		IssuedAt: time.Now().Unix(),
	})
	require.NoError(t, err)

	_, err = VerifyJWTWithOptions(token, kp.PublicKey, &VerifyOptions{Audience: "required-aud"})
	assert.ErrorContains(t, err, "audience")
}

// splitToken splits a JWT into its 3 parts.
func splitToken(t *testing.T, token string) []string {
	t.Helper()
	parts := make([]string, 0, 3)
	for i, j := 0, 0; j <= len(token); j++ {
		if j == len(token) || token[j] == '.' {
			parts = append(parts, token[i:j])
			i = j + 1
		}
	}
	require.Len(t, parts, 3, "malformed token")
	return parts
}
