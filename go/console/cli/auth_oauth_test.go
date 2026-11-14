package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret/oauth"
)

type fakeSource struct {
	tok         oauth.Token
	err         error
	invalidated int
	calls       int
	failAfter   bool
}

func (f *fakeSource) Token(context.Context) (oauth.Token, error) {
	f.calls++
	if f.failAfter && f.invalidated > 0 {
		return oauth.Token{}, f.err
	}
	return f.tok, f.err
}
func (f *fakeSource) Invalidate() { f.invalidated++ }

func TestOAuthIntrospector_Inspect(t *testing.T) {
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	src := &fakeSource{tok: oauth.Token{AccessToken: "at-secret-value", ExpiresAt: exp}}
	a := NewOAuthIntrospector(src, "me@example.com", []string{"mail.read"})
	var _ AuthIntrospector = a
	c, err := a.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "exchange" || c.Identity != "me@example.com" || !c.Renewable {
		t.Fatalf("cred = %+v", c)
	}
	if c.ExpiresAt == nil || !c.ExpiresAt.Equal(exp) {
		t.Fatalf("expires = %v", c.ExpiresAt)
	}
	if len(c.Scopes) != 1 || c.Scopes[0] != "mail.read" {
		t.Fatalf("scopes = %v", c.Scopes)
	}
	if src.invalidated != 0 {
		t.Fatal("Inspect must not invalidate")
	}
}

func TestOAuthIntrospector_InspectError(t *testing.T) {
	src := &fakeSource{err: oauth.ErrNeedsReconsent}
	_, err := NewOAuthIntrospector(src, "x", nil).Inspect(context.Background())
	if !errors.Is(err, oauth.ErrNeedsReconsent) {
		t.Fatalf("err = %v", err)
	}
}

func TestOAuthIntrospector_Refresh(t *testing.T) {
	src := &fakeSource{tok: oauth.Token{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)}}
	if err := NewOAuthIntrospector(src, "x", nil).Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if src.invalidated != 1 || src.calls != 1 {
		t.Fatalf("invalidated=%d calls=%d", src.invalidated, src.calls)
	}
}

func TestOAuthIntrospector_RefreshError(t *testing.T) {
	src := &fakeSource{err: oauth.ErrTransient, failAfter: true, tok: oauth.Token{AccessToken: "tok-leak"}}
	err := NewOAuthIntrospector(src, "x", nil).Refresh(context.Background())
	if !errors.Is(err, oauth.ErrTransient) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "tok-leak") {
		t.Fatal("token in error")
	}
}

func TestOAuthIntrospector_NilSource(t *testing.T) {
	a := NewOAuthIntrospector(nil, "x", nil)
	if _, err := a.Inspect(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if err := a.Refresh(context.Background()); err == nil {
		t.Fatal("want error")
	}
}
