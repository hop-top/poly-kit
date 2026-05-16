package cmdsurface

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// WebhookAuth chooses the auth scheme used by an inbound webhook
// mapping. Verify is called by the surface BEFORE template execution.
// Returning an error rejects the request with 401 (or 400 for shape
// errors). The raw body is provided so HMAC implementations can
// compute their digest against the unmodified bytes.
type WebhookAuth interface {
	Verify(r *http.Request, body []byte) error
}

// AuthNone passes every request. Use only when the upstream caller
// is trusted or another layer (network ACL, mTLS) protects the
// endpoint. The surface refuses to mount AuthNone on a leaf whose
// Class.AuthRequired is true.
type AuthNone struct{}

// Verify implements WebhookAuth and always returns nil.
func (AuthNone) Verify(*http.Request, []byte) error { return nil }

// AuthHMAC verifies an HMAC-SHA256 signature against the raw request
// body. Header names the request header carrying the hex-encoded
// digest (e.g. "X-Hub-Signature-256" for GitHub). Prefix is an
// optional leading string stripped from the header value before
// decoding (e.g. "sha256=" for GitHub). Secret is the shared HMAC
// secret, typically loaded from env at construction.
type AuthHMAC struct {
	Header string
	Prefix string
	Secret []byte
}

// Verify implements WebhookAuth.
func (a AuthHMAC) Verify(r *http.Request, body []byte) error {
	if a.Header == "" {
		return errors.New("cmdsurface: AuthHMAC.Header unset")
	}
	if len(a.Secret) == 0 {
		return errors.New("cmdsurface: AuthHMAC.Secret unset")
	}
	got := r.Header.Get(a.Header)
	if got == "" {
		return errors.New("cmdsurface: missing signature header")
	}
	if a.Prefix != "" {
		if !strings.HasPrefix(got, a.Prefix) {
			return errors.New("cmdsurface: signature prefix mismatch")
		}
		got = got[len(a.Prefix):]
	}
	gotBytes, err := hex.DecodeString(got)
	if err != nil {
		return errors.New("cmdsurface: signature hex decode")
	}
	mac := hmac.New(sha256.New, a.Secret)
	mac.Write(body)
	want := mac.Sum(nil)
	if !hmac.Equal(gotBytes, want) {
		return errors.New("cmdsurface: signature mismatch")
	}
	return nil
}

// AuthBearer verifies an Authorization: Bearer <token> header. Token
// is the expected shared secret. Comparison is constant-time.
type AuthBearer struct {
	Token string
}

// Verify implements WebhookAuth.
func (a AuthBearer) Verify(r *http.Request, _ []byte) error {
	if a.Token == "" {
		return errors.New("cmdsurface: AuthBearer.Token unset")
	}
	got := r.Header.Get("Authorization")
	const pfx = "Bearer "
	if !strings.HasPrefix(got, pfx) {
		return errors.New("cmdsurface: missing bearer prefix")
	}
	tok := got[len(pfx):]
	// Constant-time compare via hmac.Equal on equal-length slices.
	a1 := []byte(tok)
	a2 := []byte(a.Token)
	if len(a1) != len(a2) {
		return errors.New("cmdsurface: bearer token mismatch")
	}
	if !hmac.Equal(a1, a2) {
		return errors.New("cmdsurface: bearer token mismatch")
	}
	return nil
}
