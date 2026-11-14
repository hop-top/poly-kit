package cmdsurface_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/transport/cmdsurface"
)

// startLoopback runs LoopbackPKCE in the background and returns the upstream
// authorization URL handed to OnReady plus a result channel.
type loopbackRun struct {
	authURL string
	done    chan struct {
		res cmdsurface.PKCEResult
		err error
	}
}

func startLoopback(t *testing.T, f *fakeAS, timeout time.Duration) *loopbackRun {
	t.Helper()
	run := &loopbackRun{done: make(chan struct {
		res cmdsurface.PKCEResult
		err error
	}, 1)}
	ready := make(chan string, 1)
	go func() {
		res, err := cmdsurface.LoopbackPKCE(context.Background(), f.provider(nil), cmdsurface.LoopbackOptions{
			Timeout: timeout,
			OnReady: func(u string) { ready <- u },
		})
		run.done <- struct {
			res cmdsurface.PKCEResult
			err error
		}{res, err}
	}()
	select {
	case run.authURL = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("OnReady not called")
	}
	return run
}

func (r *loopbackRun) callbackBase(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(r.authURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("redirect_uri")
}

func (r *loopbackRun) state(t *testing.T) string {
	t.Helper()
	u, _ := url.Parse(r.authURL)
	return u.Query().Get("state")
}

func (r *loopbackRun) assertWaiting(t *testing.T) {
	t.Helper()
	select {
	case o := <-r.done:
		t.Fatalf("login ended early: err=%v", o.err)
	case <-time.After(150 * time.Millisecond):
	}
}

func (r *loopbackRun) finish(t *testing.T) (cmdsurface.PKCEResult, error) {
	t.Helper()
	select {
	case o := <-r.done:
		return o.res, o.err
	case <-time.After(5 * time.Second):
		t.Fatal("login did not finish")
	}
	return cmdsurface.PKCEResult{}, nil
}

func TestPKCE_LoopbackReadyURLIsUpstream(t *testing.T) {
	f := newFakeAS(t)
	run := startLoopback(t, f, 300*time.Millisecond)
	if !strings.HasPrefix(run.authURL, f.srv.URL+"/authorize?") {
		t.Fatalf("OnReady url not upstream: %s", run.authURL)
	}
	u, _ := url.Parse(run.authURL)
	q := u.Query()
	if q.Get("state") == "" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 {
		t.Fatalf("query=%v", q)
	}
	_, _ = run.finish(t)
}

// Attacker-minted state: no local route issues states, and a state not
// issued by the loopback flow cannot complete the login.
func TestPKCE_LoopbackRejectsAttackerMintedState(t *testing.T) {
	f := newFakeAS(t)
	run := startLoopback(t, f, 600*time.Millisecond)
	cb := run.callbackBase(t)
	base := strings.TrimSuffix(cb, "/oauth/mail/callback")

	// Generic authorize route must not exist on the loopback server.
	if st, _ := browse(t, base+"/oauth/mail/authorize"); st != http.StatusNotFound && st != http.StatusMethodNotAllowed {
		t.Errorf("authorize on loopback status=%d", st)
	}
	// Attacker's own consent result with a self-chosen state.
	st, _ := browse(t, cb+"?code=attacker-code&state=attacker-state&iss="+url.QueryEscape(f.srv.URL))
	if st != http.StatusBadRequest {
		t.Errorf("foreign state status=%d", st)
	}
	run.assertWaiting(t)
	_, err := run.finish(t)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v want deadline", err)
	}
	if f.tokenCalls != 0 {
		t.Errorf("token endpoint called %d times", f.tokenCalls)
	}
}

func TestPKCE_LoopbackForeignStateThenGenuineSucceeds(t *testing.T) {
	f := newFakeAS(t)
	run := startLoopback(t, f, 5*time.Second)
	cb := run.callbackBase(t)
	if st, _ := browse(t, cb+"?code=x&state=foreign&iss="+url.QueryEscape(f.srv.URL)); st != http.StatusBadRequest {
		t.Fatalf("foreign status=%d", st)
	}
	run.assertWaiting(t)
	if st, _ := browse(t, run.authURL); st != http.StatusOK {
		t.Fatalf("genuine status=%d", st)
	}
	res, err := run.finish(t)
	if err != nil || res.Token.AccessToken != "at-1" {
		t.Fatalf("res=%v err=%v", res.Token.AccessToken, err)
	}
}

func TestPKCE_LoopbackForgedErrorDoesNotAbort(t *testing.T) {
	f := newFakeAS(t)
	run := startLoopback(t, f, 5*time.Second)
	cb := run.callbackBase(t)
	for _, q := range []string{
		"?error=access_denied",
		"?error=access_denied&state=bogus",
		"?iss=" + url.QueryEscape("https://evil.example"),
		"?iss=" + url.QueryEscape("https://evil.example") + "&state=bogus",
	} {
		if st, _ := browse(t, cb+q); st != http.StatusBadRequest {
			t.Errorf("%s status=%d", q, st)
		}
		run.assertWaiting(t)
	}
	// Forged requests must not have consumed the genuine state.
	if st, _ := browse(t, run.authURL); st != http.StatusOK {
		t.Fatalf("genuine status=%d", st)
	}
	if _, err := run.finish(t); err != nil {
		t.Fatal(err)
	}
}

func TestPKCE_LoopbackErrorWithValidStateIsFatal(t *testing.T) {
	f := newFakeAS(t)
	run := startLoopback(t, f, 5*time.Second)
	st, _ := browse(t, run.callbackBase(t)+"?error=access_denied&iss="+url.QueryEscape(f.srv.URL)+"&state="+url.QueryEscape(run.state(t)))
	if st != http.StatusBadRequest {
		t.Fatalf("status=%d", st)
	}
	if _, err := run.finish(t); !errors.Is(err, cmdsurface.ErrOAuthDenied) {
		t.Fatalf("err=%v", err)
	}
}

func TestPKCE_LoopbackIssuerMismatchValidStateIsFatal(t *testing.T) {
	f := newFakeAS(t)
	run := startLoopback(t, f, 5*time.Second)
	st, _ := browse(t, run.callbackBase(t)+"?code=c&iss="+url.QueryEscape("https://evil.example")+
		"&state="+url.QueryEscape(run.state(t)))
	if st != http.StatusBadRequest {
		t.Fatalf("status=%d", st)
	}
	if _, err := run.finish(t); err == nil || errors.Is(err, cmdsurface.ErrOAuthDenied) {
		t.Fatalf("err=%v want issuer mismatch", err)
	}
	if f.tokenCalls != 0 {
		t.Error("token endpoint called")
	}
}
