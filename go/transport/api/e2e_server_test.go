package api_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

func TestE2E_Server_RandomPort(t *testing.T) {
	r := api.NewRouter()
	r.Handle("GET", "/ping", func(w http.ResponseWriter, req *http.Request) {
		api.JSON(w, http.StatusOK, map[string]string{"pong": "true"})
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()

	srv := &http.Server{
		Handler:      r,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	}

	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	resp, err := http.Get("http://" + addr + "/ping")
	require.NoError(t, err, "server should be reachable")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "true", body["pong"])
}

func TestE2E_Server_ListenAndServe(t *testing.T) {
	r := api.NewRouter()
	r.Handle("GET", "/ping", func(w http.ResponseWriter, req *http.Request) {
		api.JSON(w, http.StatusOK, map[string]string{"pong": "true"})
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- api.ListenAndServe(ctx, addr, r,
			api.WithReadTimeout(2*time.Second),
			api.WithWriteTimeout(2*time.Second),
			api.WithShutdownTimeout(1*time.Second),
		)
	}()

	// Wait for server to be ready.
	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.Get("http://" + addr + "/ping")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, err, "server should be reachable")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	cancel()
	require.NoError(t, <-errCh, "clean shutdown should return nil")
}

// testPublisher implements api.EventPublisher for testing.
type testPublisher struct {
	mu     sync.Mutex
	events []publishedEvent
}

type publishedEvent struct {
	Topic   string
	Source  string
	Payload any
}

func (p *testPublisher) Publish(_ context.Context, topic, source string, payload any) error {
	p.mu.Lock()
	p.events = append(p.events, publishedEvent{Topic: topic, Source: source, Payload: payload})
	p.mu.Unlock()
	return nil
}

func TestE2E_Server_WithEventPublisher(t *testing.T) {
	pub := &testPublisher{}

	r := api.NewRouter(api.WithEventPublisher(pub))
	r.Handle("GET", "/hello", func(w http.ResponseWriter, req *http.Request) {
		api.JSON(w, http.StatusOK, map[string]string{"msg": "world"})
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()

	srv := &http.Server{Handler: r}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	resp, err := http.Get("http://" + addr + "/hello")
	require.NoError(t, err, "server should be reachable")
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Give async handlers a moment.
	time.Sleep(50 * time.Millisecond)

	pub.mu.Lock()
	defer pub.mu.Unlock()

	require.Len(t, pub.events, 2, "should have start and end events")

	assert.Equal(t, "kit.api.request.started", pub.events[0].Topic)
	assert.Equal(t, "api.router", pub.events[0].Source)
	startPayload, ok := pub.events[0].Payload.(map[string]string)
	require.True(t, ok, "start payload should be map[string]string")
	assert.Equal(t, "GET", startPayload["method"])
	assert.Equal(t, "/hello", startPayload["path"])

	assert.Equal(t, "kit.api.request.ended", pub.events[1].Topic)
	assert.Equal(t, "api.router", pub.events[1].Source)
	endPayload, ok := pub.events[1].Payload.(map[string]any)
	require.True(t, ok, "end payload should be map[string]any")
	assert.Equal(t, "GET", endPayload["method"])
	assert.Equal(t, "/hello", endPayload["path"])
	assert.Equal(t, 200, endPayload["status"])
	_, hasDuration := endPayload["duration"]
	assert.True(t, hasDuration, "end payload should have duration")
}
