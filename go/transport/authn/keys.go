package authn

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"

	jose "github.com/go-jose/go-jose/v4"

	"hop.top/kit/go/core/identity"
)

// Key is one trusted public key and the kid tokens it signed carry.
type Key struct {
	// ID is the kid: a token naming it is checked against this key.
	ID string
	// Public is an ed25519.PublicKey, *rsa.PublicKey or
	// *ecdsa.PublicKey.
	Public crypto.PublicKey
}

// IdentityKey is the public half of a kit identity keypair, with the
// kid [identity.Keypair.SignJWT] embeds.
func IdentityKey(kp *identity.Keypair) Key {
	return Key{ID: kp.PublicKeyID(), Public: kp.PublicKey}
}

// ParsePublicKeyPEM parses a PEM "PUBLIC KEY" block (PKIX): Ed25519,
// RSA or ECDSA. The kid is the one kit's identity keypairs use for an
// Ed25519 key, so a token another kit tool signed with its identity
// matches; for RSA and ECDSA it is the RFC 7638 thumbprint.
func ParsePublicKeyPEM(data []byte) (Key, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return Key{}, errors.New("no PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return Key{}, err
	}
	switch k := pub.(type) {
	case ed25519.PublicKey:
		return IdentityKey(&identity.Keypair{PublicKey: k}), nil
	case *rsa.PublicKey, *ecdsa.PublicKey:
		jwk := jose.JSONWebKey{Key: k}
		sum, err := jwk.Thumbprint(crypto.SHA256)
		if err != nil {
			return Key{}, err
		}
		return Key{ID: base64.RawURLEncoding.EncodeToString(sum), Public: k}, nil
	}
	return Key{}, fmt.Errorf("unsupported public key type %T", pub)
}

// NewJWT returns a verifier for tokens signed by one of keys: the
// token's kid selects the key, and a token whose kid no key has — an
// external signer's own naming — or that names none is tried against
// each; the signature decides. Rotation is adding the new key and,
// until the tokens the old one signed have expired, keeping the old
// one.
func NewJWT(keys []Key, opts Options) (*Verifier, error) {
	if len(keys) == 0 {
		return nil, errors.New("authn: no key to verify with")
	}
	set := make(staticKeys, 0, len(keys))
	for _, k := range keys {
		switch k.Public.(type) {
		case ed25519.PublicKey, *rsa.PublicKey, *ecdsa.PublicKey:
		default:
			return nil, fmt.Errorf("authn: unsupported public key type %T", k.Public)
		}
		set = append(set, jose.JSONWebKey{Key: k.Public, KeyID: k.ID, Use: "sig"})
	}
	return &Verifier{keys: set, opts: opts}, nil
}

// staticKeys is a fixed key set.
type staticKeys []jose.JSONWebKey

func (s staticKeys) lookup(_ context.Context, kid string) ([]jose.JSONWebKey, error) {
	if keys := match(s, kid); len(keys) > 0 {
		return keys, nil
	}
	return match(s, ""), nil
}

// match is the signing keys among keys that kid selects: those with
// that kid, or every one when kid is empty.
func match(keys []jose.JSONWebKey, kid string) []jose.JSONWebKey {
	var out []jose.JSONWebKey
	for _, k := range keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if !k.IsPublic() {
			k = k.Public()
		}
		if kid == "" || k.KeyID == kid {
			out = append(out, k)
		}
	}
	return out
}
