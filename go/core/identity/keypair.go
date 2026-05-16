package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"hop.top/kit/go/core/util"
)

// Keypair holds an Ed25519 signing keypair.
type Keypair struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// Generate creates a new random Ed25519 keypair.
func Generate() (*Keypair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: generate keypair: %w", err)
	}
	return &Keypair{PublicKey: pub, PrivateKey: priv}, nil
}

// PublicKeyID returns a short hex fingerprint of the public key
// (first 8 bytes of SHA-256).
func (k *Keypair) PublicKeyID() string {
	return util.Short(k.PublicKey, 16)
}

// MarshalPublicKey returns the PEM-encoded public key.
func (k *Keypair) MarshalPublicKey() ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(k.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("identity: marshal public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: der,
	}), nil
}

// MarshalPrivateKey returns the PEM-encoded private key.
func (k *Keypair) MarshalPrivateKey() ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("identity: marshal private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der,
	}), nil
}

// ParsePublicKey loads a public key from PEM bytes.
func ParsePublicKey(data []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("identity: no PEM block found")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("identity: parse public key: %w", err)
	}
	key, ok := pub.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("identity: not an Ed25519 public key")
	}
	return key, nil
}

// ParsePrivateKey loads a keypair from PEM-encoded private key bytes.
func ParsePrivateKey(data []byte) (*Keypair, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("identity: no PEM block found")
	}
	priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("identity: parse private key: %w", err)
	}
	key, ok := priv.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("identity: not an Ed25519 private key")
	}
	return &Keypair{
		PublicKey:  key.Public().(ed25519.PublicKey),
		PrivateKey: key,
	}, nil
}
