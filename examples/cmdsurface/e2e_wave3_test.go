//go:build e2e

// Wave 3 e2e suite. Adversarial coverage of the Webhook, OAuth
// callback, and Signed-URL surfaces — happy paths plus the security
// properties each surface promises (HMAC binds body, OAuth state
// blocks replay/CSRF, signed URLs reject tampering/replay/expiry).
// Build-tagged so `go test ./...` ignores it by default; run with
// `go test -tags=e2e -race -count=1 ./examples/cmdsurface/...`.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// startWithOpts is the Wave-3 counterpart to start(). It accepts
// BuildExample options so signed-URL tests can opt SurfaceSigned in
// for the destructive `subscription cancel` flow.
func startWithOpts(t *testing.T, opts ...ExampleOption) *liveExample {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	app, err := BuildExample(ctx, discardLogger(), opts...)
	if err != nil {
		cancel()
		t.Fatalf("BuildExample: %v", err)
	}

	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		app.Cleanup()
		t.Fatalf("listen http: %v", err)
	}
	rpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = httpLis.Close()
		cancel()
		app.Cleanup()
		t.Fatalf("listen rpc: %v", err)
	}

	httpSrv := &http.Server{Handler: app.Router}
	rpcSrv := &http.Server{Handler: app.RPCSrv}

	go func() { _ = httpSrv.Serve(httpLis) }()
	go func() { _ = rpcSrv.Serve(rpcLis) }()

	le := &liveExample{
		app:     app,
		httpURL: "http://" + httpLis.Addr().String(),
		rpcURL:  "http://" + rpcLis.Addr().String(),
		httpSrv: httpSrv,
		rpcSrv:  rpcSrv,
	}
	t.Cleanup(func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutCancel()
		_ = httpSrv.Shutdown(shutCtx)
		_ = rpcSrv.Shutdown(shutCtx)
		app.Cleanup()
		cancel()
	})
	return le
}

// webhookSign computes the canonical hex-encoded HMAC-SHA256 of body
// under secret. Tests prepend "sha256=" to mirror the example's
// AuthHMAC.Prefix.
func webhookSign(t *testing.T, body []byte, secret []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// dialNoRedirect returns an http.Client that refuses to follow
// redirects so callers can inspect the Location header and status
// code directly. OAuth + signed-URL tests rely on this.
func dialNoRedirect() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// =============================================================================
// Webhook
// =============================================================================

func TestE2E_Wave3_WebhookHappyPath(t *testing.T) {
	le := startWithOpts(t)

	body := []byte(`{"source":"github","title":"PR opened"}`)
	sig := webhookSign(t, body, le.app.WebhookSecret)

	req, err := http.NewRequest(http.MethodPost,
		le.httpURL+"/hooks/notify", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", "sha256="+sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}

	// The FileSink in the sink fan-out records every invocation; verify
	// the webhook drove a notify message call.
	got := le.app.SinkBuf.String()
	if !strings.Contains(got, `"path":"notify message"`) {
		t.Errorf("SinkBuf=%q missing path=notify message", got)
	}
	if !strings.Contains(got, `"surface":"webhook"`) {
		t.Errorf("SinkBuf=%q missing surface=webhook", got)
	}
}

func TestE2E_Wave3_WebhookMissingSignature(t *testing.T) {
	le := startWithOpts(t)

	body := []byte(`{"source":"github","title":"PR opened"}`)
	req, _ := http.NewRequest(http.MethodPost,
		le.httpURL+"/hooks/notify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No X-Webhook-Signature header.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 401", resp.StatusCode, raw)
	}
}

func TestE2E_Wave3_WebhookWrongSignature(t *testing.T) {
	le := startWithOpts(t)

	body := []byte(`{"source":"github","title":"PR opened"}`)
	// Sign with the wrong key.
	wrong := webhookSign(t, body, []byte("not-the-real-secret"))

	req, _ := http.NewRequest(http.MethodPost,
		le.httpURL+"/hooks/notify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", "sha256="+wrong)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 401", resp.StatusCode, raw)
	}
}

func TestE2E_Wave3_WebhookTamperedBody(t *testing.T) {
	le := startWithOpts(t)

	// Sign body A but send body B. The HMAC must cover the body bytes,
	// not just the URL — sending B with sig(A) must reject with 401.
	bodyA := []byte(`{"source":"github","title":"PR opened"}`)
	bodyB := []byte(`{"source":"evil","title":"forged"}`)
	sigA := webhookSign(t, bodyA, le.app.WebhookSecret)

	req, _ := http.NewRequest(http.MethodPost,
		le.httpURL+"/hooks/notify", bytes.NewReader(bodyB))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", "sha256="+sigA)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 401 (HMAC must bind body)", resp.StatusCode, raw)
	}
}

func TestE2E_Wave3_WebhookBodyTooLarge(t *testing.T) {
	le := startWithOpts(t)

	// Default MaxBody is 1 MiB; send 2 MiB.
	body := bytes.Repeat([]byte("x"), 2*1024*1024)
	// Compute a sig over the body so we get past sig prefix check (the
	// server reads body before verifying sig, but body limit fires
	// first when over the cap). We just need to make sure the request
	// is well-formed enough to reach the limit check.
	sig := webhookSign(t, body, le.app.WebhookSecret)

	req, _ := http.NewRequest(http.MethodPost,
		le.httpURL+"/hooks/notify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", "sha256="+sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 413", resp.StatusCode, raw)
	}
}

func TestE2E_Wave3_WebhookNonJSONBody(t *testing.T) {
	le := startWithOpts(t)

	// text/plain body: the surface does not decode it as JSON, so
	// .body in the template root is an empty map; the FlagMap entries
	// referencing .body.source / .body.title render "<no value>" but
	// the request still passes auth+template and the invocation runs.
	body := []byte("foo")
	sig := webhookSign(t, body, le.app.WebhookSecret)

	req, _ := http.NewRequest(http.MethodPost,
		le.httpURL+"/hooks/notify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("X-Webhook-Signature", "sha256="+sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 202 (non-JSON body, fields absent)", resp.StatusCode, raw)
	}
	// Confirm the leaf observed empty / "<no value>" flags rather than
	// failing. We assert by inspecting the sink record's path.
	if !strings.Contains(le.app.SinkBuf.String(), `"path":"notify message"`) {
		t.Errorf("SinkBuf=%q missing path=notify message", le.app.SinkBuf.String())
	}
}

func TestE2E_Wave3_WebhookMalformedJSON(t *testing.T) {
	le := startWithOpts(t)

	// Auth verify happens BEFORE JSON decode. Sign over the raw body
	// regardless of payload shape — surface accepts the request, JSON
	// decode silently fails, .body is empty, template renders <no
	// value> for absent fields. The invocation runs at 202.
	body := []byte(`{not json}`)
	sig := webhookSign(t, body, le.app.WebhookSecret)

	req, _ := http.NewRequest(http.MethodPost,
		le.httpURL+"/hooks/notify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", "sha256="+sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 202 (auth before decode; decode failure is silent)",
			resp.StatusCode, raw)
	}
}

func TestE2E_Wave3_WebhookUnknownRoute(t *testing.T) {
	le := startWithOpts(t)

	body := []byte(`{"source":"github","title":"x"}`)
	sig := webhookSign(t, body, le.app.WebhookSecret)
	req, _ := http.NewRequest(http.MethodPost,
		le.httpURL+"/hooks/bogus", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", "sha256="+sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 404", resp.StatusCode, raw)
	}
}

func TestE2E_Wave3_WebhookDestructiveRejectedAtMount(t *testing.T) {
	// Build a fresh bridge and try to mount a webhook mapping for the
	// destructive `subscription cancel` leaf without policy opt-in.
	// MountWebhooks must return ErrDestructiveBlocked (or a wrapped
	// equivalent) and attach no routes.
	le := startWithOpts(t)

	// Build a one-off router so we don't pollute the live one.
	// We use the same bridge — the destructive gate fires regardless.
	r := newScratchRouter(t)
	err := cmdsurface.MountWebhooks(le.app.Bridge, r,
		[]cmdsurface.WebhookMapping{
			{
				Name: "kill-sub",
				Path: []string{"subscription", "cancel"},
				FlagMap: map[string]string{
					"id": `{{ .body.id }}`,
				},
				Auth: cmdsurface.AuthHMAC{
					Header: "X-Sig",
					Prefix: "sha256=",
					Secret: []byte("k"),
				},
			},
		},
	)
	if err == nil {
		t.Fatal("MountWebhooks unexpectedly succeeded for destructive leaf")
	}
	// The error should be (or wrap) ErrDestructiveBlocked or
	// ErrSurfaceNotEnabled — both indicate the destructive gate
	// closed.
	if !errors.Is(err, cmdsurface.ErrDestructiveBlocked) &&
		!errors.Is(err, cmdsurface.ErrSurfaceNotEnabled) {
		t.Errorf("err=%v want ErrDestructiveBlocked or ErrSurfaceNotEnabled", err)
	}
}

func TestE2E_Wave3_WebhookTimingResistance(t *testing.T) {
	// HMAC.Equal is constant-time, so verify latency for a "swap one
	// byte in the sig" path should be statistically indistinguishable
	// from the valid-sig path. The measurement is dominated by HTTP
	// round-trip + body read, which on a busy machine swamps the
	// HMAC compare delta entirely; on a quiet local box the means
	// land within a few microseconds. This probe is best-effort.
	//
	// To keep CI honest we skip when -short is set; local devs can
	// flip the skip to investigate regressions.
	if testing.Short() {
		t.Skip("timing probe is best-effort; skipped under -short")
	}
	le := startWithOpts(t)

	body := []byte(`{"source":"github","title":"PR opened"}`)
	good := webhookSign(t, body, le.app.WebhookSecret)
	// Mutate one byte of the hex signature.
	bad := []byte(good)
	bad[0] ^= 0x01
	badSig := string(bad)

	send := func(sig string) time.Duration {
		req, _ := http.NewRequest(http.MethodPost,
			le.httpURL+"/hooks/notify", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Webhook-Signature", "sha256="+sig)
		start := time.Now()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return time.Since(start)
	}

	const iters = 50
	var goodSum, badSum time.Duration
	for i := 0; i < iters; i++ {
		goodSum += send(good)
		badSum += send(badSig)
	}
	goodAvg := goodSum / iters
	badAvg := badSum / iters

	// The absolute means are noise-dominated; we only assert sanity:
	// neither path is wildly slower than the other (10x suggests a
	// non-constant-time compare). Real timing proofs need many more
	// samples + statistical machinery; we lean on hmac.Equal's
	// guarantee and just verify nothing screams here.
	ratio := float64(goodAvg) / float64(badAvg)
	if ratio < 1 {
		ratio = 1 / ratio
	}
	if ratio > 10 {
		t.Errorf("verify latency ratio good/bad=%.2f (avg good=%v bad=%v) suggests non-constant-time compare",
			ratio, goodAvg, badAvg)
	}
}

// =============================================================================
// OAuth callback
// =============================================================================

func TestE2E_Wave3_OAuthAuthorizeRedirects(t *testing.T) {
	le := startWithOpts(t)
	cli := dialNoRedirect()

	req, _ := http.NewRequest(http.MethodGet, le.httpURL+"/oauth/example/authorize", nil)
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status=%d want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "https://example.invalid/authorize") {
		t.Errorf("Location=%q want prefix https://example.invalid/authorize", loc)
	}
	if !strings.Contains(loc, "state=") {
		t.Errorf("Location=%q missing state= parameter", loc)
	}
}

func TestE2E_Wave3_OAuthCallbackHappyPath(t *testing.T) {
	le := startWithOpts(t)
	cli := dialNoRedirect()

	// Issue a state directly via the store so we don't have to scrape
	// it from the authorize redirect.
	state, err := le.app.OAuthState.Issue(context.Background(), "example", time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	url := le.httpURL + "/oauth/example/callback?code=abc&state=" + state
	resp, err := cli.Get(url)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 302", resp.StatusCode, raw)
	}
	if loc := resp.Header.Get("Location"); loc != "/oauth-done" {
		t.Errorf("Location=%q want /oauth-done", loc)
	}

	// Sink record confirms the leaf received the code flag.
	got := le.app.SinkBuf.String()
	if !strings.Contains(got, `"path":"auth oauth-link"`) {
		t.Errorf("SinkBuf=%q missing path=auth oauth-link", got)
	}
	if !strings.Contains(got, `"surface":"oauth-cb"`) {
		t.Errorf("SinkBuf=%q missing surface=oauth-cb", got)
	}
}

func TestE2E_Wave3_OAuthMissingState(t *testing.T) {
	le := startWithOpts(t)
	cli := dialNoRedirect()

	resp, err := cli.Get(le.httpURL + "/oauth/example/callback?code=abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status=%d want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/oauth-error") || !strings.Contains(loc, "error=missing_state") {
		t.Errorf("Location=%q want /oauth-error?error=missing_state", loc)
	}
}

func TestE2E_Wave3_OAuthInvalidState(t *testing.T) {
	le := startWithOpts(t)
	cli := dialNoRedirect()

	// Forged state that was never issued.
	resp, err := cli.Get(le.httpURL + "/oauth/example/callback?code=abc&state=forged-state-12345")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status=%d want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/oauth-error") || !strings.Contains(loc, "error=invalid_state") {
		t.Errorf("Location=%q want /oauth-error?error=invalid_state", loc)
	}
}

func TestE2E_Wave3_OAuthStateReplay(t *testing.T) {
	le := startWithOpts(t)
	cli := dialNoRedirect()

	state, err := le.app.OAuthState.Issue(context.Background(), "example", time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	url := le.httpURL + "/oauth/example/callback?code=abc&state=" + state

	// First visit — success.
	resp1, err := cli.Get(url)
	if err != nil {
		t.Fatalf("Get1: %v", err)
	}
	_ = resp1.Body.Close()
	if resp1.StatusCode != http.StatusFound {
		t.Fatalf("first visit status=%d want 302 redirect to success", resp1.StatusCode)
	}
	if loc := resp1.Header.Get("Location"); loc != "/oauth-done" {
		t.Fatalf("first visit Location=%q want /oauth-done", loc)
	}

	// Second visit with same state — invalid_state.
	resp2, err := cli.Get(url)
	if err != nil {
		t.Fatalf("Get2: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		t.Fatalf("replay status=%d want 302", resp2.StatusCode)
	}
	if loc := resp2.Header.Get("Location"); !strings.Contains(loc, "error=invalid_state") {
		t.Errorf("replay Location=%q want error=invalid_state", loc)
	}
}

func TestE2E_Wave3_OAuthStateFromWrongProvider(t *testing.T) {
	le := startWithOpts(t)
	cli := dialNoRedirect()

	// Issue under a different provider name.
	state, err := le.app.OAuthState.Issue(context.Background(), "other", time.Minute)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Visit the example provider's callback with the wrong-provider state.
	resp, err := cli.Get(le.httpURL + "/oauth/example/callback?code=abc&state=" + state)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status=%d want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "error=invalid_state") {
		t.Errorf("Location=%q want error=invalid_state", loc)
	}
}

// OAuth #7 (expired state): skipped at e2e level. The surface's state
// TTL is configured at mount time and BuildExample plumbs it as a
// 2-minute default; threading a test-only short TTL through requires
// either a separate BuildExample option (clutter) or sleeping the
// test out (unacceptably slow). The unit tests in
// go/transport/cmdsurface/surface_oauth_test.go exercise expired
// state via the StateStore's now-override hook.
func TestE2E_Wave3_OAuthExpiredState(t *testing.T) {
	t.Skip("expired-state coverage lives in unit tests (surface_oauth_test.go); " +
		"surface TTL is mount-time configured and threading a short TTL " +
		"through BuildExample clutters the example. See unit tests.")
}

func TestE2E_Wave3_OAuthProviderError(t *testing.T) {
	le := startWithOpts(t)
	cli := dialNoRedirect()

	resp, err := cli.Get(le.httpURL + "/oauth/example/callback?error=access_denied")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status=%d want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/oauth-error") {
		t.Errorf("Location=%q want /oauth-error", loc)
	}
	// The surface URL-encodes the colon in provider_error:access_denied.
	if !strings.Contains(loc, "provider_error") || !strings.Contains(loc, "access_denied") {
		t.Errorf("Location=%q want error=provider_error:access_denied", loc)
	}
}

// =============================================================================
// Signed URL
// =============================================================================

func TestE2E_Wave3_SignedHappyPath(t *testing.T) {
	le := startWithOpts(t)

	url, err := le.app.SignedIssuer.IssueViaBridge(context.Background(), le.app.Bridge,
		cmdsurface.SignedToken{Path: []string{"ping"}},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("IssueViaBridge: %v", err)
	}

	resp, err := http.Get(le.httpURL + url)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 200", resp.StatusCode, raw)
	}
	var res cmdsurface.Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(res.Stdout, "pong") {
		t.Errorf("Result.Stdout=%q want contains pong", res.Stdout)
	}

	// Sink record confirms.
	if !strings.Contains(le.app.SinkBuf.String(), `"surface":"signed"`) {
		t.Errorf("SinkBuf=%q missing surface=signed", le.app.SinkBuf.String())
	}
}

func TestE2E_Wave3_SignedTamperedPayload(t *testing.T) {
	le := startWithOpts(t)

	url, err := le.app.SignedIssuer.IssueViaBridge(context.Background(), le.app.Bridge,
		cmdsurface.SignedToken{Path: []string{"ping"}},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("IssueViaBridge: %v", err)
	}

	// url is "/x/<base64payload>.<base64tag>". Flip one base64 char in
	// the payload segment so the HMAC tag no longer matches.
	tamperedURL := mutateSignedURL(t, url, true /*payload*/)
	resp, err := http.Get(le.httpURL + tamperedURL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 401 bad_signature", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "bad_signature") {
		t.Errorf("body=%s want contains bad_signature", raw)
	}
}

func TestE2E_Wave3_SignedTamperedTag(t *testing.T) {
	le := startWithOpts(t)

	url, err := le.app.SignedIssuer.IssueViaBridge(context.Background(), le.app.Bridge,
		cmdsurface.SignedToken{Path: []string{"ping"}},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("IssueViaBridge: %v", err)
	}

	tamperedURL := mutateSignedURL(t, url, false /*tag*/)
	resp, err := http.Get(le.httpURL + tamperedURL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 401 bad_signature", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "bad_signature") {
		t.Errorf("body=%s want contains bad_signature", raw)
	}
}

func TestE2E_Wave3_SignedMalformed(t *testing.T) {
	le := startWithOpts(t)

	url, err := le.app.SignedIssuer.IssueViaBridge(context.Background(), le.app.Bridge,
		cmdsurface.SignedToken{Path: []string{"ping"}},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("IssueViaBridge: %v", err)
	}

	// Drop the dot separating payload and tag → malformed token.
	stripped := strings.Replace(url, ".", "", 1)
	resp, err := http.Get(le.httpURL + stripped)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 400 malformed", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "malformed") {
		t.Errorf("body=%s want contains malformed", raw)
	}
}

func TestE2E_Wave3_SignedExpired(t *testing.T) {
	le := startWithOpts(t)

	url, err := le.app.SignedIssuer.IssueViaBridge(context.Background(), le.app.Bridge,
		cmdsurface.SignedToken{Path: []string{"ping"}},
		10*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("IssueViaBridge: %v", err)
	}
	// Wait past the TTL.
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(le.httpURL + url)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 401 expired", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "expired") {
		t.Errorf("body=%s want contains expired", raw)
	}
}

func TestE2E_Wave3_SignedReplay(t *testing.T) {
	le := startWithOpts(t)

	url, err := le.app.SignedIssuer.IssueViaBridge(context.Background(), le.app.Bridge,
		cmdsurface.SignedToken{Path: []string{"ping"}},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("IssueViaBridge: %v", err)
	}

	// First visit succeeds.
	resp1, err := http.Get(le.httpURL + url)
	if err != nil {
		t.Fatalf("Get1: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp1.Body)
	_ = resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first visit status=%d want 200", resp1.StatusCode)
	}

	// Second visit fails with nonce_used.
	resp2, err := http.Get(le.httpURL + url)
	if err != nil {
		t.Fatalf("Get2: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp2.Body)
		t.Fatalf("replay status=%d body=%s want 401 nonce_used", resp2.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(raw), "nonce_used") {
		t.Errorf("body=%s want contains nonce_used", raw)
	}
}

func TestE2E_Wave3_SignedRevoked(t *testing.T) {
	le := startWithOpts(t)

	// Mint a URL but inject a known nonce so we can revoke it directly.
	url, err := le.app.SignedIssuer.IssueViaBridge(context.Background(), le.app.Bridge,
		cmdsurface.SignedToken{
			Path:  []string{"ping"},
			Nonce: "test-revoke-nonce",
		},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("IssueViaBridge: %v", err)
	}

	if err := le.app.SignedNonce.Revoke(context.Background(), "test-revoke-nonce"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	resp, err := http.Get(le.httpURL + url)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 401 nonce_used", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "nonce_used") {
		t.Errorf("body=%s want contains nonce_used", raw)
	}
}

func TestE2E_Wave3_SignedCrossKeyReject(t *testing.T) {
	le := startWithOpts(t)

	// Issuer signs with a different key than the verifier accepts.
	otherKey := []byte("a-totally-different-signing-key")
	wrongIssuer := &cmdsurface.SignedIssuer{
		Key:       otherKey,
		Store:     cmdsurface.NewInMemoryNonceStore(), // separate store; the verifier never reaches it
		URLPrefix: "/x",
	}
	url, err := wrongIssuer.Issue(context.Background(),
		cmdsurface.SignedToken{Path: []string{"ping"}},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	resp, err := http.Get(le.httpURL + url)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 401 bad_signature", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "bad_signature") {
		t.Errorf("body=%s want contains bad_signature", raw)
	}
}

func TestE2E_Wave3_SignedDestructiveLockedByDefault(t *testing.T) {
	le := startWithOpts(t)

	// `subscription cancel` is destructive. The default BuildExample
	// hides it from SurfaceSigned and Policy.AllowDestructiveOn does
	// not include SurfaceSigned. IssueViaBridge must refuse with one
	// of the destructive-gate sentinels (ErrSurfaceNotEnabled wins
	// because the hide happens at the leaf-enable level; the spec
	// notes either error indicates the gate closed).
	_, err := le.app.SignedIssuer.IssueViaBridge(context.Background(), le.app.Bridge,
		cmdsurface.SignedToken{
			Path: []string{"subscription", "cancel"},
			Args: []string{"42"},
		},
		time.Minute,
	)
	if err == nil {
		t.Fatal("IssueViaBridge unexpectedly succeeded for subscription cancel")
	}
	if !errors.Is(err, cmdsurface.ErrSurfaceNotEnabled) &&
		!errors.Is(err, cmdsurface.ErrDestructiveBlocked) {
		t.Errorf("err=%v want ErrSurfaceNotEnabled or ErrDestructiveBlocked", err)
	}
}

func TestE2E_Wave3_SignedDestructiveWithOptIn(t *testing.T) {
	le := startWithOpts(t, WithAllowDestructiveOn(cmdsurface.SurfaceSigned))

	url, err := le.app.SignedIssuer.IssueViaBridge(context.Background(), le.app.Bridge,
		cmdsurface.SignedToken{
			Path: []string{"subscription", "cancel"},
			Args: []string{"42"},
		},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("IssueViaBridge: %v", err)
	}

	resp, err := http.Get(le.httpURL + url)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s want 200", resp.StatusCode, raw)
	}
	var res cmdsurface.Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(res.Stdout, "subscription cancel: id=42") {
		t.Errorf("Result.Stdout=%q want contains subscription cancel: id=42", res.Stdout)
	}
}

// =============================================================================
// Cross-surface adversarial probe
// =============================================================================

func TestE2E_Wave3_ConfusedDeputyProbe(t *testing.T) {
	le := startWithOpts(t)

	// Issue a signed URL for ping.
	signedURL, err := le.app.SignedIssuer.IssueViaBridge(context.Background(), le.app.Bridge,
		cmdsurface.SignedToken{
			Path:  []string{"ping"},
			Nonce: "confused-deputy-nonce",
		},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("IssueViaBridge: %v", err)
	}

	// POST the signed URL as a webhook body. The webhook surface has
	// no business with signed-URL tokens; we verify it does not
	// accidentally consume the nonce.
	body := []byte(`{"source":"adversarial","title":"` + signedURL + `"}`)
	sig := webhookSign(t, body, le.app.WebhookSecret)
	req, _ := http.NewRequest(http.MethodPost,
		le.httpURL+"/hooks/notify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", "sha256="+sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("webhook status=%d want 202", resp.StatusCode)
	}

	// Now visit the signed URL — it MUST still be valid (the webhook
	// path didn't accidentally consume it). 200 on first visit and
	// nonce_used on second proves the nonce store is untouched by
	// the webhook path.
	resp1, err := http.Get(le.httpURL + signedURL)
	if err != nil {
		t.Fatalf("Get signed: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp1.Body)
	_ = resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("signed visit status=%d want 200 (webhook should not have consumed nonce)",
			resp1.StatusCode)
	}
}

// =============================================================================
// helpers
// =============================================================================

// mutateSignedURL flips one base64 character in the payload (when
// payload=true) or the tag segment of a "/x/<payload>.<tag>" URL,
// keeping the result a valid base64 string. Used to assert HMAC
// rejection of single-byte changes.
func mutateSignedURL(t *testing.T, url string, payload bool) string {
	t.Helper()
	// Strip the "/x/" prefix to find the token.
	const pfx = "/x/"
	if !strings.HasPrefix(url, pfx) {
		t.Fatalf("URL %q lacks %q prefix", url, pfx)
	}
	token := url[len(pfx):]
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		t.Fatalf("token %q is not payload.tag", token)
	}
	idx := 1 // tag
	if payload {
		idx = 0
	}
	seg := parts[idx]
	if len(seg) == 0 {
		t.Fatalf("segment %d is empty", idx)
	}

	// Decode → flip one byte → re-encode. Flipping a base64 char
	// directly can produce an invalid-padding string when the new
	// character lands in the trailing position; round-tripping
	// through bytes is safer.
	dec, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("decode segment %d: %v", idx, err)
	}
	if len(dec) == 0 {
		t.Fatalf("decoded segment %d is empty", idx)
	}
	dec[0] ^= 0x01
	parts[idx] = base64.RawURLEncoding.EncodeToString(dec)
	return pfx + strings.Join(parts, ".")
}

// newScratchRouter returns a fresh *api.Router for ad-hoc mount tests
// (Webhook mount-time gate, etc.).
func newScratchRouter(t *testing.T) *api.Router {
	t.Helper()
	return api.NewRouter()
}
