package oauth

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"

	"golang.org/x/oauth2/jwt"

	"hop.top/kit/go/storage/secret"
)

// fetchJWTBearer exchanges a signed service-account assertion for an access
// token (RFC 7523). g.SecretRef holds a uma "secret" envelope whose value is
// the service account key JSON; client_email, private_key and private_key_id
// are used. g.Subject is the impersonated user (domain-wide delegation) and
// g.Scopes the requested scopes. The grant's token_url is the endpoint and
// the assertion audience; the key JSON's token_uri is ignored. ref is the
// grant's own key and is unused: key material comes from g.SecretRef.
func fetchJWTBearer(ctx context.Context, store secret.Store, ref string, g Grant, hc *http.Client) (CachedToken, error) {
	if err := g.Validate(); err != nil {
		return CachedToken{}, err
	}
	if g.Kind != JWTBearer {
		return CachedToken{}, invalid("kind", "not jwt_bearer")
	}
	sa, err := readServiceAccount(ctx, store, g.SecretRef)
	if err != nil {
		return CachedToken{}, err
	}
	cfg := &jwt.Config{
		Email:        sa.ClientEmail,
		PrivateKey:   []byte(sa.PrivateKey),
		PrivateKeyID: sa.PrivateKeyID,
		Scopes:       g.Scopes,
		Subject:      g.Subject,
		TokenURL:     g.TokenURL,
	}
	ctx = oauth2Context(ctx, hc)
	tok, err := cfg.TokenSource(ctx).Token()
	if err != nil {
		return CachedToken{}, mapTokenError(ctx, g.Kind, err)
	}
	return cachedFrom(tok.AccessToken, tok.Expiry)
}

type serviceAccount struct {
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
}

// readServiceAccount loads and checks the service account key JSON held in
// the secret envelope at key. Errors name fields only, never values.
func readServiceAccount(ctx context.Context, store secret.Store, key string) (serviceAccount, error) {
	var zero serviceAccount
	val, err := readSecret(ctx, store, key, "secret_ref")
	if err != nil {
		return zero, err
	}
	var sa serviceAccount
	if err := json.Unmarshal([]byte(val), &sa); err != nil {
		return zero, invalid("secret_ref", "value is not service account key JSON")
	}
	if sa.ClientEmail == "" {
		return zero, invalid("secret_ref", "service account key missing client_email")
	}
	if sa.PrivateKey == "" {
		return zero, invalid("secret_ref", "service account key missing private_key")
	}
	if !parsesAsKey(sa.PrivateKey) {
		return zero, invalid("secret_ref", "service account private_key is not a PEM RSA key")
	}
	return sa, nil
}

func parsesAsKey(pemKey string) bool {
	blk, _ := pem.Decode([]byte(pemKey))
	if blk == nil {
		return false
	}
	if _, err := x509.ParsePKCS8PrivateKey(blk.Bytes); err == nil {
		return true
	}
	_, err := x509.ParsePKCS1PrivateKey(blk.Bytes)
	return err == nil
}
