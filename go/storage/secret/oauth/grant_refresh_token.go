package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hop.top/kit/go/storage/secret"
)

// checkRefreshWritable refuses, at configuration time, a store that cannot
// hold a rotated refresh token. backend is a short label for the message.
func checkRefreshWritable(store secret.Store, backend string) error {
	if err := RequireWritable(store, backend); err != nil {
		return err
	}
	if _, ok := store.(secret.MutableStore); !ok {
		return &NotWritableError{Backend: backend}
	}
	return nil
}

// fetchRefreshToken performs the OAuth 2.0 refresh token grant (RFC 6749
// section 6). The refresh token is read from the uma "secret" envelope at
// g.SecretRef; the optional client secret from g.ClientSecretRef (public
// clients send client_id only). Credentials go in the form body. When the
// response carries a new refresh token it is written back to g.SecretRef
// before the access token is returned; if that write fails the old token is
// left in place and an error is returned. hc may be nil. ref is unused. No
// caching. Source does not use this wrapper: it drives exchangeRefresh so a
// failed write-back keeps the rotated token instead of dropping it.
func fetchRefreshToken(ctx context.Context, store secret.Store, ref string, g Grant, hc *http.Client) (CachedToken, error) {
	_ = ref
	if err := g.Validate(); err != nil {
		return CachedToken{}, err
	}
	if g.Kind != RefreshToken {
		return CachedToken{}, invalid("kind", "not refresh_token")
	}
	if err := checkRefreshWritable(store, "refresh token store"); err != nil {
		return CachedToken{}, err
	}
	rt, err := readSecret(ctx, store, g.SecretRef, "secret_ref")
	if err != nil {
		return CachedToken{}, err
	}
	ct, rotated, exErr := exchangeRefresh(ctx, store, g, hc, rt)
	if rotated != "" {
		if err := storeRefreshToken(ctx, store.(secret.MutableStore), g.SecretRef, rotated); err != nil {
			return CachedToken{}, fmt.Errorf("oauth: persist rotated refresh token: %w", ErrTransient)
		}
	}
	if exErr != nil {
		return CachedToken{}, exErr
	}
	return ct, nil
}

// storeRefreshToken writes rt as a uma "secret" envelope at key.
func storeRefreshToken(ctx context.Context, ms secret.MutableStore, key, rt string) error {
	env, err := encodeSecret(rt)
	if err != nil {
		return err
	}
	return ms.Set(ctx, key, env)
}

// exchangeRefresh posts the refresh token rt and returns the access token
// plus the rotated refresh token ("" when the provider did not rotate or
// echoed rt). The rotated token is returned even with a non-nil error when
// the provider answered 2xx. It never writes the store.
func exchangeRefresh(ctx context.Context, store secret.Store, g Grant, hc *http.Client, rt string) (CachedToken, string, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {rt},
		"client_id":     {g.ClientID},
	}
	if g.ClientSecretRef != "" {
		cs, err := readSecret(ctx, store, g.ClientSecretRef, "client_secret_ref")
		if err != nil {
			return CachedToken{}, "", err
		}
		form.Set("client_secret", cs)
	}
	if len(g.Scopes) > 0 {
		form.Set("scope", strings.Join(g.Scopes, " "))
	}
	resp, err := postForm(ctx, hc, g.Kind, g.TokenURL, form)
	if err != nil {
		return CachedToken{}, "", err
	}
	// The rotated token is reported on every outcome after a 2xx reply: the
	// old one is dead at the provider even when the access token is unusable.
	rotated := ""
	if resp.RefreshToken != rt {
		rotated = resp.RefreshToken
	}
	if resp.AccessToken == "" || resp.ExpiresIn <= 0 {
		return CachedToken{}, rotated, errTokenIncomplete
	}
	ct, err := cachedFrom(resp.AccessToken, time.Now().Add(time.Duration(resp.ExpiresIn)*time.Second))
	if err != nil {
		return CachedToken{}, rotated, err
	}
	return ct, rotated, nil
}
