package cmdsurface_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"hop.top/kit/go/storage/secret/oauth"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// fakeAS is an in-process authorization server: /authorize bounces back to
// redirect_uri; /token enforces PKCE.
type fakeAS struct {
	srv *httptest.Server

	mu         sync.Mutex
	challenge  string
	method     string
	deny       bool
	wrongState bool
	noISS      bool
	badISS     bool
	tokenForm  url.Values
	tokenCalls int
	tokenErr   string // oauth error code to answer with
	tokenCode  int    // status for tokenErr (default 400)
	verifierOK bool
}

func newFakeAS(t *testing.T) *fakeAS {
	f := &fakeAS{}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.mu.Lock()
		f.challenge, f.method = q.Get("code_challenge"), q.Get("code_challenge_method")
		deny, wrong, noISS, badISS := f.deny, f.wrongState, f.noISS, f.badISS
		f.mu.Unlock()
		out := url.Values{}
		state := q.Get("state")
		if wrong {
			state = "forged"
		}
		out.Set("state", state)
		if deny {
			out.Set("error", "access_denied")
		} else {
			out.Set("code", "code-1")
		}
		switch {
		case badISS:
			out.Set("iss", "https://evil.example")
		case !noISS:
			out.Set("iss", f.srv.URL)
		}
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+out.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		f.tokenCalls++
		f.tokenForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		if f.tokenErr != "" {
			code := f.tokenCode
			if code == 0 {
				code = http.StatusBadRequest
			}
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":"` + f.tokenErr + `"}`))
			return
		}
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		f.verifierOK = f.method == "S256" && base64.RawURLEncoding.EncodeToString(sum[:]) == f.challenge
		if !f.verifierOK {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"at-1","refresh_token":"rt-1","token_type":"Bearer","expires_in":3600}`))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAS) provider(onResult func(context.Context, cmdsurface.PKCEResult) error) cmdsurface.PKCEProvider {
	return cmdsurface.PKCEProvider{
		Name: "mail",
		Config: oauth2.Config{
			ClientID: "client-1",
			Scopes:   []string{"mail.read", "offline_access"},
			Endpoint: oauth2.Endpoint{AuthURL: f.srv.URL + "/authorize", TokenURL: f.srv.URL + "/token"},
		},
		SecretRef:      "mail/refresh",
		ExpectedIssuer: f.srv.URL,
		OnResult:       onResult,
	}
}

// browse follows redirects like a browser, returning the final status.
func browse(t *testing.T, u string) (int, string) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 512)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, sb.String()
}

func runLoopback(t *testing.T, f *fakeAS, timeout time.Duration, drive bool) (cmdsurface.PKCEResult, int, string, error) {
	t.Helper()
	var status int
	var body string
	var wg sync.WaitGroup
	res, err := cmdsurface.LoopbackPKCE(context.Background(), f.provider(nil), cmdsurface.LoopbackOptions{
		Timeout: timeout,
		OnReady: func(authorizeURL string) {
			if !drive {
				return
			}
			wg.Add(1)
			go func() { defer wg.Done(); status, body = browse(t, authorizeURL) }()
		},
	})
	wg.Wait()
	return res, status, body, err
}

func TestPKCE_LoopbackSuccess(t *testing.T) {
	f := newFakeAS(t)
	res, status, _, err := runLoopback(t, f, 5*time.Second, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("browser status=%d", status)
	}
	if !f.verifierOK {
		t.Fatal("verifier does not match challenge at token endpoint")
	}
	if f.method != "S256" || len(f.challenge) != 43 {
		t.Errorf("method=%q challenge len=%d", f.method, len(f.challenge))
	}
	if f.tokenForm.Get("client_id") != "client-1" || f.tokenForm.Get("grant_type") != "authorization_code" ||
		f.tokenForm.Get("code") != "code-1" {
		t.Errorf("token form=%v", f.tokenForm)
	}
	if !strings.HasPrefix(f.tokenForm.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Errorf("redirect_uri=%q", f.tokenForm.Get("redirect_uri"))
	}
	if err := res.Grant.Validate(); err != nil {
		t.Fatalf("grant invalid: %v", err)
	}
	if res.Grant.Kind != oauth.RefreshToken || res.Grant.SecretRef != "mail/refresh" ||
		res.Grant.ClientID != "client-1" || res.Grant.TokenURL != f.srv.URL+"/token" {
		t.Errorf("grant=%v", res.Grant.Kind)
	}
	if string(res.RefreshSecret) != `{"v":1,"kind":"secret","value":"rt-1"}` {
		t.Errorf("secret envelope shape")
	}
	if res.Token.AccessToken != "at-1" || res.Token.ExpiresAt.IsZero() {
		t.Errorf("access token missing")
	}
}

func TestPKCE_VerifierNeverInAuthorizeURL(t *testing.T) {
	f := newFakeAS(t)
	var authURL string
	_, _ = cmdsurface.LoopbackPKCE(context.Background(), f.provider(nil), cmdsurface.LoopbackOptions{
		Timeout: 2 * time.Second,
		OnReady: func(u string) {
			authURL = u
			go browse(t, u)
		},
	})
	if strings.Contains(authURL, "verifier") {
		t.Errorf("authorize url leaks verifier: %s", authURL)
	}
	f.mu.Lock()
	form := f.tokenForm
	f.mu.Unlock()
	if v := form.Get("code_verifier"); v == "" || strings.Contains(authURL, v) {
		t.Errorf("verifier missing or in url")
	}
}

func TestPKCE_LoopbackDeniedConsent(t *testing.T) {
	f := newFakeAS(t)
	f.deny = true
	_, status, _, err := runLoopback(t, f, 5*time.Second, true)
	if !errors.Is(err, cmdsurface.ErrOAuthDenied) {
		t.Fatalf("err=%v want ErrOAuthDenied", err)
	}
	if status != http.StatusBadRequest {
		t.Errorf("status=%d", status)
	}
	if f.tokenCalls != 0 {
		t.Error("token endpoint called after denial")
	}
}

func TestPKCE_LoopbackTimeout(t *testing.T) {
	f := newFakeAS(t)
	start := time.Now()
	_, _, _, err := runLoopback(t, f, 150*time.Millisecond, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v want deadline exceeded", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("timeout not honored")
	}
}

func TestPKCE_LoopbackIssuerMismatch(t *testing.T) {
	f := newFakeAS(t)
	f.badISS = true
	_, status, _, err := runLoopback(t, f, 5*time.Second, true)
	if err == nil || status != http.StatusBadRequest || f.tokenCalls != 0 {
		t.Fatalf("err=%v status=%d calls=%d", err, status, f.tokenCalls)
	}
}

func TestPKCE_LoopbackMissingIssuer(t *testing.T) {
	f := newFakeAS(t)
	f.noISS = true
	_, _, _, err := runLoopback(t, f, 5*time.Second, true)
	if err == nil || f.tokenCalls != 0 {
		t.Fatalf("err=%v calls=%d", err, f.tokenCalls)
	}
}

func TestPKCE_StateMismatchRejectedNoExchange(t *testing.T) {
	f := newFakeAS(t)
	f.wrongState = true
	// Stray bad-state callbacks must not abort or exchange; the login times out.
	_, status, _, err := runLoopback(t, f, 300*time.Millisecond, true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if status != http.StatusBadRequest || f.tokenCalls != 0 {
		t.Fatalf("status=%d calls=%d", status, f.tokenCalls)
	}
}

func TestPKCE_StateSingleUse(t *testing.T) {
	f := newFakeAS(t)
	var got []cmdsurface.PKCEResult
	var mu sync.Mutex
	p := f.provider(func(_ context.Context, r cmdsurface.PKCEResult) error {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
		return nil
	})
	store := cmdsurface.NewInMemoryPKCEStateStore()
	router := api.NewRouter()
	srv3 := httptest.NewServer(router)
	defer srv3.Close()
	p.Config.RedirectURL = srv3.URL + "/oauth/mail/callback"
	if err := cmdsurface.MountOAuthPKCE(router, []cmdsurface.PKCEProvider{p}, store); err != nil {
		t.Fatal(err)
	}

	// authorize on srv3, capture the callback URL instead of following it.
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(srv3.URL + "/oauth/mail/authorize")
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize: %v %v", err, resp)
	}
	resp.Body.Close()
	resp, err = c.Get(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("as: %v", err)
	}
	cb := resp.Header.Get("Location")
	resp.Body.Close()
	r1, _ := c.Get(cb)
	r1.Body.Close()
	r2, _ := c.Get(cb)
	r2.Body.Close()
	if r1.StatusCode != 200 || r2.StatusCode != http.StatusBadRequest {
		t.Fatalf("first=%d replay=%d", r1.StatusCode, r2.StatusCode)
	}
	if f.tokenCalls != 1 {
		t.Fatalf("token calls=%d", f.tokenCalls)
	}
}

func TestPKCE_ExchangeErrorMapping(t *testing.T) {
	cases := map[string]error{
		"invalid_grant":  oauth.ErrNeedsReconsent,
		"invalid_client": oauth.ErrCredentialRejected,
	}
	for code, want := range cases {
		f := newFakeAS(t)
		f.tokenErr = code
		_, _, body, err := runLoopback(t, f, 5*time.Second, true)
		if !errors.Is(err, want) {
			t.Errorf("%s: err=%v want %v", code, err, want)
		}
		if strings.Contains(body, "code-1") {
			t.Errorf("auth code echoed to browser")
		}
	}
}

func TestMountOAuthPKCE_Validation(t *testing.T) {
	r := api.NewRouter()
	store := cmdsurface.NewInMemoryPKCEStateStore()
	f := newFakeAS(t)
	good := f.provider(nil)
	bad := good
	bad.Name = ""
	for name, fn := range map[string]func() error{
		"nil router": func() error { return cmdsurface.MountOAuthPKCE(nil, []cmdsurface.PKCEProvider{good}, store) },
		"nil store":  func() error { return cmdsurface.MountOAuthPKCE(r, []cmdsurface.PKCEProvider{good}, nil) },
		"empty name": func() error { return cmdsurface.MountOAuthPKCE(r, []cmdsurface.PKCEProvider{bad}, store) },
	} {
		if fn() == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestPKCE_ExchangeDoesNotFollowRedirect(t *testing.T) {
	var hit int
	var mu sync.Mutex
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		mu.Lock()
		hit++
		mu.Unlock()
	}))
	defer other.Close()
	f := newFakeAS(t)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	p := f.provider(nil)
	p.Config.Endpoint.TokenURL = redir.URL
	_, err := cmdsurface.LoopbackPKCE(context.Background(), p, cmdsurface.LoopbackOptions{
		Timeout: 5 * time.Second,
		OnReady: func(u string) { go browse(t, u) },
	})
	if err == nil || errors.Is(err, oauth.ErrTransient) {
		t.Fatalf("err=%v want non-transient failure", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hit != 0 {
		t.Fatalf("redirect target hit %d times", hit)
	}
}

func TestPKCE_ConfidentialClientGrantRefs(t *testing.T) {
	f := newFakeAS(t)
	p := f.provider(nil)
	p.Config.ClientSecret = "cs-" + strings.Repeat("z", 6)
	p.ClientSecretRef = "mail/client-secret"
	res, err := cmdsurface.LoopbackPKCE(context.Background(), p, cmdsurface.LoopbackOptions{
		Timeout: 5 * time.Second, OnReady: func(u string) { go browse(t, u) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Grant.ClientSecretRef != "mail/client-secret" || f.tokenForm.Get("client_secret") != p.Config.ClientSecret {
		t.Errorf("client secret not wired")
	}
	if err := res.Grant.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := f.provider(nil)
	bad.Config.ClientSecret = "x"
	if cmdsurface.MountOAuthPKCE(api.NewRouter(), []cmdsurface.PKCEProvider{bad}, cmdsurface.NewInMemoryPKCEStateStore()) == nil {
		t.Error("ClientSecret without ClientSecretRef accepted")
	}
}
