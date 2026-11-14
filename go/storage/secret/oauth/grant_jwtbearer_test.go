package oauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret/memory"
)

const jbRef = "uma/t/sa"

func jbKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return k, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func jbStore(t *testing.T, saJSON map[string]string) *memory.Store {
	t.Helper()
	sa, _ := json.Marshal(saJSON)
	env, _ := json.Marshal(map[string]any{"v": 1, "kind": "secret", "value": string(sa)})
	st := memory.New()
	if err := st.Set(context.Background(), jbRef, env); err != nil {
		t.Fatal(err)
	}
	return st
}

type jbClaims struct {
	Iss   string `json:"iss"`
	Sub   string `json:"sub"`
	Scope string `json:"scope"`
	Aud   string `json:"aud"`
	Exp   int64  `json:"exp"`
}

func jbDecode(t *testing.T, assertion string, pub *rsa.PublicKey) (hdr map[string]any, c jbClaims) {
	t.Helper()
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		t.Fatalf("assertion parts = %d", len(parts))
	}
	dec := func(s string) []byte {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if err := json.Unmarshal(dec(parts[0]), &hdr); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(dec(parts[1]), &c); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], dec(parts[2])); err != nil {
		t.Fatalf("assertion signature: %v", err)
	}
	return hdr, c
}

func jbGrant(url string) Grant {
	return Grant{
		Kind: JWTBearer, TokenURL: url, SecretRef: jbRef,
		Scopes: []string{"https://scope.test/a", "https://scope.test/b"}, Subject: "user@example.test",
	}
}

func TestFetchJWTBearer(t *testing.T) {
	key, pemKey := jbKey(t)
	var gotForm map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fake-access","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	st := jbStore(t, map[string]string{
		"client_email":   "sa@proj.iam.example.test",
		"private_key":    pemKey,
		"private_key_id": "kid-1",
		// ignored: grant token_url wins
		"token_uri": "https://other.example.test/token",
	})
	before := time.Now()
	ct, err := fetchJWTBearer(context.Background(), st, jbRef, jbGrant(srv.URL), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if ct.AccessToken != "fake-access" || !ct.ExpiresAt.After(before.Add(59*time.Minute)) {
		t.Fatalf("token = %+v", ct.Token)
	}
	if err := ct.Validate(); err != nil {
		t.Fatal(err)
	}
	if g := gotForm["grant_type"]; len(g) != 1 || g[0] != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Fatalf("grant_type = %v", g)
	}
	hdr, c := jbDecode(t, gotForm["assertion"][0], &key.PublicKey)
	if hdr["alg"] != "RS256" || hdr["kid"] != "kid-1" {
		t.Fatalf("header = %v", hdr)
	}
	if c.Iss != "sa@proj.iam.example.test" || c.Sub != "user@example.test" ||
		c.Scope != "https://scope.test/a https://scope.test/b" || c.Aud != srv.URL {
		t.Fatalf("claims = %+v", c)
	}
}

func TestFetchJWTBearerNoSubject(t *testing.T) {
	key, pemKey := jbKey(t)
	var assertion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		assertion = r.PostForm.Get("assertion")
		_, _ = w.Write([]byte(`{"access_token":"x","expires_in":60}`))
	}))
	defer srv.Close()
	st := jbStore(t, map[string]string{"client_email": "sa@e.test", "private_key": pemKey})
	g := jbGrant(srv.URL)
	g.Subject = ""
	if _, err := fetchJWTBearer(context.Background(), st, jbRef, g, srv.Client()); err != nil {
		t.Fatal(err)
	}
	if _, c := jbDecode(t, assertion, &key.PublicKey); c.Sub != "" {
		t.Fatalf("sub = %q", c.Sub)
	}
}

func TestJWTBearerCacheKeyCoversSubjectAndScopes(t *testing.T) {
	base := jbGrant("https://idp.example/token")
	subj := base
	subj.Subject = "other@example.test"
	scp := base
	scp.Scopes = []string{"https://scope.test/a"}
	keys := map[string]bool{}
	for _, g := range []Grant{base, subj, scp} {
		keys[Digest(g)] = true
		keys[TokenKey(jbRef, g)] = true
	}
	if len(keys) != 6 {
		t.Fatalf("distinct keys = %d, want 6", len(keys))
	}
}

func TestFetchJWTBearerErrors(t *testing.T) {
	_, pemKey := jbKey(t)
	const secretMark = "SECRETMARK"
	good := map[string]string{"client_email": "sa@e.test", "private_key": pemKey}
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"invalid_grant", 400, `{"error":"invalid_grant","error_description":"` + secretMark + `"}`, ErrCredentialRejected},
		{"invalid_client", 401, `{"error":"invalid_client"}`, ErrCredentialRejected},
		{"unauthorized_client", 400, `{"error":"unauthorized_client"}`, ErrCredentialRejected},
		{"other 4xx", 400, `{"error":"invalid_scope"}`, ErrCredentialRejected},
		{"non-json 4xx", 403, secretMark, ErrCredentialRejected},
		{"500", 500, secretMark, ErrTransient},
		{"503", 503, `{"error":"invalid_grant"}`, ErrTransient},
		{"429", 429, secretMark, ErrTransient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := fetchJWTBearer(context.Background(), jbStore(t, good), jbRef, jbGrant(srv.URL), srv.Client())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			for _, bad := range []string{secretMark, pemKey, "PRIVATE KEY"} {
				if strings.Contains(err.Error(), bad) {
					t.Fatalf("error leaks %q: %v", bad, err)
				}
			}
		})
	}
}

func TestFetchJWTBearerNetworkTransient(t *testing.T) {
	_, pemKey := jbKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	st := jbStore(t, map[string]string{"client_email": "sa@e.test", "private_key": pemKey})
	_, err := fetchJWTBearer(context.Background(), st, jbRef, jbGrant(url), nil)
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Fatal("leak")
	}
}

func TestFetchJWTBearerBadKeyMaterial(t *testing.T) {
	_, pemKey := jbKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("endpoint must not be called")
	}))
	defer srv.Close()
	g := jbGrant(srv.URL)
	put := func(v string) *memory.Store {
		st := memory.New()
		_ = st.Set(context.Background(), jbRef, []byte(v))
		return st
	}
	sa := func(m map[string]string) string {
		b, _ := json.Marshal(m)
		e, _ := json.Marshal(map[string]any{"v": 1, "kind": "secret", "value": string(b)})
		return string(e)
	}
	cases := map[string]*memory.Store{
		"missing":          memory.New(),
		"not envelope":     put(`{"v":1,"kind":"client_cert"}`),
		"not sa json":      put(`{"v":1,"kind":"secret","value":"nope"}`),
		"no email":         put(sa(map[string]string{"private_key": pemKey})),
		"no key":           put(sa(map[string]string{"client_email": "a@b.test"})),
		"bad pem":          put(sa(map[string]string{"client_email": "a@b.test", "private_key": "not pem"})),
		"wrong key type":   put(sa(map[string]string{"client_email": "a@b.test", "private_key": "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"})),
		"base64 bad value": put(`{"v":1,"kind":"secret","value_base64":"!!"}`),
	}
	for name, st := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := fetchJWTBearer(context.Background(), st, jbRef, g, srv.Client())
			if err == nil {
				t.Fatal("want error")
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if name == "missing" && ve.Field != "secret_ref" {
				t.Fatalf("field = %q, want secret_ref", ve.Field)
			}
		})
	}
}

func TestFetchJWTBearerBase64Value(t *testing.T) {
	_, pemKey := jbKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"x","expires_in":60}`))
	}))
	defer srv.Close()
	sa, _ := json.Marshal(map[string]string{"client_email": "a@b.test", "private_key": pemKey})
	env, _ := json.Marshal(map[string]any{"v": 1, "kind": "secret", "value_base64": base64.StdEncoding.EncodeToString(sa)})
	st := memory.New()
	_ = st.Set(context.Background(), jbRef, env)
	if _, err := fetchJWTBearer(context.Background(), st, jbRef, jbGrant(srv.URL), srv.Client()); err != nil {
		t.Fatal(err)
	}
}
