package cmdsurface_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret/oauth"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

func TestMountOAuthPKCE_UnauthenticatedCallbackNotFatal(t *testing.T) {
	f := newFakeAS(t)
	store := cmdsurface.NewInMemoryPKCEStateStore()
	router := api.NewRouter()
	srv := httptest.NewServer(router)
	defer srv.Close()
	p := f.provider(nil)
	p.Config.RedirectURL = srv.URL + "/oauth/mail/callback"
	if err := cmdsurface.MountOAuthPKCE(router, []cmdsurface.PKCEProvider{p}, store); err != nil {
		t.Fatal(err)
	}
	state, err := store.Issue(context.Background(), "mail", "v", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"?error=access_denied", "?error=access_denied&state=bogus", "?iss=https://evil.example"} {
		resp, err := http.Get(srv.URL + "/oauth/mail/callback" + q)
		if err != nil {
			t.Fatal(err)
		}
		body := new(strings.Builder)
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		body.Write(buf[:n])
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || strings.Contains(body.String(), "access_denied") {
			t.Errorf("%s status=%d body=%q", q, resp.StatusCode, body)
		}
	}
	// Valid state was not consumed by the forged requests.
	if _, err := store.Consume(context.Background(), "mail", state, ""); err != nil {
		t.Fatalf("valid state consumed by forged callbacks: %v", err)
	}
}

// bindingRig mounts MountOAuthPKCE and returns the callback URL a browser
// would be sent to, plus the cookies set by /authorize.
type bindingRig struct {
	f       *fakeAS
	srv     *httptest.Server
	cb      string
	cookies []*http.Cookie
	results int
}

func newBindingRig(t *testing.T) *bindingRig {
	t.Helper()
	b := &bindingRig{f: newFakeAS(t)}
	p := b.f.provider(func(context.Context, cmdsurface.PKCEResult) error { b.results++; return nil })
	router := api.NewRouter()
	b.srv = httptest.NewServer(router)
	t.Cleanup(b.srv.Close)
	p.Config.RedirectURL = b.srv.URL + "/oauth/mail/callback"
	if err := cmdsurface.MountOAuthPKCE(router, []cmdsurface.PKCEProvider{p}, cmdsurface.NewInMemoryPKCEStateStore()); err != nil {
		t.Fatal(err)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(b.srv.URL + "/oauth/mail/authorize")
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize: %v %v", err, resp)
	}
	resp.Body.Close()
	b.cookies = resp.Cookies()
	resp, err = noFollow.Get(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("as: %v", err)
	}
	b.cb = resp.Header.Get("Location")
	resp.Body.Close()
	return b
}

func (b *bindingRig) callback(t *testing.T, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, b.cb, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestPKCE_AuthorizeSetsBindingCookie(t *testing.T) {
	b := newBindingRig(t)
	if len(b.cookies) != 1 {
		t.Fatalf("cookies = %v", b.cookies)
	}
	c := b.cookies[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 600 || c.Value == "" {
		t.Fatalf("cookie attrs: %+v", c)
	}
	if c.Secure || strings.HasPrefix(c.Name, "__Host-") {
		t.Fatalf("plain http must not use Secure/__Host-: %+v", c)
	}
	if strings.Contains(b.cb, c.Value) {
		t.Fatal("binding nonce leaked into callback url")
	}
}

func TestPKCE_CallbackWithoutOrWrongCookieRejected(t *testing.T) {
	b := newBindingRig(t)
	if r := b.callback(t); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("no cookie: %d", r.StatusCode)
	}
	wrong := *b.cookies[0]
	wrong.Value = "attacker-nonce"
	if r := b.callback(t, &wrong); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong cookie: %d", r.StatusCode)
	}
	if b.f.tokenCalls != 0 || b.results != 0 {
		t.Fatalf("exchange ran: calls=%d results=%d", b.f.tokenCalls, b.results)
	}
	// State was not consumed: the genuine browser still completes.
	r := b.callback(t, b.cookies[0])
	if r.StatusCode != http.StatusOK || b.results != 1 {
		t.Fatalf("genuine: %d results=%d", r.StatusCode, b.results)
	}
	var cleared bool
	for _, c := range r.Cookies() {
		if c.Name == b.cookies[0].Name && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("binding cookie not cleared: %v", r.Cookies())
	}
}

func TestPKCE_TransientStatusMapping(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusRequestTimeout, http.StatusServiceUnavailable} {
		f := newFakeAS(t)
		f.tokenErr, f.tokenCode = "temporarily_unavailable", code
		_, status, _, err := runLoopback(t, f, 5*time.Second, true)
		if !errors.Is(err, oauth.ErrTransient) {
			t.Errorf("%d: err=%v want ErrTransient", code, err)
		}
		if status != http.StatusBadGateway {
			t.Errorf("%d: browser status=%d want 502", code, status)
		}
	}
	f := newFakeAS(t)
	f.tokenErr, f.tokenCode = "invalid_request", http.StatusBadRequest
	_, _, _, err := runLoopback(t, f, 5*time.Second, true)
	if errors.Is(err, oauth.ErrTransient) {
		t.Errorf("400 must not be transient: %v", err)
	}
}

func TestPKCE_TLSVerificationFailureNotTransient(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer ts.Close()
	f := newFakeAS(t)
	p := f.provider(nil)
	p.Config.Endpoint.TokenURL = ts.URL // default client does not trust the test CA
	_, err := cmdsurface.LoopbackPKCE(context.Background(), p, cmdsurface.LoopbackOptions{
		Timeout: 5 * time.Second,
		OnReady: func(u string) { go browse(t, u) },
	})
	if err == nil || errors.Is(err, oauth.ErrTransient) {
		t.Fatalf("err=%v want non-transient", err)
	}
}

func TestMountOAuthPKCE_FullStoreIs503(t *testing.T) {
	f := newFakeAS(t)
	router := api.NewRouter()
	srv := httptest.NewServer(router)
	defer srv.Close()
	p := f.provider(nil)
	p.Config.RedirectURL = srv.URL + "/oauth/mail/callback"
	if err := cmdsurface.MountOAuthPKCE(router, []cmdsurface.PKCEProvider{p}, fullStore{cmdsurface.NewInMemoryPKCEStateStore()}); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(srv.URL + "/oauth/mail/authorize")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

type fullStore struct {
	*cmdsurface.InMemoryPKCEStateStore
}

func (fullStore) Issue(context.Context, string, string, string, time.Duration) (string, error) {
	return "", cmdsurface.ErrStateStoreFull
}

// authorizeCookies mounts MountOAuthPKCE on srv's router and returns the
// cookies /authorize sets, requested through client.
func authorizeCookies(t *testing.T, srv *httptest.Server, router *api.Router, client *http.Client, secureCookie bool) []*http.Cookie {
	t.Helper()
	f := newFakeAS(t)
	p := f.provider(nil)
	p.SecureCookie = secureCookie
	p.Config.RedirectURL = srv.URL + "/oauth/mail/callback"
	if err := cmdsurface.MountOAuthPKCE(router, []cmdsurface.PKCEProvider{p}, cmdsurface.NewInMemoryPKCEStateStore()); err != nil {
		t.Fatal(err)
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(srv.URL + "/oauth/mail/authorize")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Cookies()
}

func checkSecureCookie(t *testing.T, cs []*http.Cookie) {
	t.Helper()
	if len(cs) != 1 {
		t.Fatalf("cookies = %v", cs)
	}
	c := cs[0]
	if !strings.HasPrefix(c.Name, "__Host-") || !c.Secure || !c.HttpOnly || c.Path != "/" ||
		c.Domain != "" || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 600 {
		t.Fatalf("cookie: %+v", c)
	}
}

func TestPKCE_BindingCookieSecureOverTLS(t *testing.T) {
	router := api.NewRouter()
	srv := httptest.NewTLSServer(router)
	defer srv.Close()
	checkSecureCookie(t, authorizeCookies(t, srv, router, srv.Client(), false))
}

func TestPKCE_BindingCookieSecureCookieOption(t *testing.T) {
	router := api.NewRouter()
	srv := httptest.NewServer(router) // plain http, as behind a TLS-terminating proxy
	defer srv.Close()
	checkSecureCookie(t, authorizeCookies(t, srv, router, srv.Client(), true))
}
