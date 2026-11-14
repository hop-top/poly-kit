package oauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret/memory"
)

type caCert struct {
	key    *rsa.PrivateKey
	der    []byte
	certPM string
}

func caNewCert(t *testing.T) caCert {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "kit-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return caCert{key: key, der: der,
		certPM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

func caKeyPEM(t *testing.T, key any, pkcs8 bool) string {
	t.Helper()
	if !pkcs8 {
		return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key.(*rsa.PrivateKey))}))
	}
	b, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: b}))
}

func caStore(t *testing.T, certPEM, keyPEM string) *memory.Store {
	t.Helper()
	st := memory.New()
	b, _ := json.Marshal(map[string]any{"v": 1, "kind": "client_cert",
		"certificate_pem": certPEM, "private_key_pem": keyPEM})
	if err := st.Set(context.Background(), "uma/t/cert", b); err != nil {
		t.Fatal(err)
	}
	return st
}

type caServer struct {
	*httptest.Server
	forms []url.Values
	reply func(w http.ResponseWriter)
}

func caNewServer(t *testing.T) *caServer {
	s := &caServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.forms = append(s.forms, r.PostForm)
		if u, p, ok := r.BasicAuth(); ok || u != "" || p != "" {
			t.Errorf("basic auth sent")
		}
		if s.reply != nil {
			s.reply(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-1","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(s.Close)
	return s
}

func caGrant(s *caServer) Grant {
	return Grant{Kind: ClientAssertion, TokenURL: s.URL + "/tok", ClientID: "client-1",
		Scopes: []string{"https://graph.example/.default"}, ClientCertRef: "uma/t/cert"}
}

func caPart(t *testing.T, s string) map[string]any {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestFetchClientAssertion(t *testing.T) {
	for _, pkcs8 := range []bool{false, true} {
		c := caNewCert(t)
		st := caStore(t, c.certPM, caKeyPEM(t, c.key, pkcs8))
		s := caNewServer(t)
		g := caGrant(s)
		before := time.Now()
		var jtis []string
		for i := 0; i < 2; i++ {
			tok, err := fetchClientAssertion(context.Background(), st, "uma/t/cert", g, s.Client())
			if err != nil {
				t.Fatal(err)
			}
			if tok.AccessToken != "at-1" || tok.ExpiresAt.Before(before.Add(50*time.Minute)) {
				t.Fatalf("token %+v", tok)
			}
			f := s.forms[i]
			if f.Get("grant_type") != "client_credentials" ||
				f.Get("client_id") != "client-1" ||
				f.Get("scope") != "https://graph.example/.default" ||
				f.Get("client_assertion_type") != "urn:ietf:params:oauth:client-assertion-type:jwt-bearer" {
				t.Fatalf("form %v", f)
			}
			if f.Has("client_secret") {
				t.Fatal("client_secret sent")
			}
			parts := strings.Split(f.Get("client_assertion"), ".")
			if len(parts) != 3 {
				t.Fatal("not a JWT")
			}
			h := caPart(t, parts[0])
			sum := sha1.Sum(c.der)
			if h["alg"] != "RS256" || h["typ"] != "JWT" ||
				h["x5t"] != base64.RawURLEncoding.EncodeToString(sum[:]) {
				t.Fatalf("header %v", h)
			}
			cl := caPart(t, parts[1])
			if cl["aud"] != g.TokenURL || cl["iss"] != "client-1" || cl["sub"] != "client-1" {
				t.Fatalf("claims %v", cl)
			}
			iat, nbf, exp := cl["iat"].(float64), cl["nbf"].(float64), cl["exp"].(float64)
			if iat != nbf || exp <= iat || exp-iat > 600 ||
				time.Unix(int64(iat), 0).Before(before.Add(-2*time.Second)) {
				t.Fatalf("times %v", cl)
			}
			jtis = append(jtis, cl["jti"].(string))
			if jtis[i] == "" {
				t.Fatal("empty jti")
			}
			sig, err := base64.RawURLEncoding.DecodeString(parts[2])
			if err != nil {
				t.Fatal(err)
			}
			d := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			if err := rsa.VerifyPKCS1v15(&c.key.PublicKey, crypto.SHA256, d[:], sig); err != nil {
				t.Fatalf("signature: %v", err)
			}
		}
		if jtis[0] == jtis[1] {
			t.Fatal("jti reused")
		}
	}
}

func TestFetchClientAssertionBadCert(t *testing.T) {
	c := caNewCert(t)
	ec := caNewECKey(t)
	cases := map[string]struct {
		st    *memory.Store
		field string
	}{
		"ec key":    {caStore(t, c.certPM, ec), "private_key_pem"},
		"bad key":   {caStore(t, c.certPM, "junk"), "private_key_pem"},
		"bad cert":  {caStore(t, "junk", caKeyPEM(t, c.key, true)), "certificate_pem"},
		"no cert":   {caStore(t, "", caKeyPEM(t, c.key, true)), "certificate_pem"},
		"wrong key": {caStore(t, c.certPM, caKeyPEM(t, caNewCert(t).key, true)), "private_key_pem"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := caNewServer(t)
			_, err := fetchClientAssertion(context.Background(), tc.st, "uma/t/cert", caGrant(s), s.Client())
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != tc.field {
				t.Fatalf("err %v", err)
			}
			if len(s.forms) != 0 {
				t.Fatal("request sent")
			}
		})
	}
	s := caNewServer(t)
	_, err := fetchClientAssertion(context.Background(), memory.New(), "uma/t/cert", caGrant(s), s.Client())
	if err == nil {
		t.Fatal("missing envelope accepted")
	}
	bad := memory.New()
	_ = bad.Set(context.Background(), "uma/t/cert", []byte(`{"v":1,"kind":"secret","value":"x"}`))
	_, err = fetchClientAssertion(context.Background(), bad, "uma/t/cert", caGrant(s), s.Client())
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "kind" {
		t.Fatalf("err %v", err)
	}
}

func TestFetchClientAssertionErrors(t *testing.T) {
	c := caNewCert(t)
	keyPEM := caKeyPEM(t, c.key, true)
	st := caStore(t, c.certPM, keyPEM)
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"invalid_client", 401, `{"error":"invalid_client","error_description":"SECRETDESC"}`, ErrCredentialRejected},
		{"unauthorized_client", 400, `{"error":"unauthorized_client"}`, ErrCredentialRejected},
		{"invalid_grant", 400, `{"error":"invalid_grant"}`, ErrCredentialRejected},
		{"invalid_scope", 400, `{"error":"invalid_scope"}`, ErrCredentialRejected},
		{"500", 500, `oops SECRETDESC`, ErrTransient},
		{"503", 503, `{"error":"temporarily_unavailable"}`, ErrTransient},
		{"429", 429, `{"error":"slow_down"}`, ErrTransient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := caNewServer(t)
			s.reply = func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}
			_, err := fetchClientAssertion(context.Background(), st, "uma/t/cert", caGrant(s), s.Client())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err %v", err)
			}
			if errors.Is(err, ErrNeedsReconsent) {
				t.Fatal("reconsent")
			}
			caNoLeak(t, err, s.forms, keyPEM, "SECRETDESC")
		})
	}
	t.Run("network", func(t *testing.T) {
		s := caNewServer(t)
		g := caGrant(s)
		s.Close()
		_, err := fetchClientAssertion(context.Background(), st, "uma/t/cert", g, s.Client())
		if !errors.Is(err, ErrTransient) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("no expiry", func(t *testing.T) {
		s := caNewServer(t)
		s.reply = func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"at-1","token_type":"Bearer"}`))
		}
		_, err := fetchClientAssertion(context.Background(), st, "uma/t/cert", caGrant(s), s.Client())
		if err == nil || isSentinel(err) {
			t.Fatalf("err = %v, want plain error", err)
		}
	})
}

func caNoLeak(t *testing.T, err error, forms []url.Values, secrets ...string) {
	t.Helper()
	msg := err.Error()
	for _, f := range forms {
		secrets = append(secrets, f.Get("client_assertion"))
	}
	for _, s := range secrets {
		if s != "" && strings.Contains(msg, s) {
			t.Fatalf("error leaks credential: %v", err)
		}
	}
}

func caNewECKey(t *testing.T) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return caKeyPEM(t, k, true)
}
