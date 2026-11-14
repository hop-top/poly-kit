package oauth_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/file"
	"hop.top/kit/go/storage/secret/keyring"
	"hop.top/kit/go/storage/secret/memory"
	"hop.top/kit/go/storage/secret/oauth"
	"hop.top/kit/go/storage/secret/openbao"
)

func goodGrant(kind oauth.GrantKind) oauth.Grant {
	g := oauth.Grant{
		Kind:     kind,
		TokenURL: "https://idp.example/oauth2/token",
		ClientID: "client-1",
		Scopes:   []string{"mail.read", "calendars.read"},
		Subject:  "user@example.test",
	}
	if kind == oauth.ClientAssertion {
		g.ClientCertRef = "uma/t/cert"
	} else {
		g.SecretRef = "uma/t/secret"
	}
	return g
}

var kinds = []oauth.GrantKind{oauth.ClientCredentials, oauth.ClientAssertion, oauth.JWTBearer, oauth.RefreshToken}

func backends(t *testing.T) map[string]secret.MutableStore {
	return map[string]secret.MutableStore{
		"memory": memory.New(),
		"file":   file.New(t.TempDir(), nil),
	}
}

func TestGrantRoundTripBackends(t *testing.T) {
	for name, st := range backends(t) {
		for _, k := range kinds {
			t.Run(name+"/"+string(k), func(t *testing.T) {
				ctx := context.Background()
				want := goodGrant(k)
				b, err := oauth.EncodeGrant(want)
				if err != nil {
					t.Fatal(err)
				}
				if err := st.Set(ctx, "uma/t/ref", b); err != nil {
					t.Fatal(err)
				}
				s, err := st.Get(ctx, "uma/t/ref")
				if err != nil {
					t.Fatal(err)
				}
				got, err := oauth.DecodeGrant(s.Value)
				if err != nil {
					t.Fatal(err)
				}
				if fmt.Sprintf("%#v", got.Scopes) != fmt.Sprintf("%#v", want.Scopes) ||
					got.Kind != want.Kind || got.TokenURL != want.TokenURL || got.ClientID != want.ClientID ||
					got.Subject != want.Subject || got.SecretRef != want.SecretRef || got.ClientCertRef != want.ClientCertRef {
					t.Fatalf("round trip mismatch")
				}
			})
		}
	}
}

func TestCachedTokenRoundTripBackends(t *testing.T) {
	for name, st := range backends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			g := goodGrant(oauth.ClientCredentials)
			key := oauth.TokenKey("uma/t/ref", g)
			want := oauth.CachedToken{
				Token: oauth.Token{AccessToken: "tok-" + name, ExpiresAt: time.Date(2030, 1, 2, 3, 4, 5, 6, time.UTC)},
				User:  "u", AuthzID: "z",
			}
			b, err := oauth.EncodeCachedToken(want)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Set(ctx, key, b); err != nil {
				t.Fatal(err)
			}
			s, err := st.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			got, err := oauth.DecodeCachedToken(s.Value)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("mismatch")
			}
		})
	}
}

func TestCachedTokenExactShape(t *testing.T) {
	b, err := oauth.EncodeCachedToken(oauth.CachedToken{Token: oauth.Token{
		AccessToken: "a", ExpiresAt: time.Date(2030, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"v":1,"kind":"oauth2","access_token":"a","expires_at":"2030-01-02T02:04:05Z"}`
	if string(b) != want {
		t.Fatalf("got %s", b)
	}
}

func TestDigestVectorAndStability(t *testing.T) {
	g := oauth.Grant{TokenURL: "https://t.example/token", ClientID: "c", Subject: "s", Scopes: []string{"b", "a"}}
	// first 16 hex of sha256 over
	// {"client_cert_ref":"","client_id":"c","client_secret_ref":"","kind":"","scopes":["a","b"],"secret_ref":"","subject":"s","token_url":"https://t.example/token"}
	// (computed independently with python hashlib).
	if got := oauth.Digest(g); got != "dcbf89c850c72f22" {
		t.Fatalf("digest = %q, want dcbf89c850c72f22", got)
	}
	h := g
	h.Scopes = []string{"a", "b"}
	if oauth.Digest(g) != oauth.Digest(h) {
		t.Fatal("scope order must not matter")
	}
	for _, mut := range []func(*oauth.Grant){
		func(x *oauth.Grant) { x.ClientID = "d" },
		func(x *oauth.Grant) { x.Subject = "t" },
		func(x *oauth.Grant) { x.TokenURL += "/v2" },
		func(x *oauth.Grant) { x.Scopes = []string{"a"} },
		func(x *oauth.Grant) { x.Kind = oauth.ClientCredentials },
		func(x *oauth.Grant) { x.SecretRef = "uma/t/secret" },
		func(x *oauth.Grant) { x.ClientCertRef = "uma/t/cert" },
		func(x *oauth.Grant) { x.ClientSecretRef = "uma/t/client-secret" },
	} {
		m := g
		mut(&m)
		if oauth.Digest(m) == oauth.Digest(g) {
			t.Fatal("digest must change")
		}
	}
	if k := oauth.TokenKey("r/x", g); k != "r/x.token."+oauth.Digest(g) {
		t.Fatal(k)
	}
	// nil scopes canonicalise as [] (not null)
	if oauth.Digest(oauth.Grant{}) != oauth.Digest(oauth.Grant{Scopes: []string{}}) {
		t.Fatal("nil vs empty scopes")
	}
}

func TestGrantValidation(t *testing.T) {
	cases := []struct {
		name, field string
		mut         func(*oauth.Grant)
		kind        oauth.GrantKind
	}{
		{"bad kind", "kind", func(g *oauth.Grant) { g.Kind = "x" }, oauth.ClientCredentials},
		{"no url", "token_url", func(g *oauth.Grant) { g.TokenURL = "" }, oauth.ClientCredentials},
		{"http remote", "token_url", func(g *oauth.Grant) { g.TokenURL = "http://idp.example/t" }, oauth.ClientCredentials},
		{"relative", "token_url", func(g *oauth.Grant) { g.TokenURL = "/t" }, oauth.ClientCredentials},
		{"userinfo", "token_url", func(g *oauth.Grant) { g.TokenURL = "https://u:p@idp.example/t" }, oauth.ClientCredentials},
		{"no client id", "client_id", func(g *oauth.Grant) { g.ClientID = "" }, oauth.RefreshToken},
		{"empty scope", "scopes", func(g *oauth.Grant) { g.Scopes = []string{""} }, oauth.ClientCredentials},
		{"space scope", "scopes", func(g *oauth.Grant) { g.Scopes = []string{"a b"} }, oauth.ClientCredentials},
		{"dup scope", "scopes", func(g *oauth.Grant) { g.Scopes = []string{"a", "a"} }, oauth.ClientCredentials},
		{"no secret", "secret_ref", func(g *oauth.Grant) { g.SecretRef = "" }, oauth.JWTBearer},
		{"assertion with secret", "secret_ref", func(g *oauth.Grant) { g.SecretRef = "x" }, oauth.ClientAssertion},
		{"assertion no cert", "client_cert_ref", func(g *oauth.Grant) { g.ClientCertRef = "" }, oauth.ClientAssertion},
		{"secret kind with cert", "client_cert_ref", func(g *oauth.Grant) { g.ClientCertRef = "x" }, oauth.RefreshToken},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := goodGrant(c.kind)
			c.mut(&g)
			err := g.Validate()
			var ve *oauth.ValidationError
			if !errors.As(err, &ve) || ve.Field != c.field {
				t.Fatalf("want field %s, got %v", c.field, err)
			}
			if _, err := oauth.EncodeGrant(g); err == nil {
				t.Fatal("encode must validate")
			}
		})
	}
	g := goodGrant(oauth.JWTBearer)
	g.ClientID = ""
	if err := g.Validate(); err != nil {
		t.Fatalf("jwt_bearer without client_id: %v", err)
	}
	g = goodGrant(oauth.ClientCredentials)
	g.TokenURL = "http://127.0.0.1:9/t"
	if err := g.Validate(); err != nil {
		t.Fatalf("loopback http: %v", err)
	}
}

func TestClientSecretRef(t *testing.T) {
	// Confidential refresh clients carry a client secret beside the refresh token.
	g := goodGrant(oauth.RefreshToken)
	g.ClientSecretRef = "uma/t/client-secret"
	if err := g.Validate(); err != nil {
		t.Fatalf("refresh_token with client_secret_ref: %v", err)
	}
	b, err := oauth.EncodeGrant(g)
	if err != nil {
		t.Fatal(err)
	}
	got, err := oauth.DecodeGrant(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientSecretRef != g.ClientSecretRef {
		t.Fatal("client_secret_ref lost in round trip")
	}
	plain := goodGrant(oauth.RefreshToken)
	if oauth.Digest(plain) == oauth.Digest(g) {
		t.Fatal("client_secret_ref must change the cache digest")
	}
	for _, k := range []oauth.GrantKind{oauth.ClientCredentials, oauth.ClientAssertion, oauth.JWTBearer} {
		h := goodGrant(k)
		h.ClientSecretRef = "x"
		var ve *oauth.ValidationError
		if err := h.Validate(); !errors.As(err, &ve) || ve.Field != "client_secret_ref" {
			t.Fatalf("%s: want client_secret_ref error, got %v", k, err)
		}
	}
	g.ClientSecretRef = "bad\x00ref"
	var ve *oauth.ValidationError
	if err := g.Validate(); !errors.As(err, &ve) || ve.Field != "client_secret_ref" {
		t.Fatalf("control chars: %v", err)
	}
}

func TestDecodeGrantWrongTypedScopesElement(t *testing.T) {
	for _, d := range []string{
		`{"v":1,"kind":"client_credentials","token_url":"https://x.example/t","client_id":"c","secret_ref":"r","scopes":["a",1]}`,
		`{"v":1,"kind":"client_credentials","token_url":"https://x.example/t","client_id":"c","secret_ref":"r","scopes":[null,{}]}`,
		`{"v":1,"kind":"client_credentials","token_url":"https://x.example/t","client_id":"c","secret_ref":"r","scopes":"a"}`,
	} {
		_, err := oauth.DecodeGrant([]byte(d))
		var ve *oauth.ValidationError
		if !errors.As(err, &ve) || ve.Field != "scopes" {
			t.Errorf("doc %s: err = %v, want field scopes", d, err)
		}
	}
}

func TestDecodeErrorsNeverEchoValues(t *testing.T) {
	const leak = "LEAKME-zzz"
	docs := []string{
		`{"v":1,"kind":"client_credentials","token_url":"https://x.example/t","client_id":"c","secret_ref":"r","` + leak + `":1}`,
		`{"v":1,"kind":"` + leak + `","token_url":"https://x.example/t"}`,
		`{"v":1,"kind":"client_credentials","token_url":"` + leak + `","client_id":"c","secret_ref":"r"}`,
		`{"v":1,"kind":"client_credentials","token_url":"https://x.example/t","client_id":5,"secret_ref":"` + leak + `"}`,
		`{"v":2,"kind":"client_credentials"}`,
		`{"kind":"client_credentials"}`,
		`not json ` + leak,
		`null`,
	}
	for _, d := range docs {
		_, err := oauth.DecodeGrant([]byte(d))
		if err == nil || strings.Contains(err.Error(), leak) {
			t.Fatalf("doc %s: err=%v", d, err)
		}
	}
	tdocs := []string{
		`{"v":1,"kind":"oauth2","access_token":"` + leak + `","expires_at":"2030-01-01T00:00:00Z","extra":1}`,
		`{"v":1,"kind":"oauth2","access_token":"` + leak + `","expires_at":"` + leak + `"}`,
		`{"v":1,"kind":"oauth2","access_token":"` + leak + `"}`,
		`{"v":1,"kind":"secret","access_token":"` + leak + `","expires_at":"2030-01-01T00:00:00Z"}`,
		`{"v":1,"kind":"oauth2","access_token":7,"expires_at":"2030-01-01T00:00:00Z"}`,
		`{"v":3,"kind":"oauth2","access_token":"` + leak + `","expires_at":"2030-01-01T00:00:00Z"}`,
		`{"v":1,"kind":"oauth2","access_token":"","expires_at":"2030-01-01T00:00:00Z"}`,
	}
	for _, d := range tdocs {
		_, err := oauth.DecodeCachedToken([]byte(d))
		if err == nil || strings.Contains(err.Error(), leak) {
			t.Fatalf("doc %s: err=%v", d, err)
		}
	}
}

func TestRedaction(t *testing.T) {
	const tok = "TOKEN-SECRET-VALUE"
	c := oauth.CachedToken{Token: oauth.Token{AccessToken: tok, ExpiresAt: time.Now()}, User: "USERVAL"}
	g := goodGrant(oauth.ClientCredentials)
	g.ClientID = "CLIENTVAL"
	for _, v := range []any{c, &c, c.Token, &c.Token, g, &g} {
		outs := []string{
			fmt.Sprintf("%v", v), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v),
			fmt.Sprintf("%s", v), fmt.Sprintf("%q", v), fmt.Sprintf("%d", v),
		}
		j, _ := json.Marshal(v)
		outs = append(outs, string(j))
		var sb strings.Builder
		slog.New(slog.NewTextHandler(&sb, nil)).Info("x", "v", v)
		outs = append(outs, sb.String())
		for _, o := range outs {
			for _, bad := range []string{tok, "USERVAL", "CLIENTVAL", "uma/t/secret", "idp.example"} {
				if strings.Contains(o, bad) {
					t.Fatalf("%T leaked %q in %q", v, bad, o)
				}
			}
		}
	}
}

type writableStore struct{ *memory.Store }

func (writableStore) Writable() bool { return true }

func TestWritability(t *testing.T) {
	if oauth.IsWritable(memory.New()) {
		t.Fatal("memory must not be writable")
	}
	err := oauth.RequireWritable(memory.New(), "memory")
	var nw *oauth.NotWritableError
	if !errors.Is(err, oauth.ErrNotWritable) || !errors.As(err, &nw) || nw.Backend != "memory" {
		t.Fatalf("got %v", err)
	}
	if err := oauth.RequireWritable(writableStore{memory.New()}, "x"); err != nil {
		t.Fatal(err)
	}
	if !oauth.IsWritable(file.New(t.TempDir(), nil)) {
		t.Fatal("file must declare writable")
	}
}

func TestTokenExpired(t *testing.T) {
	now := time.Now()
	tk := oauth.Token{AccessToken: "a", ExpiresAt: now.Add(time.Minute)}
	if tk.Expired(now, 30*time.Second) {
		t.Fatal("not yet")
	}
	if !tk.Expired(now, 2*time.Minute) {
		t.Fatal("within leeway")
	}
}

// Compile-time: backends that declare write-back.
var (
	_ oauth.Writability = (*file.Store)(nil)
	_ oauth.Writability = (*keyring.Store)(nil)
	_ oauth.Writability = (*openbao.Store)(nil)
)
