package oauth

import (
	"context"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"hop.top/kit/go/storage/secret"
)

// fetchClientCredentials performs the OAuth 2.0 client credentials grant
// with a client secret (RFC 6749 section 4.4). The secret is read from the
// uma "secret" envelope at g.SecretRef and sent in the request body. hc may
// be nil. ref is the grant's own key; unused here. No caching.
func fetchClientCredentials(ctx context.Context, store secret.Store, ref string, g Grant, hc *http.Client) (CachedToken, error) {
	_ = ref
	if err := g.Validate(); err != nil {
		return CachedToken{}, err
	}
	if g.Kind != ClientCredentials {
		return CachedToken{}, invalid("kind", "not client_credentials")
	}
	sec, err := readSecret(ctx, store, g.SecretRef, "secret_ref")
	if err != nil {
		return CachedToken{}, err
	}
	cfg := clientcredentials.Config{
		ClientID:     g.ClientID,
		ClientSecret: sec,
		TokenURL:     g.TokenURL,
		Scopes:       g.Scopes,
		AuthStyle:    oauth2.AuthStyleInParams,
	}
	ctx = oauth2Context(ctx, hc)
	tok, err := cfg.Token(ctx)
	if err != nil {
		return CachedToken{}, mapTokenError(ctx, g.Kind, err)
	}
	return cachedFrom(tok.AccessToken, tok.Expiry)
}
