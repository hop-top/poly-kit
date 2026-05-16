package cmdsurface_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

// signedFakeRunner records each Invocation it sees and returns the
// configured fn result (or a default echo).
type signedFakeRunner struct {
	mu  sync.Mutex
	got []cmdsurface.Invocation
	fn  func(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error)
}

func (r *signedFakeRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	r.mu.Lock()
	r.got = append(r.got, inv)
	r.mu.Unlock()
	if r.fn != nil {
		return r.fn(ctx, inv)
	}
	return cmdsurface.Result{Stdout: strings.Join(inv.Path, " ")}, nil
}

func (r *signedFakeRunner) Stream(context.Context, cmdsurface.Invocation, chan<- cmdsurface.Event) error {
	return errors.New("signedFakeRunner: Stream not supported")
}

func (r *signedFakeRunner) captured() []cmdsurface.Invocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]cmdsurface.Invocation, len(r.got))
	copy(out, r.got)
	return out
}

// signedTestTree mirrors the REST-surface tree but is named distinctly
// so parallel sibling agents (Webhook, OAuth) do not collide.
func signedTestTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

	auth := &cobra.Command{Use: "auth"}
	login := &cobra.Command{
		Use:         "login",
		Short:       "Log a user in via magic link",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "write"},
	}
	auth.AddCommand(login)

	sub := &cobra.Command{Use: "subscription"}
	cancel := &cobra.Command{
		Use:         "cancel",
		Short:       "Cancel an active subscription",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	}
	sub.AddCommand(cancel)

	root.AddCommand(auth, sub)

	ping := &cobra.Command{
		Use:         "ping",
		Short:       "Ping",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	root.AddCommand(ping)

	return root
}

// signedTestKey is a static test key. Sufficient entropy for the test
// suite; production callers source from secret/.
var signedTestKey = []byte("signed-surface-test-key-32bytes!")

// signedNewBridge constructs a Bridge with the runner and exposes the
// supplied paths on SurfaceSigned.
func signedNewBridge(runner cmdsurface.Runner, exposeWith []cmdsurface.Surface, paths ...string) *cmdsurface.Bridge {
	if exposeWith == nil {
		exposeWith = []cmdsurface.Surface{cmdsurface.SurfaceSigned}
	}
	b := cmdsurface.New(signedTestTree(), cmdsurface.WithRunner(runner))
	for _, p := range paths {
		b.Expose(p, exposeWith...)
	}
	return b
}

// signedNewBridgeWithPolicy is the same but also applies a Policy.
func signedNewBridgeWithPolicy(runner cmdsurface.Runner, pol cmdsurface.Policy, paths ...string) *cmdsurface.Bridge {
	b := cmdsurface.New(signedTestTree(),
		cmdsurface.WithRunner(runner),
		cmdsurface.WithPolicy(pol),
	)
	for _, p := range paths {
		b.Expose(p, cmdsurface.SurfaceSigned)
	}
	return b
}

// signedMount stands up a verifier server and returns its URL.
func signedMount(t *testing.T, b *cmdsurface.Bridge, store cmdsurface.NonceStore, opts ...cmdsurface.SignedOption) (string, func()) {
	t.Helper()
	r := api.NewRouter()
	if err := cmdsurface.MountSigned(b, r, signedTestKey, store, opts...); err != nil {
		t.Fatalf("MountSigned: %v", err)
	}
	srv := httptest.NewServer(r)
	return srv.URL, srv.Close
}

// signedGet visits u with redirects disabled and returns
// status, body, and Location header.
func signedGet(t *testing.T, u string) (int, []byte, string) {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := c.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, buf, resp.Header.Get("Location")
}

// ---- 1. Issue + visit happy path ---------------------------------

func TestSignedSurface_HappyPath(t *testing.T) {
	runner := &signedFakeRunner{
		fn: func(_ context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{Stdout: "ok " + strings.Join(inv.Path, "/")}, nil
		},
	}
	b := signedNewBridge(runner, nil, "auth login")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.IssueViaBridge(context.Background(), b, cmdsurface.SignedToken{
		Path:   []string{"auth", "login"},
		Args:   []string{"--user=alice"},
		Caller: "alice@example.com",
	}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	status, body, _ := signedGet(t, link)
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", status, body)
	}
	var res cmdsurface.Result
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	if res.Stdout != "ok auth/login" {
		t.Errorf("Stdout=%q", res.Stdout)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("runs=%d want 1", len(got))
	}
	if got[0].Meta.Surface != cmdsurface.SurfaceSigned {
		t.Errorf("Surface=%q want signed", got[0].Meta.Surface)
	}
	if got[0].Meta.Caller != "alice@example.com" {
		t.Errorf("Caller=%q", got[0].Meta.Caller)
	}
}

// ---- 2. Visit with success redirect ------------------------------

func TestSignedSurface_SuccessRedirect(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store,
		cmdsurface.WithSignedSuccessRedirect("https://example.test/welcome"),
	)
	defer stop()

	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.Issue(context.Background(), cmdsurface.SignedToken{
		Path: []string{"ping"},
	}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	status, _, loc := signedGet(t, link)
	if status != http.StatusFound {
		t.Fatalf("status=%d want 302", status)
	}
	if loc != "https://example.test/welcome" {
		t.Errorf("Location=%q", loc)
	}
}

// ---- 3. Tampered payload -----------------------------------------

func TestSignedSurface_TamperedPayload(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.Issue(context.Background(), cmdsurface.SignedToken{Path: []string{"ping"}}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Flip a byte in the payload segment of the token.
	tampered := signedFlipByteInTokenSegment(t, link, 0)
	status, body, _ := signedGet(t, tampered)
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401; body=%s", status, body)
	}
	if !signedBodyHasCode(body, "bad_signature") {
		t.Errorf("body=%s want bad_signature", body)
	}
}

// ---- 4. Tampered tag ---------------------------------------------

func TestSignedSurface_TamperedTag(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.Issue(context.Background(), cmdsurface.SignedToken{Path: []string{"ping"}}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	tampered := signedFlipByteInTokenSegment(t, link, 1)
	status, body, _ := signedGet(t, tampered)
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401; body=%s", status, body)
	}
	if !signedBodyHasCode(body, "bad_signature") {
		t.Errorf("body=%s want bad_signature", body)
	}
}

// ---- 5. Malformed (no dot) ---------------------------------------

func TestSignedSurface_MalformedNoDot(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	status, body, _ := signedGet(t, base+"/x/no-dot-here")
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", status, body)
	}
	if !signedBodyHasCode(body, "malformed") {
		t.Errorf("body=%s want malformed", body)
	}
}

// ---- 6. Malformed (bad base64) -----------------------------------

func TestSignedSurface_MalformedBadBase64(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	// "!!!" is not valid base64url.
	status, body, _ := signedGet(t, base+"/x/!!!.!!!")
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", status, body)
	}
	if !signedBodyHasCode(body, "malformed") {
		t.Errorf("body=%s want malformed", body)
	}
}

// ---- 7. Expired ---------------------------------------------------

func TestSignedSurface_Expired(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.Issue(context.Background(), cmdsurface.SignedToken{Path: []string{"ping"}}, time.Millisecond)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	time.Sleep(1100 * time.Millisecond) // exp uses unix seconds; wait > 1s
	status, body, _ := signedGet(t, link)
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401; body=%s", status, body)
	}
	if !signedBodyHasCode(body, "expired") {
		t.Errorf("body=%s want expired", body)
	}
}

// ---- 8. Single-use ------------------------------------------------

func TestSignedSurface_SingleUse(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.Issue(context.Background(), cmdsurface.SignedToken{Path: []string{"ping"}}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if s, _, _ := signedGet(t, link); s != http.StatusOK {
		t.Fatalf("first visit status=%d want 200", s)
	}
	status, body, _ := signedGet(t, link)
	if status != http.StatusUnauthorized {
		t.Fatalf("second visit status=%d want 401; body=%s", status, body)
	}
	if !signedBodyHasCode(body, "nonce_used") {
		t.Errorf("body=%s want nonce_used", body)
	}
}

// ---- 9. Revoked ---------------------------------------------------

func TestSignedSurface_Revoked(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	nonce := "fixed-nonce-revoke-test"
	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.Issue(context.Background(), cmdsurface.SignedToken{
		Path:  []string{"ping"},
		Nonce: nonce,
	}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := store.Revoke(context.Background(), nonce); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	status, body, _ := signedGet(t, link)
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401; body=%s", status, body)
	}
	if !signedBodyHasCode(body, "nonce_used") {
		t.Errorf("body=%s want nonce_used", body)
	}
}

// ---- 10. Issue refuses unknown leaf ------------------------------

func TestSignedSurface_IssueRefusesUnknown(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: "https://example.test/x"}
	_, err := issuer.IssueViaBridge(context.Background(), b, cmdsurface.SignedToken{
		Path: []string{"does", "not", "exist"},
	}, time.Minute)
	if !errors.Is(err, cmdsurface.ErrUnknownCommand) {
		t.Errorf("err=%v want ErrUnknownCommand", err)
	}
}

// ---- 11. Issue refuses surface-not-enabled -----------------------

func TestSignedSurface_IssueRefusesSurfaceNotEnabled(t *testing.T) {
	// Expose ping on SurfaceREST only.
	b := signedNewBridge(&signedFakeRunner{}, []cmdsurface.Surface{cmdsurface.SurfaceREST}, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: "https://example.test/x"}
	_, err := issuer.IssueViaBridge(context.Background(), b, cmdsurface.SignedToken{
		Path: []string{"ping"},
	}, time.Minute)
	if !errors.Is(err, cmdsurface.ErrSurfaceNotEnabled) {
		t.Errorf("err=%v want ErrSurfaceNotEnabled", err)
	}
}

// ---- 12. Issue refuses destructive without opt-in ---------------

func TestSignedSurface_IssueRefusesDestructive(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "subscription cancel")
	store := cmdsurface.NewInMemoryNonceStore()
	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: "https://example.test/x"}
	_, err := issuer.IssueViaBridge(context.Background(), b, cmdsurface.SignedToken{
		Path: []string{"subscription", "cancel"},
	}, time.Minute)
	if !errors.Is(err, cmdsurface.ErrDestructiveBlocked) {
		t.Errorf("err=%v want ErrDestructiveBlocked", err)
	}
}

// ---- 13. Issue OK with destructive opt-in -----------------------

func TestSignedSurface_DestructiveOptInVisit(t *testing.T) {
	runner := &signedFakeRunner{
		fn: func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{Stdout: "canceled"}, nil
		},
	}
	b := signedNewBridgeWithPolicy(runner, cmdsurface.Policy{
		AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceSigned},
		DefaultEnabled:     []cmdsurface.Surface{cmdsurface.SurfaceCLI, cmdsurface.SurfaceLib},
	}, "subscription cancel")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.IssueViaBridge(context.Background(), b, cmdsurface.SignedToken{
		Path: []string{"subscription", "cancel"},
	}, time.Minute)
	if err != nil {
		t.Fatalf("IssueViaBridge: %v", err)
	}
	status, body, _ := signedGet(t, link)
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", status, body)
	}
	var res cmdsurface.Result
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Stdout != "canceled" {
		t.Errorf("Stdout=%q want canceled", res.Stdout)
	}
}

// ---- 14. Error redirect on expired ------------------------------

func TestSignedSurface_ErrorRedirect(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store,
		cmdsurface.WithSignedErrorRedirect("https://example.test/oops"),
	)
	defer stop()

	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.Issue(context.Background(), cmdsurface.SignedToken{Path: []string{"ping"}}, time.Millisecond)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	status, _, loc := signedGet(t, link)
	if status != http.StatusFound {
		t.Fatalf("status=%d want 302", status)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got := u.Query().Get("error"); got != "expired" {
		t.Errorf("error param=%q want expired", got)
	}
}

// ---- 15. No redirect, no JSON (default error page) --------------

func TestSignedSurface_DefaultErrorPage(t *testing.T) {
	// "Plain-text error page" per spec — implementation renders JSON
	// APIError when no redirects are configured (machine-parseable for
	// programmatic clients while still surfacing the code). We assert
	// the response carries the expected code in some form.
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	// Provoke malformed → no redirect should fire.
	status, body, loc := signedGet(t, base+"/x/no-dot-here")
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", status, body)
	}
	if loc != "" {
		t.Errorf("Location=%q want empty (no redirect configured)", loc)
	}
	if !signedBodyHasCode(body, "malformed") {
		t.Errorf("body=%s want malformed", body)
	}
}

// ---- 16. Constant-time signature compare (cross-key reject) -----

func TestSignedSurface_CrossKeyReject(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	// Issue with key A.
	keyA := []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	issuer := &cmdsurface.SignedIssuer{Key: keyA, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.Issue(context.Background(), cmdsurface.SignedToken{Path: []string{"ping"}}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Verifier was mounted with signedTestKey ≠ keyA → 401 bad_signature.
	status, body, _ := signedGet(t, link)
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401; body=%s", status, body)
	}
	if !signedBodyHasCode(body, "bad_signature") {
		t.Errorf("body=%s want bad_signature", body)
	}
}

// ---- 17. Concurrent Consume — exactly one succeeds --------------

func TestSignedSurface_ConcurrentNonce(t *testing.T) {
	var hits atomic.Int32
	runner := &signedFakeRunner{
		fn: func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			hits.Add(1)
			return cmdsurface.Result{Stdout: "once"}, nil
		},
	}
	b := signedNewBridge(runner, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.Issue(context.Background(), cmdsurface.SignedToken{Path: []string{"ping"}}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			s, _, _ := signedGet(t, link)
			statuses[i] = s
		}(i)
	}
	wg.Wait()

	var ok200, ok401, other int
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			ok200++
		case http.StatusUnauthorized:
			ok401++
		default:
			other++
		}
	}
	if ok200 != 1 {
		t.Errorf("ok=%d want exactly 1", ok200)
	}
	if ok401 != n-1 {
		t.Errorf("401=%d want %d", ok401, n-1)
	}
	if other != 0 {
		t.Errorf("other status counts=%d", other)
	}
	if hits.Load() != 1 {
		t.Errorf("runner hits=%d want 1", hits.Load())
	}
}

// ---- 18. Meta.Surface + Caller forced ---------------------------

func TestSignedSurface_MetaForced(t *testing.T) {
	runner := &signedFakeRunner{}
	b := signedNewBridge(runner, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.Issue(context.Background(), cmdsurface.SignedToken{
		Path:   []string{"ping"},
		Caller: "u-7",
	}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if s, _, _ := signedGet(t, link); s != http.StatusOK {
		t.Fatalf("status=%d want 200", s)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if got[0].Meta.Surface != cmdsurface.SurfaceSigned {
		t.Errorf("Surface=%q want signed", got[0].Meta.Surface)
	}
	if got[0].Meta.Caller != "u-7" {
		t.Errorf("Caller=%q want u-7", got[0].Meta.Caller)
	}
}

// ---- 19. Nonce omitted → generated ------------------------------

func TestSignedSurface_NonceGenerated(t *testing.T) {
	b := signedNewBridge(&signedFakeRunner{}, nil, "ping")
	store := cmdsurface.NewInMemoryNonceStore()
	base, stop := signedMount(t, b, store)
	defer stop()

	issuer := &cmdsurface.SignedIssuer{Key: signedTestKey, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.Issue(context.Background(), cmdsurface.SignedToken{
		Path:  []string{"ping"},
		Nonce: "", // explicit empty
	}, time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Decode the issued token to inspect the generated nonce.
	tok := signedExtractToken(t, link)
	payload, err := base64.RawURLEncoding.DecodeString(strings.SplitN(tok, ".", 2)[0])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var st cmdsurface.SignedToken
	if err := json.Unmarshal(payload, &st); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if st.Nonce == "" {
		t.Fatal("Nonce remained empty after Issue")
	}
	if len(st.Nonce) < 16 {
		t.Errorf("Nonce=%q too short (want ≥16 base64url chars)", st.Nonce)
	}
	// Visit → store records the same nonce.
	if s, _, _ := signedGet(t, link); s != http.StatusOK {
		t.Fatalf("visit status=%d want 200", s)
	}
	// Second visit refused → Consume saw the same nonce.
	if s, _, _ := signedGet(t, link); s != http.StatusUnauthorized {
		t.Errorf("second visit status=%d want 401", s)
	}
}

// ----- helpers ---------------------------------------------------

// signedBodyHasCode is true when body decodes to an APIError whose
// Code matches want.
func signedBodyHasCode(body []byte, want string) bool {
	var ae api.APIError
	if err := json.Unmarshal(body, &ae); err != nil {
		return false
	}
	return ae.Code == want
}

// signedExtractToken returns the trailing path segment of u (the token).
func signedExtractToken(t *testing.T, u string) string {
	t.Helper()
	idx := strings.LastIndex(u, "/")
	if idx < 0 {
		t.Fatalf("no slash in URL %q", u)
	}
	return u[idx+1:]
}

// signedFlipByteInTokenSegment flips a byte in the payload (seg=0)
// or tag (seg=1) of the token portion of u and returns the modified
// URL. The byte is flipped after base64-decoding and re-encoded so
// the resulting URL parses cleanly but mismatches.
func signedFlipByteInTokenSegment(t *testing.T, u string, seg int) string {
	t.Helper()
	tok := signedExtractToken(t, u)
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("token has no dot: %q", tok)
	}
	dec, err := base64.RawURLEncoding.DecodeString(parts[seg])
	if err != nil {
		t.Fatalf("decode seg %d: %v", seg, err)
	}
	if len(dec) == 0 {
		t.Fatalf("seg %d empty", seg)
	}
	dec[0] ^= 0x01
	parts[seg] = base64.RawURLEncoding.EncodeToString(dec)
	return u[:strings.LastIndex(u, "/")+1] + parts[0] + "." + parts[1]
}
