package identity

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Audience represents a JWT "aud" claim: either a single string or array
// of strings per RFC 7519 §4.1.3.
type Audience []string

func (a Audience) MarshalJSON() ([]byte, error) {
	if len(a) == 1 {
		return json.Marshal(a[0])
	}
	return json.Marshal([]string(a))
}

func (a *Audience) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = Audience{single}
		return nil
	}
	var multi []string
	if err := json.Unmarshal(data, &multi); err != nil {
		return fmt.Errorf("identity: aud must be string or []string: %w", err)
	}
	*a = multi
	return nil
}

// Claims represents JWT claims.
type Claims struct {
	Subject   string   `json:"sub"`
	Issuer    string   `json:"iss,omitempty"`
	KeyID     string   `json:"kid,omitempty"`
	Audience  Audience `json:"aud,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
	IssuedAt  int64    `json:"iat"`
	ExpiresAt int64    `json:"exp,omitempty"`
	Nonce     string   `json:"nonce,omitempty"`
}

// jwtHeader builds the JWT header JSON including the kid field.
func jwtHeaderJSON(kid string) []byte {
	header := map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": kid}
	data, _ := json.Marshal(header)
	return data
}

// SignJWT signs claims into a JWT string using the keypair.
func (k *Keypair) SignJWT(claims Claims) (string, error) {
	kid := k.PublicKeyID()
	claims.KeyID = kid
	headerB64 := base64Encode(jwtHeaderJSON(kid))

	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("identity: marshal claims: %w", err)
	}
	payloadB64 := base64Encode(payload)

	sigInput := headerB64 + "." + payloadB64
	sig := ed25519.Sign(k.PrivateKey, []byte(sigInput))

	return sigInput + "." + base64Encode(sig), nil
}

// VerifyJWT verifies a JWT string and returns the claims.
// Checks signature validity and expiration (if set).
func VerifyJWT(token string, pubKey ed25519.PublicKey) (*Claims, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return nil, errors.New("identity: malformed JWT: expected 3 parts")
	}

	sig, err := base64Decode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("identity: decode signature: %w", err)
	}

	sigInput := parts[0] + "." + parts[1]
	if !ed25519.Verify(pubKey, []byte(sigInput), sig) {
		return nil, errors.New("identity: invalid JWT signature")
	}

	payload, err := base64Decode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("identity: decode payload: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("identity: unmarshal claims: %w", err)
	}

	if claims.ExpiresAt > 0 && time.Now().Unix() > claims.ExpiresAt {
		return nil, errors.New("identity: token expired")
	}

	return &claims, nil
}

// VerifyOptions configures optional issuer/audience validation.
type VerifyOptions struct {
	Issuer   string
	Audience string
}

// VerifyJWTWithOptions verifies a JWT and validates issuer/audience when set.
// A nil opts is equivalent to calling VerifyJWT directly.
func VerifyJWTWithOptions(token string, pubKey ed25519.PublicKey, opts *VerifyOptions) (*Claims, error) {
	claims, err := VerifyJWT(token, pubKey)
	if err != nil {
		return nil, err
	}

	if opts == nil {
		return claims, nil
	}

	if opts.Issuer != "" && claims.Issuer != opts.Issuer {
		return nil, fmt.Errorf("identity: issuer mismatch: got %q, want %q", claims.Issuer, opts.Issuer)
	}

	if opts.Audience != "" && !slices.Contains(claims.Audience, opts.Audience) {
		return nil, fmt.Errorf("identity: audience mismatch: got %v, want %q", claims.Audience, opts.Audience)
	}

	return claims, nil
}

func base64Encode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func base64Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
