package cmdsurface_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// RFC 9207 issuer validation tests. The callback surface is the
// OAuth client's authorization-response receiver, so `iss` checking
// attaches here. Providers without ExpectedIssuer keep the exact
// pre-existing behavior; every test in this file opts in explicitly
// except the ignore-when-unconfigured regression.

const issTestIssuer = "https://as.example"

// issProvider returns a provider with issuer validation enabled.
func issProvider(overrides func(*cmdsurface.OAuthProvider)) cmdsurface.OAuthProvider {
	p := cmdsurface.OAuthProvider{
		Name:           "test",
		Path:           []string{"auth", "oauth-link"},
		FlagFromQuery:  map[string]string{"code": "code"},
		ExpectedIssuer: issTestIssuer,
	}
	if overrides != nil {
		overrides(&p)
	}
	return p
}

// issBridge returns a bridge + runner with the standard OAuth test
// tree and the callback surface exposed.
func issBridge() (*cmdsurface.Bridge, *oauthFakeRunner) {
	runner := &oauthFakeRunner{}
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(runner),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).Expose("auth oauth-link", cmdsurface.SurfaceOAuthCB)
	return b, runner
}

// 1. iss present and matching → happy path proceeds unchanged.
func TestOAuthSurface_IssuerMatchHappyPath(t *testing.T) {
	b, runner := issBridge()
	store := cmdsurface.NewInMemoryStateStore()

	srvURL, stop := oauthServer(t, b, []cmdsurface.OAuthProvider{issProvider(nil)}, store)
	defer stop()

	state, _ := store.Issue(context.Background(), "test", time.Minute)
	status, _, body := oauthGet(t, srvURL+"/oauth/test/callback?code=abc&state="+
		url.QueryEscape(state)+"&iss="+url.QueryEscape(issTestIssuer))
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", status, body)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if got[0].Flags["code"] != "abc" {
		t.Errorf("flags[code]=%v want abc", got[0].Flags["code"])
	}
}

// 2. iss mismatch → 400 issuer_mismatch; runner never invoked and
// state not consumed (rejection happens before state validation).
func TestOAuthSurface_IssuerMismatch(t *testing.T) {
	b, runner := issBridge()
	store := cmdsurface.NewInMemoryStateStore()

	srvURL, stop := oauthServer(t, b, []cmdsurface.OAuthProvider{issProvider(nil)}, store)
	defer stop()

	state, _ := store.Issue(context.Background(), "test", time.Minute)
	status, _, body := oauthGet(t, srvURL+"/oauth/test/callback?code=abc&state="+
		url.QueryEscape(state)+"&iss="+url.QueryEscape("https://evil.example"))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", status, body)
	}
	if !strings.Contains(string(body), "issuer_mismatch") {
		t.Errorf("body=%q want issuer_mismatch", body)
	}
	if len(runner.captured()) != 0 {
		t.Errorf("runner captured=%d want 0", len(runner.captured()))
	}
	// State must still be intact: the mismatch was rejected before
	// Consume ran.
	if err := store.Consume(context.Background(), "test", state); err != nil {
		t.Errorf("state consumed by rejected callback: %v", err)
	}
}

// 3. iss absent while ExpectedIssuer configured → 400 missing_iss.
func TestOAuthSurface_IssuerMissing(t *testing.T) {
	b, runner := issBridge()
	store := cmdsurface.NewInMemoryStateStore()

	srvURL, stop := oauthServer(t, b, []cmdsurface.OAuthProvider{issProvider(nil)}, store)
	defer stop()

	state, _ := store.Issue(context.Background(), "test", time.Minute)
	status, _, body := oauthGet(t, srvURL+"/oauth/test/callback?code=abc&state="+url.QueryEscape(state))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", status, body)
	}
	if !strings.Contains(string(body), "missing_iss") {
		t.Errorf("body=%q want missing_iss", body)
	}
	if len(runner.captured()) != 0 {
		t.Errorf("runner captured=%d want 0", len(runner.captured()))
	}
}

// 4. ExpectedIssuer unset → an inbound iss parameter is ignored and
// the pre-existing flow is untouched (legacy-provider regression).
func TestOAuthSurface_IssuerNotConfiguredIgnoresIss(t *testing.T) {
	b, runner := issBridge()
	store := cmdsurface.NewInMemoryStateStore()

	p := issProvider(func(p *cmdsurface.OAuthProvider) { p.ExpectedIssuer = "" })
	srvURL, stop := oauthServer(t, b, []cmdsurface.OAuthProvider{p}, store)
	defer stop()

	state, _ := store.Issue(context.Background(), "test", time.Minute)
	status, _, body := oauthGet(t, srvURL+"/oauth/test/callback?code=abc&state="+
		url.QueryEscape(state)+"&iss="+url.QueryEscape("https://anything.example"))
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", status, body)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if _, present := got[0].Meta.Extra["oauth_issuer"]; present {
		t.Errorf("Meta.Extra[oauth_issuer] present without ExpectedIssuer: %v", got[0].Meta.Extra)
	}
}

// 5. RFC 9207 puts iss on error responses too: a provider error with
// a wrong iss is reported as issuer_mismatch, not provider_error.
func TestOAuthSurface_IssuerMismatchBeatsProviderError(t *testing.T) {
	b, _ := issBridge()
	store := cmdsurface.NewInMemoryStateStore()

	srvURL, stop := oauthServer(t, b, []cmdsurface.OAuthProvider{issProvider(nil)}, store)
	defer stop()

	status, _, body := oauthGet(t, srvURL+"/oauth/test/callback?error=access_denied&iss="+
		url.QueryEscape("https://evil.example"))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", status, body)
	}
	if !strings.Contains(string(body), "issuer_mismatch") {
		t.Errorf("body=%q want issuer_mismatch", body)
	}
	if strings.Contains(string(body), "provider_error") {
		t.Errorf("body=%q must not report provider_error", body)
	}
}

// 6. Provider error with a matching iss is still a provider error.
func TestOAuthSurface_IssuerMatchProviderErrorStillReported(t *testing.T) {
	b, _ := issBridge()
	store := cmdsurface.NewInMemoryStateStore()

	srvURL, stop := oauthServer(t, b, []cmdsurface.OAuthProvider{issProvider(nil)}, store)
	defer stop()

	status, _, body := oauthGet(t, srvURL+"/oauth/test/callback?error=access_denied&iss="+
		url.QueryEscape(issTestIssuer))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", status, body)
	}
	if !strings.Contains(string(body), "provider_error:access_denied") {
		t.Errorf("body=%q want provider_error:access_denied", body)
	}
}

// 7. Simple string comparison (RFC 9207 section 2.4 via RFC 3986
// section 6.2.1): no normalization — a trailing slash is a mismatch.
func TestOAuthSurface_IssuerSimpleStringComparison(t *testing.T) {
	b, runner := issBridge()
	store := cmdsurface.NewInMemoryStateStore()

	srvURL, stop := oauthServer(t, b, []cmdsurface.OAuthProvider{issProvider(nil)}, store)
	defer stop()

	state, _ := store.Issue(context.Background(), "test", time.Minute)
	status, _, body := oauthGet(t, srvURL+"/oauth/test/callback?code=abc&state="+
		url.QueryEscape(state)+"&iss="+url.QueryEscape(issTestIssuer+"/"))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", status, body)
	}
	if !strings.Contains(string(body), "issuer_mismatch") {
		t.Errorf("body=%q want issuer_mismatch", body)
	}
	if len(runner.captured()) != 0 {
		t.Errorf("runner captured=%d want 0", len(runner.captured()))
	}
}

// 8. Validated issuer is recorded in Meta.Extra["oauth_issuer"] so
// sinks / custom Runners can bind minted credentials to the issuing
// authorization server.
func TestOAuthSurface_IssuerRecordedInMetaExtra(t *testing.T) {
	b, runner := issBridge()
	store := cmdsurface.NewInMemoryStateStore()

	srvURL, stop := oauthServer(t, b, []cmdsurface.OAuthProvider{issProvider(nil)}, store)
	defer stop()

	state, _ := store.Issue(context.Background(), "test", time.Minute)
	status, _, body := oauthGet(t, srvURL+"/oauth/test/callback?code=abc&state="+
		url.QueryEscape(state)+"&iss="+url.QueryEscape(issTestIssuer))
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", status, body)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if iss := got[0].Meta.Extra["oauth_issuer"]; iss != issTestIssuer {
		t.Errorf("Meta.Extra[oauth_issuer]=%q want %q", iss, issTestIssuer)
	}
}

// 9. iss mapped through FlagFromQuery reaches the leaf validated —
// the adopter-side hook for binding credentials to the issuer.
func TestOAuthSurface_IssuerForwardedViaFlagFromQuery(t *testing.T) {
	b, runner := issBridge()
	store := cmdsurface.NewInMemoryStateStore()

	p := issProvider(func(p *cmdsurface.OAuthProvider) {
		p.FlagFromQuery = map[string]string{"code": "code", "iss": "issuer"}
	})
	srvURL, stop := oauthServer(t, b, []cmdsurface.OAuthProvider{p}, store)
	defer stop()

	state, _ := store.Issue(context.Background(), "test", time.Minute)
	status, _, body := oauthGet(t, srvURL+"/oauth/test/callback?code=abc&state="+
		url.QueryEscape(state)+"&iss="+url.QueryEscape(issTestIssuer))
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", status, body)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if got[0].Flags["issuer"] != issTestIssuer {
		t.Errorf("flags[issuer]=%v want %q", got[0].Flags["issuer"], issTestIssuer)
	}
}

// 10. ErrorRedirect branch: mismatch redirects with
// ?error=issuer_mismatch.
func TestOAuthSurface_IssuerMismatchErrorRedirect(t *testing.T) {
	b, _ := issBridge()
	store := cmdsurface.NewInMemoryStateStore()

	p := issProvider(func(p *cmdsurface.OAuthProvider) {
		p.ErrorRedirect = "https://app/oauth/err"
	})
	srvURL, stop := oauthServer(t, b, []cmdsurface.OAuthProvider{p}, store)
	defer stop()

	status, loc, _ := oauthGet(t, srvURL+"/oauth/test/callback?code=abc&iss="+
		url.QueryEscape("https://evil.example"))
	if status != http.StatusFound {
		t.Fatalf("status=%d want 302", status)
	}
	if !strings.Contains(loc, "error=issuer_mismatch") {
		t.Errorf("location=%q want ?error=issuer_mismatch", loc)
	}
}

// 11. Mount-time refusal: malformed ExpectedIssuer values.
func TestOAuthSurface_ExpectedIssuerInvalidMountError(t *testing.T) {
	cases := []struct {
		name   string
		issuer string
	}{
		{"relative", "as.example/path"},
		{"with query", "https://as.example?x=1"},
		{"with fragment", "https://as.example#frag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := issBridge()
			store := cmdsurface.NewInMemoryStateStore()
			p := issProvider(func(p *cmdsurface.OAuthProvider) {
				p.ExpectedIssuer = tc.issuer
			})
			router := api.NewRouter()
			err := cmdsurface.MountOAuth(b, router, []cmdsurface.OAuthProvider{p}, store)
			if err == nil {
				t.Fatalf("MountOAuth: want error for ExpectedIssuer %q", tc.issuer)
			}
			if !strings.Contains(err.Error(), "ExpectedIssuer") {
				t.Errorf("err=%v want ExpectedIssuer in message", err)
			}
		})
	}
}
