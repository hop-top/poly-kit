package oauth

import (
	"strings"
	"testing"
)

func TestEncodeRefreshSecret(t *testing.T) {
	rt := "rt-" + strings.Repeat("x", 8)
	b, err := EncodeRefreshSecret(rt)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"v":1,"kind":"secret","value":"`+rt+`"}`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if _, err := EncodeRefreshSecret(""); err == nil {
		t.Fatal("empty refresh token accepted")
	}
}

func TestNewRefreshGrant(t *testing.T) {
	g, err := NewRefreshGrant("https://as.example/token", "cid", "mail/ref", "mail/cs", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Kind != RefreshToken || g.SecretRef != "mail/ref" || g.ClientSecretRef != "mail/cs" {
		t.Fatalf("unexpected grant: %v", g.Kind)
	}
	if _, err := NewRefreshGrant("https://as.example/token", "", "r", "", nil); err == nil {
		t.Fatal("missing client_id accepted")
	}
	if _, err := NewRefreshGrant("http://as.example/token", "cid", "r", "", nil); err == nil {
		t.Fatal("plain http non-loopback accepted")
	}
}
