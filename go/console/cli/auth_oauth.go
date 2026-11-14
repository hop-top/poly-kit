package cli

import (
	"context"
	"errors"
	"fmt"

	"hop.top/kit/go/storage/secret/oauth"
)

// OAuthTokenSource is the minimal token source an OAuthIntrospector reads.
// Token returns a currently valid access token, minting one when needed;
// Invalidate drops any cached token so the next Token call mints afresh.
type OAuthTokenSource interface {
	Token(ctx context.Context) (oauth.Token, error)
	Invalidate()
}

// OAuthIntrospector implements AuthIntrospector over an OAuthTokenSource.
type OAuthIntrospector struct {
	src      OAuthTokenSource
	identity string
	scopes   []string
}

var _ AuthIntrospector = (*OAuthIntrospector)(nil)

// NewOAuthIntrospector reports identity and scopes alongside the source's
// token lifecycle. Every OAuth grant kind can mint a new token, so the
// credential is always Renewable.
func NewOAuthIntrospector(src OAuthTokenSource, identity string, scopes []string) *OAuthIntrospector {
	return &OAuthIntrospector{src: src, identity: identity, scopes: append([]string(nil), scopes...)}
}

var errNoTokenSource = errors.New("oauth: no token source configured")

// Inspect returns the active credential. It never exposes the token value.
func (a *OAuthIntrospector) Inspect(ctx context.Context) (*Credential, error) {
	if a == nil || a.src == nil {
		return nil, errNoTokenSource
	}
	tok, err := a.src.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect oauth credential: %w", err)
	}
	c := &Credential{
		Source:    "exchange",
		Identity:  a.identity,
		Scopes:    append([]string(nil), a.scopes...),
		Renewable: true,
	}
	if !tok.ExpiresAt.IsZero() {
		exp := tok.ExpiresAt
		c.ExpiresAt = &exp
	}
	return c, nil
}

// Refresh discards the cached token and mints a new one.
func (a *OAuthIntrospector) Refresh(ctx context.Context) error {
	if a == nil || a.src == nil {
		return errNoTokenSource
	}
	a.src.Invalidate()
	if _, err := a.src.Token(ctx); err != nil {
		return fmt.Errorf("refresh oauth credential: %w", err)
	}
	return nil
}

var _ OAuthTokenSource = (*oauth.Source)(nil)
