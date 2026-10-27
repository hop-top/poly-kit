package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/runtime/bus"
)

const (
	// relayIssuer is the iss of every token a relay signs.
	relayIssuer = "relay"
	// relayTokenTTL is how long a connect token lives: the bus asks for
	// a fresh one on every dial and redial, so it only has to outlive
	// the handshake.
	relayTokenTTL = time.Minute
)

// relayAuth is the relay's bus authenticator, the self-signed client
// model: the connecting relay signs a short-lived token with its own
// identity keypair, and the listener accepts it when the signer's
// public key is one it was told to trust. The token is the one `relay
// token create` and any kit service under auth.mode jwt with that key
// in auth.jwt.public_key_files agree on: EdDSA, the kid in the header.
type relayAuth struct {
	signer  *identity.Keypair
	trusted map[string]ed25519.PublicKey // by kid
}

var _ bus.Authenticator = (*relayAuth)(nil)

// newRelayAuth signs with signer (nil on a listener) and trusts the
// tokens signed by trusted (none on a connector).
func newRelayAuth(signer *identity.Keypair, trusted ...ed25519.PublicKey) *relayAuth {
	a := &relayAuth{signer: signer, trusted: map[string]ed25519.PublicKey{}}
	for _, k := range trusted {
		a.trusted[(&identity.Keypair{PublicKey: k}).PublicKeyID()] = k
	}
	return a
}

// Token signs the token sent on connect: sub is the signer's kid.
func (a *relayAuth) Token() (string, error) {
	if a.signer == nil {
		return "", errors.New("relay: no identity keypair to sign with")
	}
	now := time.Now()
	return a.signer.SignJWT(identity.Claims{
		Subject:   a.signer.PublicKeyID(),
		Issuer:    relayIssuer,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(relayTokenTTL).Unix(),
	})
}

// Verify accepts a token a trusted key signed, unexpired, with an
// expiry and a subject; anything else is bus.ErrAuthFailed.
func (a *relayAuth) Verify(token string) error {
	key, ok := a.trusted[tokenKID(token)]
	if !ok {
		return bus.ErrAuthFailed
	}
	claims, err := identity.VerifyJWT(token, key)
	if err != nil || claims.ExpiresAt == 0 || claims.Subject == "" || claims.Issuer != relayIssuer {
		return bus.ErrAuthFailed
	}
	return nil
}

// tokenKID reads the kid from an EdDSA token's header; "" otherwise.
func tokenKID(token string) string {
	head, _, ok := strings.Cut(token, ".")
	if !ok {
		return ""
	}
	data, err := base64.RawURLEncoding.DecodeString(head)
	if err != nil {
		return ""
	}
	var h struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(data, &h) != nil || h.Alg != "EdDSA" {
		return ""
	}
	return h.Kid
}

// loadTrusted reads the Ed25519 public keys (PEM, as `relay pubkey`
// prints them) in files.
func loadTrusted(files []string) ([]ed25519.PublicKey, error) {
	keys := make([]ed25519.PublicKey, 0, len(files))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("--trust: %w", err)
		}
		k, err := identity.ParsePublicKey(data)
		if err != nil {
			return nil, fmt.Errorf("--trust %s: %w", f, err)
		}
		keys = append(keys, k)
	}
	return keys, nil
}
