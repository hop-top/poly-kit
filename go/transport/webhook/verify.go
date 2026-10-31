package webhook

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // GitHub-style X-Hub-Signature, opt-in
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Verifier decides whether a delivery came from the sender.
type Verifier interface {
	Verify(r *http.Request, body []byte) error
}

// VerifierFunc adapts a function to [Verifier].
type VerifierFunc func(r *http.Request, body []byte) error

// Verify implements [Verifier].
func (f VerifierFunc) Verify(r *http.Request, body []byte) error { return f(r, body) }

// ErrUnverified is every refusal a verifier makes; the reason is for
// logs and hooks, never for the sender.
var ErrUnverified = errors.New("webhook: delivery not verified")

func unverified(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnverified, fmt.Sprintf(format, args...))
}

// HMACConfig configures [HMAC].
type HMACConfig struct {
	// Header carries the signature.
	Header string
	// Secret is the shared key.
	Secret []byte
	// Hash is "sha256" (default), "sha1" or "sha512".
	Hash string
	// Prefix precedes the digest in the header ("sha256=").
	Prefix string
	// Encoding is "hex" (default) or "base64".
	Encoding string
	// TimestampHeader, when set, names a header holding Unix seconds;
	// the signed payload becomes "<timestamp>.<body>" and the value is
	// the delivery's timestamp for the replay window.
	TimestampHeader string
}

type hmacVerifier struct {
	cfg  HMACConfig
	newH func() hash.Hash
}

// HMAC verifies a keyed digest of the body in a header, compared in
// constant time.
func HMAC(cfg HMACConfig) (Verifier, error) {
	if cfg.Header == "" || len(cfg.Secret) == 0 {
		return nil, errors.New("webhook: HMAC needs a header and a secret")
	}
	v := &hmacVerifier{cfg: cfg}
	switch cfg.Hash {
	case "", "sha256":
		v.newH = sha256.New
	case "sha1":
		v.newH = sha1.New //nolint:gosec
	case "sha512":
		v.newH = sha512.New
	default:
		return nil, fmt.Errorf("webhook: HMAC hash %q: want sha256, sha1 or sha512", cfg.Hash)
	}
	switch cfg.Encoding {
	case "", "hex", "base64":
	default:
		return nil, fmt.Errorf("webhook: HMAC encoding %q: want hex or base64", cfg.Encoding)
	}
	return v, nil
}

// Sign returns the header value a sender would send for body (and
// timestamp, when TimestampHeader is set): for tests and senders.
func (v *hmacVerifier) sign(ts string, body []byte) string {
	m := hmac.New(v.newH, v.cfg.Secret)
	if v.cfg.TimestampHeader != "" {
		m.Write([]byte(ts))
		m.Write([]byte("."))
	}
	m.Write(body)
	sum := m.Sum(nil)
	if v.cfg.Encoding == "base64" {
		return v.cfg.Prefix + base64.StdEncoding.EncodeToString(sum)
	}
	return v.cfg.Prefix + hex.EncodeToString(sum)
}

// SignHMAC returns the header value [HMAC] expects for body, for
// tests and for tools that send webhooks.
func SignHMAC(cfg HMACConfig, ts string, body []byte) (string, error) {
	v, err := HMAC(cfg)
	if err != nil {
		return "", err
	}
	return v.(*hmacVerifier).sign(ts, body), nil
}

func (v *hmacVerifier) Verify(r *http.Request, body []byte) error {
	got := r.Header.Get(v.cfg.Header)
	if got == "" {
		return unverified("no %s header", v.cfg.Header)
	}
	ts := ""
	if v.cfg.TimestampHeader != "" {
		if ts = r.Header.Get(v.cfg.TimestampHeader); ts == "" {
			return unverified("no timestamp header %s", v.cfg.TimestampHeader)
		}
		if !plainUnixSeconds(ts) {
			return unverified("timestamp header %s is not Unix seconds", v.cfg.TimestampHeader)
		}
	}
	want := v.sign(ts, body)
	// Compare digests of both, so a length difference leaks nothing.
	a, b := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))
	if subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		return unverified("signature mismatch")
	}
	return nil
}

// plainUnixSeconds reports whether ts is exactly the decimal form of a
// non-negative int64: ASCII digits only, no sign, space or leading zero.
// It is checked before the MAC, so a malformed value never reaches it.
func plainUnixSeconds(ts string) bool {
	if len(ts) == 0 || len(ts) > 19 {
		return false
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	return err == nil && n >= 0 && strconv.FormatInt(n, 10) == ts
}

// ClientState verifies the shared secret MS Graph echoes in every
// notification of a delivery: each value[].clientState must equal
// expected, and there must be at least one.
func ClientState(expected string) Verifier {
	want := sha256.Sum256([]byte(expected))
	return VerifierFunc(func(_ *http.Request, body []byte) error {
		var doc struct {
			Value []struct {
				ClientState string `json:"clientState"`
			} `json:"value"`
		}
		if err := json.Unmarshal(body, &doc); err != nil || len(doc.Value) == 0 {
			return unverified("no notifications")
		}
		ok := 1
		for _, n := range doc.Value {
			got := sha256.Sum256([]byte(n.ClientState))
			ok &= subtle.ConstantTimeCompare(got[:], want[:])
		}
		if ok != 1 {
			return unverified("clientState mismatch")
		}
		return nil
	})
}

// BasicAuth verifies HTTP basic credentials, in constant time.
func BasicAuth(user, password string) Verifier {
	wu, wp := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(password))
	return VerifierFunc(func(r *http.Request, _ []byte) error {
		u, p, ok := r.BasicAuth()
		if !ok {
			return unverified("no basic credentials")
		}
		gu, gp := sha256.Sum256([]byte(u)), sha256.Sum256([]byte(p))
		if subtle.ConstantTimeCompare(gu[:], wu[:])&subtle.ConstantTimeCompare(gp[:], wp[:]) != 1 {
			return unverified("credentials mismatch")
		}
		return nil
	})
}

// TimestampFunc reads a delivery's own timestamp.
type TimestampFunc func(r *http.Request, body []byte) (time.Time, bool)

// HeaderUnixTimestamp reads Unix seconds from a header.
func HeaderUnixTimestamp(header string) TimestampFunc {
	return func(r *http.Request, _ []byte) (time.Time, bool) {
		n, err := strconv.ParseInt(strings.TrimSpace(r.Header.Get(header)), 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(n, 0), true
	}
}

// JSONTimestamp reads RFC 3339 text or Unix seconds at a JSON pointer.
func JSONTimestamp(pointer string) TimestampFunc {
	return func(_ *http.Request, body []byte) (time.Time, bool) {
		switch v := jsonPointer(body, pointer).(type) {
		case string:
			t, err := time.Parse(time.RFC3339Nano, v)
			return t, err == nil
		case float64:
			return time.Unix(int64(v), 0), true
		}
		return time.Time{}, false
	}
}

// KeyFunc extracts the idempotency key; "" means none.
type KeyFunc func(r *http.Request, body []byte) string

// HeaderKey keys on a header. The header is not authenticated, so it
// only names the delivery in the published event; the idempotency claim
// is the body's hash (and a signed timestamp, when the receiver has
// one): changing the header of a captured request is not a new delivery.
func HeaderKey(header string) KeyFunc {
	return func(r *http.Request, _ []byte) string { return strings.TrimSpace(r.Header.Get(header)) }
}

// JSONKey keys on the string at a JSON pointer.
func JSONKey(pointer string) KeyFunc {
	return func(_ *http.Request, body []byte) string {
		s, _ := jsonPointer(body, pointer).(string)
		return s
	}
}

func jsonPointer(body []byte, pointer string) any {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return nil
	}
	for _, seg := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		if v, ok = m[seg]; !ok {
			return nil
		}
	}
	return v
}
