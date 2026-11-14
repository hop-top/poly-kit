package oauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // x5t is a SHA-1 thumbprint by spec (RFC 7515 4.1.7)
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"hop.top/kit/go/storage/secret"
)

const (
	caAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
	caAssertionTTL  = 5 * time.Minute
)

// fetchClientAssertion runs the client credentials grant authenticated with
// a signed client_assertion JWT (RS256, x5t thumbprint header) built from
// the certificate and private key at g.ClientCertRef. No client_secret is
// sent. The result is not cached.
func fetchClientAssertion(ctx context.Context, store secret.Store, ref string, g Grant, hc *http.Client) (CachedToken, error) {
	if err := g.Validate(); err != nil {
		return CachedToken{}, err
	}
	if g.Kind != ClientAssertion {
		return CachedToken{}, invalid("kind", "must be client_assertion")
	}
	cert, key, err := readClientCert(ctx, store, g.ClientCertRef)
	if err != nil {
		return CachedToken{}, err
	}
	assertion, err := caSign(cert, key, g.ClientID, g.TokenURL, time.Now())
	if err != nil {
		return CachedToken{}, err
	}
	cfg := clientcredentials.Config{
		ClientID:  g.ClientID,
		TokenURL:  g.TokenURL,
		Scopes:    g.Scopes,
		AuthStyle: oauth2.AuthStyleInParams,
		EndpointParams: url.Values{
			"client_assertion":      {assertion},
			"client_assertion_type": {caAssertionType},
		},
	}
	ctx = oauth2Context(ctx, hc)
	tok, err := cfg.Token(ctx)
	if err != nil {
		return CachedToken{}, mapTokenError(ctx, g.Kind, err)
	}
	return cachedFrom(tok.AccessToken, tok.Expiry)
}

// caSign builds the compact RS256 JWT. The x5t header is the base64url
// SHA-1 thumbprint of the DER certificate.
func caSign(cert *x509.Certificate, key *rsa.PrivateKey, clientID, tokenURL string, now time.Time) (string, error) {
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", fmt.Errorf("oauth: assertion id: %w", err)
	}
	thumb := sha1.Sum(cert.Raw) //nolint:gosec // see import
	enc := base64.RawURLEncoding
	hdr, _ := marshal(map[string]string{"alg": "RS256", "typ": "JWT", "x5t": enc.EncodeToString(thumb[:])})
	claims, _ := marshal(map[string]any{
		"aud": tokenURL, "iss": clientID, "sub": clientID,
		"jti": enc.EncodeToString(jti),
		"nbf": now.Unix(), "iat": now.Unix(),
		"exp": now.Add(caAssertionTTL).Unix(),
	})
	signing := enc.EncodeToString(hdr) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("oauth: sign assertion: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}
