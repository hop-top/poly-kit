package peer

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"hop.top/kit/go/core/identity"
)

// TrustManager handles trust establishment between peers.
type TrustManager struct {
	registry *Registry
	self     *identity.Keypair
}

// NewTrustManager creates a TrustManager.
func NewTrustManager(reg *Registry, self *identity.Keypair) *TrustManager {
	return &TrustManager{registry: reg, self: self}
}

// AcceptTOFU accepts a peer on first encounter (Trust-On-First-Use).
// First-seen peers are set to PendingTOFU; explicit Trust() promotes them.
// If the peer is already known, verifies the public key matches.
func (tm *TrustManager) AcceptTOFU(info PeerInfo) error {
	if err := info.Validate(); err != nil {
		return err
	}
	existing, err := tm.registry.Get(info.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		// First encounter: add as pending (not trusted)
		if err := tm.registry.Add(info); err != nil {
			return err
		}
		return tm.registry.SetTrust(info.ID, PendingTOFU)
	}
	// Already known: verify pubkey matches
	if !bytes.Equal(existing.PublicKey, info.PublicKey) {
		return fmt.Errorf("peer: pubkey mismatch for %s (possible impersonation)", info.ID)
	}
	return tm.registry.UpdateLastSeen(info.ID)
}

// Verify checks that a PeerInfo's pubkey matches the stored record.
func (tm *TrustManager) Verify(info PeerInfo) (bool, error) {
	existing, err := tm.registry.Get(info.ID)
	if err != nil {
		return false, err
	}
	if existing == nil {
		return false, nil
	}
	return bytes.Equal(existing.PublicKey, info.PublicKey), nil
}

// Trust explicitly marks a peer as trusted.
func (tm *TrustManager) Trust(id string) error {
	return tm.registry.SetTrust(id, Trusted)
}

// Block explicitly blocks a peer.
func (tm *TrustManager) Block(id string) error {
	return tm.registry.SetTrust(id, Blocked)
}

// Revoke removes trust (sets to Unknown).
func (tm *TrustManager) Revoke(id string) error {
	return tm.registry.SetTrust(id, Unknown)
}

// IsBlocked checks if a peer is blocked.
func (tm *TrustManager) IsBlocked(id string) (bool, error) {
	rec, err := tm.registry.Get(id)
	if err != nil {
		return false, err
	}
	if rec == nil {
		return false, nil
	}
	return rec.Trust == Blocked, nil
}

// IsTrusted checks if a peer is trusted.
func (tm *TrustManager) IsTrusted(id string) (bool, error) {
	rec, err := tm.registry.Get(id)
	if err != nil {
		return false, err
	}
	if rec == nil {
		return false, nil
	}
	return rec.Trust == Trusted, nil
}

// Challenge creates a signed JWT challenge for peer verification.
// The targetPeerID is set as the audience to prevent replay to other peers.
func (tm *TrustManager) Challenge(targetPeerID string) (string, error) {
	nonce, err := randomHex(16)
	if err != nil {
		return "", fmt.Errorf("peer: generate nonce: %w", err)
	}
	claims := identity.Claims{
		Subject:   tm.self.PublicKeyID(),
		Issuer:    "kit-peer",
		Audience:  identity.Audience{targetPeerID},
		Nonce:     nonce,
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(30 * time.Second).Unix(),
	}
	return tm.self.SignJWT(claims)
}

// VerifyChallenge verifies a peer's signed challenge token.
// Checks that the audience matches the verifier's own ID.
func (tm *TrustManager) VerifyChallenge(token string, peerPubKey []byte) error {
	pubKey, err := identity.ParsePublicKey(peerPubKey)
	if err != nil {
		return fmt.Errorf("peer: parse pubkey: %w", err)
	}
	claims, err := identity.VerifyJWT(token, pubKey)
	if err != nil {
		return errors.New("peer: challenge verification failed")
	}
	// Verify audience matches our own ID to prevent replay attacks
	if !slices.Contains(claims.Audience, tm.self.PublicKeyID()) {
		return errors.New("peer: challenge audience mismatch")
	}
	return nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
