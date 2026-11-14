package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// maxTokenBody caps the token response read.
const maxTokenBody = 1 << 20

// errTokenIncomplete: the endpoint answered 2xx without a usable token.
// Retrying will not help and the credential is not at fault, so no sentinel.
var errTokenIncomplete = errors.New("oauth: token response missing access_token or expires_in")

// noRedirectClient copies hc (the default client when nil) and refuses to
// follow redirects: Go replays POST bodies on 307/308, which would leak
// credentials to the redirect target.
func noRedirectClient(hc *http.Client) *http.Client {
	c := *http.DefaultClient
	if hc != nil {
		c = *hc
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// oauth2Context returns ctx carrying the no-redirect client for x/oauth2.
func oauth2Context(ctx context.Context, hc *http.Client) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, noRedirectClient(hc))
}

// cachedFrom builds a validated CachedToken from an access token and its
// expiry; a zero expiry or empty token is errTokenIncomplete.
func cachedFrom(access string, expiry time.Time) (CachedToken, error) {
	if access == "" || expiry.IsZero() {
		return CachedToken{}, errTokenIncomplete
	}
	ct := CachedToken{Token: Token{AccessToken: access, ExpiresAt: expiry.UTC()}}
	if err := ct.Validate(); err != nil {
		return CachedToken{}, err
	}
	return ct, nil
}

// tokenReply is a decoded 2xx token response.
type tokenReply struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
}

// postForm sends form to tokenURL without following redirects and decodes a
// 2xx reply. Every other outcome is a mapped error.
func postForm(ctx context.Context, hc *http.Client, kind GrantKind, tokenURL string, form url.Values) (tokenReply, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenReply{}, invalid("token_url", "cannot build request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := noRedirectClient(hc).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return tokenReply{}, fmt.Errorf("oauth: token request: %w: %w", ErrTransient, ctx.Err())
		}
		if isTLSVerifyError(err) {
			return tokenReply{}, errTLSVerify
		}
		return tokenReply{}, fmt.Errorf("oauth: token request failed: %w", ErrTransient)
	}
	defer res.Body.Close()
	body, rerr := io.ReadAll(io.LimitReader(res.Body, maxTokenBody))
	var w struct {
		AccessToken  string          `json:"access_token"`
		RefreshToken string          `json:"refresh_token"`
		ExpiresIn    json.RawMessage `json:"expires_in"`
		Error        string          `json:"error"`
	}
	_ = json.Unmarshal(body, &w)
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return tokenReply{}, mapTokenStatus(kind, res.StatusCode, w.Error)
	}
	if rerr != nil {
		return tokenReply{}, fmt.Errorf("oauth: token response unreadable: %w", ErrTransient)
	}
	return tokenReply{AccessToken: w.AccessToken, RefreshToken: w.RefreshToken, ExpiresIn: seconds(w.ExpiresIn)}, nil
}
