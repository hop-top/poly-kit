package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/memory"
)

// One scenario = one cassette directory = one token endpoint exchange.
// Provider URLs are the real ones; the transport never lets a request leave
// the process in replay.

const cassScope = "https://graph.microsoft.com/.default"

// cassStore is a writable in-memory store.
type cassStore struct{ *memory.Store }

func (cassStore) Writable() bool { return true }

func cassEnvelope(t testing.TB, st *memory.Store, key, value string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"v": 1, "kind": "secret", "value": value})
	if err := st.Set(context.Background(), key, b); err != nil {
		t.Fatal(err)
	}
}

func cassRSA(t testing.TB) (*rsa.PrivateKey, string) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	cassCollect("rsa-key-der", base64.StdEncoding.EncodeToString(der))
	return k, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// cassCertStore stores a fresh self-signed client cert and returns its key.
func cassCertStore(t testing.TB) (*memory.Store, *rsa.PrivateKey) {
	t.Helper()
	key, keyPEM := cassRSA(t)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "kit-cassette"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	b, _ := json.Marshal(map[string]any{"v": 1, "kind": "client_cert",
		"certificate_pem": certPEM, "private_key_pem": keyPEM})
	st := memory.New()
	if err := st.Set(context.Background(), "cert", b); err != nil {
		t.Fatal(err)
	}
	return st, key
}

func cassWithin(t *testing.T, tok CachedToken, want string) {
	t.Helper()
	if tok.AccessToken != want {
		t.Fatalf("access token = %q, want %q", tok.AccessToken, want)
	}
	if d := time.Until(tok.ExpiresAt); d < 50*time.Minute || d > 61*time.Minute {
		t.Fatalf("expiry in %v, want about 3599s", d)
	}
}

// cassNoLeakInErr fails when err carries any runtime sensitive value.
func cassNoLeakInErr(t *testing.T, err error) {
	t.Helper()
	for v, label := range cassSnapshot() {
		if strings.Contains(err.Error(), v) {
			t.Errorf("error string leaks %s", label)
		}
	}
}

// entraFixture generates the tenant, app and secret of one scenario.
type entraFixture struct {
	scrub cassScrub
	idp   *cassEntra
	grant Grant
}

func newEntraFixture(t *testing.T) *entraFixture {
	f := &entraFixture{}
	f.idp = &cassEntra{t: t}
	f.idp.tenant = f.scrub.know("tenant-id", cassUUID(t))
	f.idp.clientID = f.scrub.know("client-id", cassUUID(t))
	f.grant = Grant{Kind: ClientCredentials, TokenURL: f.idp.tokenURL(), ClientID: f.idp.clientID, Scopes: []string{cassScope}}
	return f
}

func TestCassetteEntraClientSecret(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(f *entraFixture, st *memory.Store)
		wantErr error
	}{
		{"entra_cc_secret_ok", func(*entraFixture, *memory.Store) {}, nil},
		{"entra_cc_secret_invalid_secret", func(f *entraFixture, st *memory.Store) {
			cassEnvelope(t, st, "sec", f.scrub.know("client-secret", cassRand(t, 8)+"Q~"+cassRand(t, 30)))
		}, ErrCredentialRejected},
		{"entra_cc_secret_unknown_app", func(f *entraFixture, _ *memory.Store) {
			f.grant.ClientID = f.scrub.know("client-id-unknown", cassUUID(t))
		}, ErrCredentialRejected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newEntraFixture(t)
			f.idp.secret = f.scrub.know("client-secret", cassRand(t, 8)+"Q~"+cassRand(t, 30))
			st := memory.New()
			cassEnvelope(t, st, "sec", f.idp.secret)
			f.grant.SecretRef = "sec"
			c.mutate(f, st)
			env := cassStart(t, c.name, &f.scrub, f.idp)
			tok, err := fetchClientCredentials(t.Context(), st, "ref", f.grant, env.Client)
			if c.wantErr == nil {
				if err != nil {
					t.Fatal(err)
				}
				cassWithin(t, tok, "scrubbed-access-token")
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			cassNoLeakInErr(t, err)
		})
	}
}

func TestCassetteEntraClientCertificate(t *testing.T) {
	cases := []struct {
		name    string
		wrong   bool // registered cert differs from the one the client holds
		wantErr error
	}{
		{"entra_cc_cert_ok", false, nil},
		{"entra_cc_cert_wrong_key", true, ErrCredentialRejected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newEntraFixture(t)
			st, key := cassCertStore(t)
			f.idp.pub = &key.PublicKey
			if c.wrong {
				_, other := cassCertStore(t)
				f.idp.pub = &other.PublicKey
			}
			f.grant = Grant{Kind: ClientAssertion, TokenURL: f.idp.tokenURL(), ClientID: f.idp.clientID,
				Scopes: []string{cassScope}, ClientCertRef: "cert"}
			env := cassStart(t, c.name, &f.scrub, f.idp)
			tok, err := fetchClientAssertion(t.Context(), st, "ref", f.grant, env.Client)
			if c.wantErr == nil {
				if err != nil {
					t.Fatal(err)
				}
				cassWithin(t, tok, "scrubbed-access-token")
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			cassNoLeakInErr(t, err)
		})
	}
}

func TestCassetteGoogleJWTBearer(t *testing.T) {
	cases := []struct {
		name    string
		wrong   bool // server trusts a different key than the one signing
		wantErr error
	}{
		{"google_jwt_ok", false, nil},
		{"google_jwt_invalid_signature", true, ErrCredentialRejected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var scrub cassScrub
			key, keyPEM := cassRSA(t)
			pub := &key.PublicKey
			if c.wrong {
				other, _ := cassRSA(t)
				pub = &other.PublicKey
			}
			email := scrub.know("sa-email", "svc-"+cassRand(t, 8)+"@proj-"+cassRand(t, 6)+".iam.gserviceaccount.test")
			sa, _ := json.Marshal(map[string]string{
				"type": "service_account", "client_email": email, "private_key": keyPEM,
				"private_key_id": scrub.know("sa-key-id", cassRand(t, 40)),
			})
			st := memory.New()
			cassEnvelope(t, st, "sa", string(sa))
			scope := "https://www.googleapis.com/auth/drive.readonly"
			idp := &cassGoogle{t: t, saEmail: email, saPub: pub, scope: scope, subject: "user@example.test"}
			g := Grant{Kind: JWTBearer, TokenURL: cassGoogleTokenURL, SecretRef: "sa",
				Scopes: []string{scope}, Subject: "user@example.test"}
			env := cassStart(t, c.name, &scrub, idp)
			tok, err := fetchJWTBearer(t.Context(), st, "ref", g, env.Client)
			if c.wantErr == nil {
				if err != nil {
					t.Fatal(err)
				}
				cassWithin(t, tok, "scrubbed-access-token")
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			cassNoLeakInErr(t, err)
		})
	}
}

func TestCassetteGoogleRefreshToken(t *testing.T) {
	cases := []struct {
		name    string
		rotate  bool
		revoked bool
		wantErr error
	}{
		{"google_refresh_ok", false, false, nil},
		{"google_refresh_rotated", true, false, nil},
		{"google_refresh_revoked", false, true, ErrNeedsReconsent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var scrub cassScrub
			rt := scrub.know("refresh-token-held", "1//"+cassRand(t, 48))
			idp := &cassGoogle{
				t: t, scope: "https://www.googleapis.com/auth/drive.readonly", refreshToken: rt,
				clientID:     scrub.know("client-id", cassRand(t, 12)+"-"+cassRand(t, 16)+".apps.googleusercontent.test"),
				clientSecret: scrub.know("client-secret", "gcs-"+cassRand(t, 24)),
			}
			if c.rotate {
				idp.rotateTo = scrub.know("refresh-token-new", "1//"+cassRand(t, 48))
			}
			if c.revoked {
				idp.refreshToken = "1//" + cassRand(t, 48) // server no longer knows rt
			}
			st := cassStore{memory.New()}
			cassEnvelope(t, st.Store, "rt", rt)
			cassEnvelope(t, st.Store, "cs", idp.clientSecret)
			g := Grant{Kind: RefreshToken, TokenURL: cassGoogleTokenURL, ClientID: idp.clientID,
				SecretRef: "rt", ClientSecretRef: "cs", Scopes: []string{idp.scope}}
			env := cassStart(t, c.name, &scrub, idp)
			tok, err := fetchRefreshToken(t.Context(), st, "ref", g, env.Client)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				cassNoLeakInErr(t, err)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cassWithin(t, tok, "scrubbed-access-token")
			got, err := st.Get(t.Context(), "rt")
			if err != nil {
				t.Fatal(err)
			}
			var env1 struct{ Value string }
			_ = json.Unmarshal(got.Value, &env1)
			want := rt
			if c.rotate {
				want = "scrubbed-refresh-token"
			}
			if env1.Value != want {
				t.Fatalf("stored refresh token rotated=%v, want rotated=%v", env1.Value != rt, c.rotate)
			}
		})
	}
}

var _ secret.Store = cassStore{}
