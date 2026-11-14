package oauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// TokenVersion is the only cached token envelope version.
const TokenVersion = 1

// tokenKind is the uma credential kind of the cached token.
const tokenKind = "oauth2"

// Token is an access token and its expiry.
type Token struct {
	AccessToken string
	ExpiresAt   time.Time
}

// Expired reports whether the token is within leeway of expiry at now.
func (t Token) Expired(now time.Time, leeway time.Duration) bool {
	return !now.Add(leeway).Before(t.ExpiresAt)
}

// CachedToken is the value stored at TokenKey: a uma "oauth2" envelope.
// User and AuthzID are optional and map to uma's fields of the same name.
type CachedToken struct {
	Token
	User    string
	AuthzID string
}

// wireToken is exactly the uma oauth2 envelope; uma rejects any other field.
type wireToken struct {
	V           int    `json:"v"`
	Kind        string `json:"kind"`
	AccessToken string `json:"access_token"`
	ExpiresAt   string `json:"expires_at"`
	User        string `json:"user,omitempty"`
	AuthzID     string `json:"authzid,omitempty"`
}

var tokenFields = map[string]bool{
	"v": true, "kind": true, "access_token": true, "expires_at": true,
	"user": true, "authzid": true,
}

// Validate requires a non-empty access token and a non-zero expiry.
func (c CachedToken) Validate() error {
	if c.AccessToken == "" {
		return invalid("access_token", "required")
	}
	if c.ExpiresAt.IsZero() {
		return invalid("expires_at", "required")
	}
	return nil
}

// EncodeCachedToken returns the uma oauth2 envelope for c. expires_at is
// RFC 3339 with nanoseconds, in UTC.
func EncodeCachedToken(c CachedToken) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return marshal(wireToken{
		V: TokenVersion, Kind: tokenKind, AccessToken: c.AccessToken,
		ExpiresAt: c.ExpiresAt.UTC().Format(time.RFC3339Nano),
		User:      c.User, AuthzID: c.AuthzID,
	})
}

// DecodeCachedToken parses a stored uma oauth2 envelope strictly: same
// version, same kind, no unknown fields, expires_at required. Errors never
// echo stored text.
func DecodeCachedToken(data []byte) (CachedToken, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return CachedToken{}, invalid("", "not a JSON object")
	}
	for name := range fields {
		if !tokenFields[name] {
			return CachedToken{}, invalid("", "unknown field")
		}
	}
	var w wireToken
	if err := json.Unmarshal(data, &w); err != nil {
		var te *json.UnmarshalTypeError
		if asTypeError(err, &te) && tokenFields[te.Field] {
			return CachedToken{}, invalid(te.Field, "wrong type")
		}
		return CachedToken{}, invalid("", "wrong field type")
	}
	if _, ok := fields["v"]; !ok {
		return CachedToken{}, invalid("v", "required")
	}
	if w.V != TokenVersion {
		return CachedToken{}, invalid("v", "unsupported version")
	}
	if w.Kind != tokenKind {
		return CachedToken{}, invalid("kind", "must be oauth2")
	}
	if w.ExpiresAt == "" {
		return CachedToken{}, invalid("expires_at", "required")
	}
	exp, err := time.Parse(time.RFC3339Nano, w.ExpiresAt)
	if err != nil {
		return CachedToken{}, invalid("expires_at", "not an RFC 3339 time")
	}
	c := CachedToken{
		Token: Token{AccessToken: w.AccessToken, ExpiresAt: exp.UTC()},
		User:  w.User, AuthzID: w.AuthzID,
	}
	if err := c.Validate(); err != nil {
		return CachedToken{}, err
	}
	return c, nil
}

// Digest is the first 16 hex characters of SHA-256 over the canonical JSON
// of {client_cert_ref, client_id, client_secret_ref, kind, scopes (sorted),
// secret_ref, subject, token_url}: keys in that alphabetical order, no
// whitespace, no HTML escaping, absent strings "", scopes always an array.
// Editing any of them, including re-pointing key material, changes the cache
// key, so a token minted for another mailbox, scope or credential is never
// served.
func Digest(g Grant) string {
	scopes := append([]string{}, g.Scopes...)
	sort.Strings(scopes)
	b, _ := marshal(struct {
		ClientCertRef   string    `json:"client_cert_ref"`
		ClientID        string    `json:"client_id"`
		ClientSecretRef string    `json:"client_secret_ref"`
		Kind            GrantKind `json:"kind"`
		Scopes          []string  `json:"scopes"`
		SecretRef       string    `json:"secret_ref"`
		Subject         string    `json:"subject"`
		TokenURL        string    `json:"token_url"`
	}{g.ClientCertRef, g.ClientID, g.ClientSecretRef, g.Kind, scopes, g.SecretRef, g.Subject, g.TokenURL})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// TokenKey is the store key of the cached token for g granted at ref:
// "<ref>.token.<digest>".
func TokenKey(ref string, g Grant) string {
	return ref + ".token." + Digest(g)
}

func asTypeError(err error, target **json.UnmarshalTypeError) bool {
	return errors.As(err, target)
}
