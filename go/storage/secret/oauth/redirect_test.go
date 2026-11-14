package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestTokenRequestsNeverFollowRedirects: every grant refuses a 3xx from the
// token endpoint with a plain error; the redirect target (which would get
// the replayed POST body, credentials included) is never contacted.
func TestTokenRequestsNeverFollowRedirects(t *testing.T) {
	_, jbPEM := jbKey(t)
	cert := caNewCert(t)
	caKey := caKeyPEM(t, cert.key, true)

	fetchers := map[string]func(tokenURL string, hc *http.Client) error{
		"client_credentials": func(u string, hc *http.Client) error {
			g := ccGrant(u)
			_, err := fetchClientCredentials(context.Background(), ccStore(t, `{"v":1,"kind":"secret","value":"x"}`), "r", g, hc)
			return err
		},
		"client_assertion": func(u string, hc *http.Client) error {
			g := Grant{Kind: ClientAssertion, TokenURL: u, ClientID: "c", ClientCertRef: "uma/t/cert"}
			_, err := fetchClientAssertion(context.Background(), caStore(t, cert.certPM, caKey), "r", g, hc)
			return err
		},
		"jwt_bearer": func(u string, hc *http.Client) error {
			st := jbStore(t, map[string]string{"client_email": "sa@e.test", "private_key": jbPEM})
			_, err := fetchJWTBearer(context.Background(), st, jbRef, jbGrant(u), hc)
			return err
		},
		"refresh_token": func(u string, hc *http.Client) error {
			_, err := fetchRefreshToken(context.Background(), rtNewStore(t), "r", rtGrant(u), hc)
			return err
		},
	}
	for name, fetch := range fetchers {
		for _, code := range []int{302, 307, 308} {
			t.Run(name, func(t *testing.T) {
				var hits atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					_, _ = w.Write([]byte(`{"access_token":"stolen","expires_in":60}`))
				}))
				defer target.Close()
				first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, target.URL, code)
				}))
				defer first.Close()
				// A caller-supplied client that follows redirects must still
				// be neutralized, and must not be mutated.
				hc := first.Client()
				if err := fetch(first.URL, hc); err == nil || isSentinel(err) {
					t.Fatalf("%d: err = %v, want plain error", code, err)
				}
				if hits.Load() != 0 {
					t.Fatalf("%d: redirect target contacted", code)
				}
				if hc.CheckRedirect != nil {
					t.Fatal("caller client mutated")
				}
			})
		}
	}
}
