package webhook_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/kv/memory"
	"hop.top/kit/go/transport/webhook"
)

type slowPub struct {
	delay time.Duration
	done  atomic.Int32
}

func (p *slowPub) Publish(context.Context, string, string, any) error {
	time.Sleep(p.delay)
	p.done.Add(1)
	return nil
}

func newBasic(t *testing.T, opts ...webhook.Option) *webhook.Receiver {
	t.Helper()
	base := []webhook.Option{webhook.WithVerifier(webhook.BasicAuth("u", "p"))}
	rc, err := webhook.New("t", append(base, opts...)...)
	require.NoError(t, err)
	return rc
}

func post(rc *webhook.Receiver, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.SetBasicAuth("u", "p")
	w := httptest.NewRecorder()
	rc.ServeHTTP(w, req)
	return w
}

func TestNewRefusesAnOpenOrMutePReceiver(t *testing.T) {
	_, err := webhook.New("x", webhook.WithPublisher(&recorder{}))
	assert.ErrorContains(t, err, "verifier")
	_, err = webhook.New("x", webhook.WithVerifier(webhook.BasicAuth("u", "p")))
	assert.ErrorContains(t, err, "publisher")
	_, err = webhook.New("", webhook.WithPublisher(&recorder{}), webhook.WithVerifier(webhook.BasicAuth("u", "p")))
	assert.ErrorContains(t, err, "name")
	_, err = webhook.New("x", webhook.WithPublisher(&recorder{}), webhook.WithVerifier(webhook.BasicAuth("u", "p")),
		webhook.WithTopic("notification", "not.a.topic"))
	assert.Error(t, err)
	_, err = webhook.HMAC(webhook.HMACConfig{Header: "X"})
	assert.Error(t, err)
}

func TestAckComesWithinTheBudgetEvenIfThePublishIsSlow(t *testing.T) {
	pub := &slowPub{delay: 300 * time.Millisecond}
	rc := newBasic(t, webhook.WithPublisher(pub), webhook.WithAckBudget(50*time.Millisecond))
	start := time.Now()
	w := post(rc, `{"a":1}`)
	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Less(t, time.Since(start), 250*time.Millisecond)
	assert.Eventually(t, func() bool { return pub.done.Load() == 1 }, 2*time.Second, 10*time.Millisecond,
		"the publish still completes after the ack")
}

func TestConcurrentDuplicatesPublishOnce(t *testing.T) {
	rec := &recorder{}
	rc := newBasic(t, webhook.WithPublisher(rec), webhook.WithIdempotency(memory.New(), time.Hour, nil))
	var wg sync.WaitGroup
	codes := make(chan int, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- post(rc, `{"same":true}`).Code }()
	}
	wg.Wait()
	close(codes)
	accepted := 0
	for c := range codes {
		if c == http.StatusAccepted {
			accepted++
		} else {
			assert.Equal(t, http.StatusOK, c)
		}
	}
	assert.Equal(t, 1, accepted)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Len(t, rec.events, 1)
}

func TestHandshakeBypassesVerificationOnlyForItsToken(t *testing.T) {
	rec := &recorder{}
	rc := newBasic(t, webhook.WithPublisher(rec), webhook.WithHandshake(webhook.GraphValidationToken()))
	w := httptest.NewRecorder()
	rc.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/?validationToken="+strings.Repeat("a", 3000), nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = httptest.NewRecorder()
	rc.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")))
	assert.Equal(t, http.StatusUnauthorized, w.Code, "no token, no bypass")
}

func TestReceiverNamesCannotCollideClaims(t *testing.T) {
	for _, bad := range []string{"a/b", "A", "a b", "a.b", ""} {
		_, err := webhook.New(bad, webhook.WithPublisher(&recorder{}), webhook.WithVerifier(webhook.BasicAuth("u", "p")))
		assert.Error(t, err, "%q", bad)
	}
	// "a" + key "b/c" and "a/b" + key "c" joined to the same string under
	// the old scheme; the claim is now a hash of the key.
	store := memory.New()
	mk := func(name string) *webhook.Receiver {
		rec := &recorder{}
		rc, err := webhook.New(name, webhook.WithPublisher(rec), webhook.WithVerifier(webhook.BasicAuth("u", "p")),
			webhook.WithIdempotency(store, time.Hour, func(*http.Request, []byte) string { return "b/c" }))
		require.NoError(t, err)
		return rc
	}
	a, ab := mk("a"), mk("a-b")
	assert.Equal(t, http.StatusAccepted, post(a, "1").Code)
	assert.Equal(t, http.StatusAccepted, post(ab, "1").Code, "another receiver, another claim")
	keys, _ := store.List(context.Background(), "webhook/")
	assert.Len(t, keys, 2)
	for _, k := range keys {
		assert.NotContains(t, k, "b/c")
	}
}

func TestHeaderKeyAloneNeverClaims(t *testing.T) {
	rec := &recorder{}
	rc, err := webhook.New("t", webhook.WithPublisher(rec), webhook.WithVerifier(webhook.BasicAuth("u", "p")),
		webhook.WithIdempotency(memory.New(), time.Hour, webhook.HeaderKey("X-Id")))
	require.NoError(t, err)
	send := func(id, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.SetBasicAuth("u", "p")
		req.Header.Set("X-Id", id)
		w := httptest.NewRecorder()
		rc.ServeHTTP(w, req)
		return w.Code
	}
	assert.Equal(t, http.StatusAccepted, send("one", "body"))
	assert.Equal(t, http.StatusOK, send("two", "body"), "a changed unsigned header is still the same delivery")
	assert.Equal(t, http.StatusAccepted, send("one", "other body"))
}

func TestBadTimestampIsRefusedBeforeAnyMAC(t *testing.T) {
	cfg := webhook.HMACConfig{Header: "S", Secret: []byte("k"), TimestampHeader: "T"}
	v, err := webhook.HMAC(cfg)
	require.NoError(t, err)
	for _, ts := range []string{"123.abc", "12 3", "+12", "-5", "", "0012", strings.Repeat("9", 40)} {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("T", ts)
		req.Header.Set("S", "anything")
		err := v.Verify(req, nil)
		require.ErrorIs(t, err, webhook.ErrUnverified, ts)
		assert.Contains(t, err.Error(), "timestamp", ts)
	}
}
