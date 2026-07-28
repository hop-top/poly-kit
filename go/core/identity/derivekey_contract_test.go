package identity

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestDeriveKeyContract pins DeriveKey to the shared cross-language fixture.
// DeriveKey is deterministic, so any drift here silently breaks every port
// that reads an encrypted store written by another language.
func TestDeriveKeyContract(t *testing.T) {
	path := filepath.Join("..", "..", "..", "contracts", "identity-v1", "derive-key.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var fixture struct {
		Version   int `json:"version"`
		DeriveKey []struct {
			Name       string `json:"name"`
			Seed       string `json:"seed"`
			PublicKey  string `json:"public_key"`
			DerivedKey string `json:"derived_key"`
		} `json:"derive_key"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if fixture.Version != 1 {
		t.Fatalf("unexpected fixture version %d", fixture.Version)
	}
	if len(fixture.DeriveKey) == 0 {
		t.Fatal("fixture has no cases")
	}

	for _, tc := range fixture.DeriveKey {
		t.Run(tc.Name, func(t *testing.T) {
			seed, err := hex.DecodeString(tc.Seed)
			if err != nil {
				t.Fatalf("decode seed: %v", err)
			}
			priv := ed25519.NewKeyFromSeed(seed)
			kp := &Keypair{
				PublicKey:  priv.Public().(ed25519.PublicKey),
				PrivateKey: priv,
			}

			if got := hex.EncodeToString(kp.PublicKey); got != tc.PublicKey {
				t.Fatalf("public key = %s, want %s", got, tc.PublicKey)
			}

			key := kp.DeriveKey()
			if got := hex.EncodeToString(key[:]); got != tc.DerivedKey {
				t.Fatalf("derived key = %s, want %s", got, tc.DerivedKey)
			}
		})
	}
}
