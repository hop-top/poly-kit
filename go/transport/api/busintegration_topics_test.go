package api_test

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// topicPub is a minimal EventPublisher capturing the topics it sees.
// It's defined here (rather than reusing testPublisher from
// e2e_server_test.go) so this file stays self-contained.
type topicPub struct {
	mu     sync.Mutex
	topics []string
}

func (p *topicPub) Publish(_ context.Context, topic, _ string, _ any) error {
	p.mu.Lock()
	p.topics = append(p.topics, topic)
	p.mu.Unlock()
	return nil
}

func (p *topicPub) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.topics))
	copy(out, p.topics)
	return out
}

// runOneRequest spins up a Router with the given EventPublisher options,
// makes a single GET, and returns the topics observed by pub.
func runOneRequest(t *testing.T, pub *topicPub, opts ...api.Option) []string {
	t.Helper()

	r := api.NewRouter(api.WithEventPublisher(pub, opts...))
	r.Handle("GET", "/hello", func(w http.ResponseWriter, _ *http.Request) {
		api.JSON(w, http.StatusOK, map[string]string{"msg": "world"})
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()

	srv := &http.Server{Handler: r}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	resp, err := http.Get("http://" + addr + "/hello")
	require.NoError(t, err)
	resp.Body.Close()

	// Allow the deferred end-of-request publish to land.
	time.Sleep(50 * time.Millisecond)
	return pub.seen()
}

func TestBusIntegration_DefaultTopicsAreConformant(t *testing.T) {
	pub := &topicPub{}
	got := runOneRequest(t, pub)
	require.Len(t, got, 2)
	assert.Equal(t, "kit.api.request.started", got[0])
	assert.Equal(t, "kit.api.request.ended", got[1])
}

func TestBusIntegration_WithTopicPrefix(t *testing.T) {
	pub := &topicPub{}
	got := runOneRequest(t, pub, api.WithTopicPrefix("myapp.api.request"))
	require.Len(t, got, 2)
	assert.Equal(t, "myapp.api.request.started", got[0])
	assert.Equal(t, "myapp.api.request.ended", got[1])
}

func TestBusIntegration_WithTopicsOverridesOnlyRequestStart(t *testing.T) {
	pub := &topicPub{}
	got := runOneRequest(t, pub, api.WithTopics(api.Topics{
		RequestStart: "x.y.z.started",
	}))
	require.Len(t, got, 2)
	assert.Equal(t, "x.y.z.started", got[0], "RequestStart should be overridden")
	// RequestEnd left empty in opts → keeps default.
	assert.Equal(t, "kit.api.request.ended", got[1], "RequestEnd should keep default")
}

func TestBusIntegration_WithTopicsOverridesBoth(t *testing.T) {
	pub := &topicPub{}
	got := runOneRequest(t, pub, api.WithTopics(api.Topics{
		RequestStart: "myapp.web.request.started",
		RequestEnd:   "myapp.web.request.ended",
	}))
	require.Len(t, got, 2)
	assert.Equal(t, "myapp.web.request.started", got[0])
	assert.Equal(t, "myapp.web.request.ended", got[1])
}

func TestBusIntegration_WithTopicPrefix_InvalidPanics(t *testing.T) {
	// 2-segment prefix violates PrefixTopics' 3-segment rule.
	assert.Panics(t, func() {
		_ = api.WithEventPublisher(&topicPub{},
			api.WithTopicPrefix("only.two"))
	})
}

func TestBusIntegration_WithTopics_InvalidPanics(t *testing.T) {
	// "start" is present-tense → ValidateTopic fails.
	assert.Panics(t, func() {
		_ = api.WithEventPublisher(&topicPub{},
			api.WithTopics(api.Topics{RequestStart: "kit.api.request.start"}))
	})
}

func TestBusIntegration_DefaultTopicsExportedConstants(t *testing.T) {
	// Sanity check the exported defaults match what the middleware emits.
	assert.Equal(t, "kit.api.request.started", string(api.DefaultTopics.RequestStart))
	assert.Equal(t, "kit.api.request.ended", string(api.DefaultTopics.RequestEnd))
}
