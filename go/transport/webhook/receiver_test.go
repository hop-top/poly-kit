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
