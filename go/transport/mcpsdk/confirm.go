package mcpsdk

// Confirmation state for MRTR (SEP-2322) exchanges: the opaque
// requestState a server hands a client alongside an elicitation, and
// verifies when the client retries with the answer. Both the tasks
// binding and the synchronous confirmation gate mint and verify it
// here, so there is one signing scheme and one set of rules.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// confirmBinding is the request context a requestState is bound to.
// A state presented for a different leaf, different arguments, or by
// a different principal fails verification outright.
type confirmBinding struct {
	// leaf is the leaf path key ("widget purge").
	leaf string
	// principal identifies the caller the prompt was issued to; empty
	// when the transport has no identity to bind.
	principal string
	// args is the digest of the call's arguments (see argsDigest);
	// empty for a binding that does not cover them.
	args string
}

// confirmStatus is the outcome of verifying a presented requestState.
type confirmStatus int

const (
	// confirmValid is an authentic, unexpired state for this binding.
	confirmValid confirmStatus = iota
	// confirmExpired is an authentic state past its lifetime: a
	// routine re-prompt, not a security event.
	confirmExpired
	// confirmInvalid is a state that is malformed, forged, or bound to
	// a different request: never honored.
	confirmInvalid
)

// confirmClaim is the signed content of a confirmation requestState.
type confirmClaim struct {
	Nonce     string `json:"n"`
	Leaf      string `json:"l"`
	Principal string `json:"p"`
	Args      string `json:"a,omitempty"`
	Expiry    int64  `json:"e"`
}

// confirmer mints and verifies confirmation requestStates under one
// HMAC key.
type confirmer struct {
	key []byte
	ttl time.Duration
}

// newConfirmer returns a confirmer signing with key, or with a fresh
// random key when key is empty. A random key makes state verifiable
// only by the process that minted it, which is exactly the scope of a
// single-instance server; instances behind a load balancer share one
// key.
func newConfirmer(key []byte, ttl time.Duration) (*confirmer, error) {
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("mcpsdk: generating confirmation key: %w", err)
		}
	}
	return &confirmer{key: append([]byte(nil), key...), ttl: ttl}, nil
}

// mint returns the MRTR input-request key the answer must come back
// under, and the signed requestState for b.
func (c *confirmer) mint(b confirmBinding) (key, state string) {
	var nb [8]byte
	if _, err := rand.Read(nb[:]); err != nil {
		panic(fmt.Sprintf("mcpsdk: crypto/rand unavailable: %v", err))
	}
	nonce := hex.EncodeToString(nb[:])
	claim, _ := json.Marshal(confirmClaim{
		Nonce:     nonce,
		Leaf:      b.leaf,
		Principal: b.principal,
		Args:      b.args,
		Expiry:    time.Now().Add(c.ttl).Unix(),
	})
	payload := base64.RawURLEncoding.EncodeToString(claim)
	return "confirm/" + nonce, payload + "." + c.sign(payload)
}

// verify checks a presented state against b and returns the input
// key the answer must appear under. Authenticity and binding are
// decided before expiry, so confirmExpired is only ever reported for
// a state this confirmer minted for exactly this binding.
func (c *confirmer) verify(state string, b confirmBinding) (key string, st confirmStatus) {
	payload, mac, found := cutLast(state)
	if !found || !hmac.Equal([]byte(mac), []byte(c.sign(payload))) {
		return "", confirmInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", confirmInvalid
	}
	var claim confirmClaim
	if json.Unmarshal(raw, &claim) != nil {
		return "", confirmInvalid
	}
	if claim.Leaf != b.leaf || claim.Principal != b.principal || claim.Args != b.args {
		return "", confirmInvalid
	}
	if time.Now().Unix() > claim.Expiry {
		return "", confirmExpired
	}
	return "confirm/" + claim.Nonce, confirmValid
}

// sign returns the hex HMAC-SHA256 of payload under the key.
func (c *confirmer) sign(payload string) string {
	m := hmac.New(sha256.New, c.key)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

// argsDigest returns the hex SHA-256 of raw arguments in canonical
// form: decoded and re-encoded, which sorts object keys at every
// level, so equal argument sets digest identically whatever the
// client's key order. Absent or undecodable arguments digest as
// "null".
func argsDigest(raw json.RawMessage) string {
	var v any
	canonical := []byte("null")
	if len(raw) > 0 && json.Unmarshal(raw, &v) == nil {
		if b, err := json.Marshal(v); err == nil {
			canonical = b
		}
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// cutLast splits s at its final dot.
func cutLast(s string) (before, after string, found bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
