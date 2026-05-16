package cmdsurface_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// oauthFakeRunner records every Invocation routed through the bridge
// and dispatches a configurable RunFn. Streaming is not supported.
type oauthFakeRunner struct {
	mu  sync.Mutex
	got []cmdsurface.Invocation

	RunFn func(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error)
}

func (f *oauthFakeRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	f.mu.Lock()
	f.got = append(f.got, inv)
	f.mu.Unlock()
	if f.RunFn != nil {
		return f.RunFn(ctx, inv)
	}
	return cmdsurface.Result{Stdout: strings.Join(inv.Path, " ")}, nil
}

func (f *oauthFakeRunner) Stream(context.Context, cmdsurface.Invocation, chan<- cmdsurface.Event) error {
	return errors.New("oauthFakeRunner: Stream not supported")
}

func (f *oauthFakeRunner) captured() []cmdsurface.Invocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]cmdsurface.Invocation, len(f.got))
	copy(out, f.got)
	return out
}

// oauthTestTree builds the cobra tree used by every OAuth surface
// test. It exposes a non-destructive "auth oauth-link" leaf, a
// destructive variant, and a confirmation-required variant.
//
//	root
//	├── auth oauth-link               (write)
//	├── auth oauth-link-destructive   (destructive)
//	└── auth oauth-link-confirm       (requires-confirmation)
func oauthTestTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	auth := &cobra.Command{Use: "auth"}

	link := &cobra.Command{
		Use:   "oauth-link",
		Short: "Link an OAuth account",
		RunE:  func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{
			"kit/side-effect": "write",
		},
	}
	dest := &cobra.Command{
		Use:   "oauth-link-destructive",
		Short: "Destructive variant for tests",
		RunE:  func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{
			"kit/side-effect": "destructive",
		},
	}
	confirm := &cobra.Command{
		Use:   "oauth-link-confirm",
		Short: "Confirmation-required variant",
		RunE:  func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{
			"kit/side-effect":           "write",
			"kit/requires-confirmation": "true",
		},
	}

	auth.AddCommand(link, dest, confirm)
	root.AddCommand(auth)
	return root
}

// oauthClient returns an http.Client that does NOT follow redirects so
// tests can inspect 302 Location headers directly.
func oauthClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// oauthServer builds an httptest server with MountOAuth applied to b
// for providers, store, and opts. Returns the server URL and teardown.
func oauthServer(t *testing.T, b *cmdsurface.Bridge, providers []cmdsurface.OAuthProvider, store cmdsurface.StateStore, opts ...cmdsurface.OAuthOption) (string, func()) {
	t.Helper()
	router := api.NewRouter()
	if err := cmdsurface.MountOAuth(b, router, providers, store, opts...); err != nil {
		t.Fatalf("MountOAuth: %v", err)
	}
	srv := httptest.NewServer(router)
	return srv.URL, srv.Close
}

// oauthGet issues a redirect-disabled GET and returns status + Location
// header + body.
func oauthGet(t *testing.T, url string) (int, string, []byte) {
	t.Helper()
	resp, err := oauthClient().Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), body
}

// oauthDefaultPolicy is the policy used by tests that expose the
// callback surface for non-destructive leaves. Destructive cases
// override with their own policy.
func oauthDefaultPolicy() cmdsurface.Policy {
	return cmdsurface.Policy{
		DefaultEnabled: []cmdsurface.Surface{
			cmdsurface.SurfaceCLI,
			cmdsurface.SurfaceLib,
		},
	}
}

// 1. Authorize redirects with state appended.
func TestOAuthSurface_AuthorizeRedirectsWithState(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(&oauthFakeRunner{}),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).Expose("auth oauth-link", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{{
		Name: "test",
		Path: []string{"auth", "oauth-link"},
	}}

	srvURL, stop := oauthServer(t, b, providers, store,
		cmdsurface.WithOAuthAuthorizeFn(func(string) (string, error) {
			return "https://provider/x", nil
		}),
	)
	defer stop()

	status, loc, _ := oauthGet(t, srvURL+"/oauth/test/authorize")
	if status != http.StatusFound {
		t.Fatalf("status=%d want 302", status)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse location %q: %v", loc, err)
	}
	if u.Scheme != "https" || u.Host != "provider" || u.Path != "/x" {
		t.Errorf("location=%q want https://provider/x?state=...", loc)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Errorf("redirect missing state param: %q", loc)
	}
}

// 2. Authorize fn missing → 501 not_configured.
func TestOAuthSurface_AuthorizeFnMissingReturns501(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(&oauthFakeRunner{}),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).Expose("auth oauth-link", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{{
		Name: "test",
		Path: []string{"auth", "oauth-link"},
	}}

	srvURL, stop := oauthServer(t, b, providers, store)
	defer stop()

	status, _, body := oauthGet(t, srvURL+"/oauth/test/authorize")
	if status != http.StatusNotImplemented {
		t.Fatalf("status=%d want 501; body=%s", status, body)
	}
	if !strings.Contains(string(body), "not_configured") {
		t.Errorf("body=%q want not_configured", body)
	}
}

// 3. Callback happy path — fake runner observes inv.Flags + redirect.
func TestOAuthSurface_CallbackHappyPath(t *testing.T) {
	runner := &oauthFakeRunner{}
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(runner),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).Expose("auth oauth-link", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{{
		Name:            "test",
		Path:            []string{"auth", "oauth-link"},
		FlagFromQuery:   map[string]string{"code": "code"},
		SuccessRedirect: "https://app/done",
	}}

	srvURL, stop := oauthServer(t, b, providers, store)
	defer stop()

	state, err := store.Issue(context.Background(), "test", time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	status, loc, body := oauthGet(t, srvURL+"/oauth/test/callback?code=abc&state="+url.QueryEscape(state))
	if status != http.StatusFound {
		t.Fatalf("status=%d want 302; body=%s", status, body)
	}
	if loc != "https://app/done" {
		t.Errorf("location=%q want https://app/done", loc)
	}
	captured := runner.captured()
	if len(captured) != 1 {
		t.Fatalf("captured=%d want 1", len(captured))
	}
	if got := captured[0].Flags["code"]; got != "abc" {
		t.Errorf("flags[code]=%v want abc", got)
	}
}

// 4. Callback happy plain page (no SuccessRedirect).
func TestOAuthSurface_CallbackHappyPlainPage(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(&oauthFakeRunner{}),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).Expose("auth oauth-link", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{{
		Name:          "test",
		Path:          []string{"auth", "oauth-link"},
		FlagFromQuery: map[string]string{"code": "code"},
	}}

	srvURL, stop := oauthServer(t, b, providers, store)
	defer stop()

	state, _ := store.Issue(context.Background(), "test", time.Minute)
	status, _, body := oauthGet(t, srvURL+"/oauth/test/callback?code=abc&state="+url.QueryEscape(state))
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", status, body)
	}
	if !strings.Contains(string(body), "OAuth complete") {
		t.Errorf("body=%q want 'OAuth complete'", body)
	}
}

// 5. Callback missing state → ErrorRedirect with ?error=missing_state.
func TestOAuthSurface_CallbackMissingState(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(&oauthFakeRunner{}),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).Expose("auth oauth-link", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{{
		Name:          "test",
		Path:          []string{"auth", "oauth-link"},
		ErrorRedirect: "https://app/oauth/err",
	}}

	srvURL, stop := oauthServer(t, b, providers, store)
	defer stop()

	status, loc, _ := oauthGet(t, srvURL+"/oauth/test/callback?code=abc")
	if status != http.StatusFound {
		t.Fatalf("status=%d want 302", status)
	}
	if !strings.Contains(loc, "error=missing_state") {
		t.Errorf("location=%q want ?error=missing_state", loc)
	}
}

// 6. Callback invalid state.
func TestOAuthSurface_CallbackInvalidState(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(&oauthFakeRunner{}),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).Expose("auth oauth-link", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{{
		Name:          "test",
		Path:          []string{"auth", "oauth-link"},
		ErrorRedirect: "https://app/oauth/err",
	}}

	srvURL, stop := oauthServer(t, b, providers, store)
	defer stop()

	status, loc, _ := oauthGet(t, srvURL+"/oauth/test/callback?code=abc&state=bogus")
	if status != http.StatusFound {
		t.Fatalf("status=%d want 302", status)
	}
	if !strings.Contains(loc, "error=invalid_state") {
		t.Errorf("location=%q want ?error=invalid_state", loc)
	}
}

// 7. State issued for "github" consumed at "/oauth/google/callback" →
// invalid_state.
func TestOAuthSurface_CallbackStateFromWrongProvider(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(&oauthFakeRunner{}),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).
		Expose("auth oauth-link", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{
		{Name: "github", Path: []string{"auth", "oauth-link"}},
		{Name: "google", Path: []string{"auth", "oauth-link"}},
	}

	srvURL, stop := oauthServer(t, b, providers, store)
	defer stop()

	// Issue state for github; replay against google → invalid_state.
	state, _ := store.Issue(context.Background(), "github", time.Minute)
	status, _, body := oauthGet(t, srvURL+"/oauth/google/callback?state="+url.QueryEscape(state))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", status, body)
	}
	if !strings.Contains(string(body), "invalid_state") {
		t.Errorf("body=%q want invalid_state", body)
	}
}

// 8. Provider rejection: ?error=access_denied → provider_error.
func TestOAuthSurface_CallbackProviderError(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(&oauthFakeRunner{}),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).Expose("auth oauth-link", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{{
		Name:          "test",
		Path:          []string{"auth", "oauth-link"},
		ErrorRedirect: "https://app/oauth/err",
	}}

	srvURL, stop := oauthServer(t, b, providers, store)
	defer stop()

	status, loc, _ := oauthGet(t, srvURL+"/oauth/test/callback?error=access_denied")
	if status != http.StatusFound {
		t.Fatalf("status=%d want 302", status)
	}
	if !strings.Contains(loc, "error=provider_error") {
		t.Errorf("location=%q want provider_error:access_denied", loc)
	}
	if !strings.Contains(loc, "access_denied") {
		t.Errorf("location=%q want access_denied", loc)
	}
}

// 9. Mount-time error: unknown leaf path.
func TestOAuthSurface_UnknownLeafMountError(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(&oauthFakeRunner{}),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	)

	providers := []cmdsurface.OAuthProvider{{
		Name: "test",
		Path: []string{"does", "not", "exist"},
	}}

	router := api.NewRouter()
	err := cmdsurface.MountOAuth(b, router, providers, store)
	if err == nil {
		t.Fatal("MountOAuth: want error for unknown leaf")
	}
	if !errors.Is(err, cmdsurface.ErrUnknownCommand) {
		t.Errorf("err=%v want ErrUnknownCommand", err)
	}
}

// 10. Mount-time error: surface not enabled.
func TestOAuthSurface_SurfaceNotEnabledMountError(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(&oauthFakeRunner{}),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	) // intentionally no Expose

	providers := []cmdsurface.OAuthProvider{{
		Name: "test",
		Path: []string{"auth", "oauth-link"},
	}}

	router := api.NewRouter()
	err := cmdsurface.MountOAuth(b, router, providers, store)
	if err == nil {
		t.Fatal("MountOAuth: want error for unexposed surface")
	}
	if !errors.Is(err, cmdsurface.ErrSurfaceNotEnabled) {
		t.Errorf("err=%v want ErrSurfaceNotEnabled", err)
	}
}

// 11. Mount-time error: destructive leaf without opt-in.
func TestOAuthSurface_DestructiveWithoutOptIn(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(&oauthFakeRunner{}),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).Expose("auth oauth-link-destructive", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{{
		Name: "test",
		Path: []string{"auth", "oauth-link-destructive"},
	}}

	router := api.NewRouter()
	err := cmdsurface.MountOAuth(b, router, providers, store)
	if err == nil {
		t.Fatal("MountOAuth: want error for destructive without opt-in")
	}
	if !errors.Is(err, cmdsurface.ErrDestructiveBlocked) {
		t.Errorf("err=%v want ErrDestructiveBlocked", err)
	}
}

// 12. Destructive with opt-in mounts OK; happy path works.
func TestOAuthSurface_DestructiveWithOptIn(t *testing.T) {
	runner := &oauthFakeRunner{}
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(runner),
		cmdsurface.WithPolicy(cmdsurface.Policy{
			AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceOAuthCB},
			DefaultEnabled: []cmdsurface.Surface{
				cmdsurface.SurfaceCLI,
				cmdsurface.SurfaceLib,
			},
		}),
	).Expose("auth oauth-link-destructive", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{{
		Name:          "test",
		Path:          []string{"auth", "oauth-link-destructive"},
		FlagFromQuery: map[string]string{"code": "code"},
	}}

	srvURL, stop := oauthServer(t, b, providers, store)
	defer stop()

	state, _ := store.Issue(context.Background(), "test", time.Minute)
	status, _, body := oauthGet(t, srvURL+"/oauth/test/callback?code=abc&state="+url.QueryEscape(state))
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", status, body)
	}
	if len(runner.captured()) != 1 {
		t.Errorf("runner captured=%d want 1", len(runner.captured()))
	}
}

// 13. Confirmation-required leaf → mount-time error.
func TestOAuthSurface_ConfirmationRequiredMountError(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(&oauthFakeRunner{}),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).Expose("auth oauth-link-confirm", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{{
		Name: "test",
		Path: []string{"auth", "oauth-link-confirm"},
	}}

	router := api.NewRouter()
	err := cmdsurface.MountOAuth(b, router, providers, store)
	if err == nil {
		t.Fatal("MountOAuth: want error for confirmation-required leaf")
	}
	if !strings.Contains(err.Error(), "confirmation") {
		t.Errorf("err=%v want confirmation in message", err)
	}
}

// 14. State expires after TTL.
func TestOAuthSurface_StateExpires(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	state, err := store.Issue(context.Background(), "test", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := store.Consume(context.Background(), "test", state); !errors.Is(err, cmdsurface.ErrUnknownState) {
		t.Errorf("err=%v want ErrUnknownState", err)
	}
}

// 15. State consumed twice — second Consume returns ErrUnknownState.
func TestOAuthSurface_StateConsumedTwice(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	state, _ := store.Issue(context.Background(), "test", time.Minute)
	if err := store.Consume(context.Background(), "test", state); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	if err := store.Consume(context.Background(), "test", state); !errors.Is(err, cmdsurface.ErrUnknownState) {
		t.Errorf("second Consume err=%v want ErrUnknownState", err)
	}
}

// 16. Concurrent Consume on the same state — exactly one wins.
func TestOAuthSurface_ConcurrentConsume(t *testing.T) {
	store := cmdsurface.NewInMemoryStateStore()
	state, _ := store.Issue(context.Background(), "test", time.Minute)

	const N = 32
	var wg sync.WaitGroup
	wg.Add(N)
	var wins int32
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if err := store.Consume(context.Background(), "test", state); err == nil {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&wins); got != 1 {
		t.Errorf("wins=%d want 1", got)
	}
}

// 17. Meta.Surface forced + Caller set to provider.Name.
func TestOAuthSurface_MetaSurfaceForced(t *testing.T) {
	runner := &oauthFakeRunner{}
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(runner),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).Expose("auth oauth-link", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{{
		Name: "github",
		Path: []string{"auth", "oauth-link"},
	}}

	srvURL, stop := oauthServer(t, b, providers, store)
	defer stop()

	state, _ := store.Issue(context.Background(), "github", time.Minute)
	status, _, body := oauthGet(t, srvURL+"/oauth/github/callback?state="+url.QueryEscape(state))
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", status, body)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if got[0].Meta.Surface != cmdsurface.SurfaceOAuthCB {
		t.Errorf("Meta.Surface=%q want oauth-cb", got[0].Meta.Surface)
	}
	if got[0].Meta.Caller != "github" {
		t.Errorf("Meta.Caller=%q want github", got[0].Meta.Caller)
	}
}

// 18. FlagFromQuery filter — query has extras, only mapped flags reach
// the runner.
func TestOAuthSurface_FlagFromQueryFilter(t *testing.T) {
	runner := &oauthFakeRunner{}
	store := cmdsurface.NewInMemoryStateStore()
	b := cmdsurface.New(oauthTestTree(),
		cmdsurface.WithRunner(runner),
		cmdsurface.WithPolicy(oauthDefaultPolicy()),
	).Expose("auth oauth-link", cmdsurface.SurfaceOAuthCB)

	providers := []cmdsurface.OAuthProvider{{
		Name: "test",
		Path: []string{"auth", "oauth-link"},
		FlagFromQuery: map[string]string{
			"code":  "auth_code",
			"scope": "scopes",
			// "extra" intentionally not mapped
		},
	}}

	srvURL, stop := oauthServer(t, b, providers, store)
	defer stop()

	state, _ := store.Issue(context.Background(), "test", time.Minute)
	url := srvURL + "/oauth/test/callback?code=abc&scope=read&extra=ignored&state=" + state
	status, _, body := oauthGet(t, url)
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", status, body)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	flags := got[0].Flags
	if flags["auth_code"] != "abc" {
		t.Errorf("flags[auth_code]=%v want abc", flags["auth_code"])
	}
	if flags["scopes"] != "read" {
		t.Errorf("flags[scopes]=%v want read", flags["scopes"])
	}
	if _, present := flags["extra"]; present {
		t.Errorf("flags[extra] present; want filtered out")
	}
	if len(flags) != 2 {
		t.Errorf("flags len=%d want 2: %v", len(flags), flags)
	}
}
