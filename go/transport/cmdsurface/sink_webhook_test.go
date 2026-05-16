package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebhookSink_PostsEnvelope(t *testing.T) {
	var (
		gotBody    []byte
		gotHeaders http.Header
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := &WebhookSink{
		URL:     srv.URL,
		Client:  srv.Client(),
		Headers: map[string]string{"X-Token": "abc"},
	}
	inv := Invocation{Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceREST}}
	if err := sink.Emit(context.Background(), inv, Result{ExitCode: 0}, nil); err != nil {
		t.Fatalf("Emit err: %v", err)
	}
	var env webhookSinkEnvelope
	if err := json.Unmarshal(gotBody, &env); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, gotBody)
	}
	if got := env.Invocation.Path; len(got) != 2 || got[0] != "widget" || got[1] != "add" {
		t.Errorf("invocation.path=%v", got)
	}
	if env.Error != nil {
		t.Errorf("error=%v, want nil", *env.Error)
	}
	if gotHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type=%q", gotHeaders.Get("Content-Type"))
	}
	if gotHeaders.Get("X-Token") != "abc" {
		t.Errorf("X-Token=%q", gotHeaders.Get("X-Token"))
	}
}

func TestWebhookSink_ErrorEnvelope(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted) // 2xx
	}))
	defer srv.Close()

	sink := &WebhookSink{URL: srv.URL, Client: srv.Client()}
	if err := sink.Emit(context.Background(), Invocation{Path: []string{"x"}}, Result{ExitCode: 3}, errors.New("nope")); err != nil {
		t.Fatalf("Emit err: %v", err)
	}
	var env webhookSinkEnvelope
	if err := json.Unmarshal(gotBody, &env); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if env.Error == nil || *env.Error != "nope" {
		t.Errorf("error=%v", env.Error)
	}
}

func TestWebhookSink_SignCalled(t *testing.T) {
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var (
		signCalled atomic.Bool
		mu         sync.Mutex
		gotBody    []byte
	)
	sink := &WebhookSink{
		URL:    srv.URL,
		Client: srv.Client(),
		Sign: func(body []byte) (string, string) {
			signCalled.Store(true)
			mu.Lock()
			gotBody = append([]byte(nil), body...)
			mu.Unlock()
			return "X-Signature", "sig-abc"
		},
	}
	if err := sink.Emit(context.Background(), Invocation{}, Result{}, nil); err != nil {
		t.Fatalf("Emit err: %v", err)
	}
	if !signCalled.Load() {
		t.Fatal("Sign was not invoked")
	}
	if gotSig != "sig-abc" {
		t.Errorf("X-Signature=%q", gotSig)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gotBody) == 0 {
		t.Errorf("Sign got empty body")
	}
}

func TestWebhookSink_SignWinsOverHeaders(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	sink := &WebhookSink{
		URL:     srv.URL,
		Client:  srv.Client(),
		Headers: map[string]string{"X-Signature": "stale"},
		Sign:    func(_ []byte) (string, string) { return "X-Signature", "fresh" },
	}
	if err := sink.Emit(context.Background(), Invocation{}, Result{}, nil); err != nil {
		t.Fatalf("Emit err: %v", err)
	}
	if gotHeader != "fresh" {
		t.Errorf("X-Signature=%q, want fresh", gotHeader)
	}
}

func TestWebhookSink_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("kaboom"))
	}))
	defer srv.Close()
	sink := &WebhookSink{URL: srv.URL, Client: srv.Client()}
	err := sink.Emit(context.Background(), Invocation{}, Result{}, nil)
	if err == nil {
		t.Fatal("expected non-2xx to return an error")
	}
}

func TestWebhookSink_ContextCancelledError(t *testing.T) {
	// Server: respond after request context cancellation so the
	// handler unblocks cleanly when the client closes the conn.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	sink := &WebhookSink{
		URL:    srv.URL,
		Client: srv.Client(),
	}
	err := sink.Emit(ctx, Invocation{}, Result{}, nil)
	if err == nil {
		t.Fatal("expected ctx cancellation error")
	}
}

func TestWebhookSink_ClosedServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	url := srv.URL
	srv.Close()
	sink := &WebhookSink{URL: url, Client: &http.Client{Timeout: 200 * time.Millisecond}}
	if err := sink.Emit(context.Background(), Invocation{}, Result{}, nil); err == nil {
		t.Fatal("expected error when server is closed")
	}
}

func TestWebhookSink_BadURLError(t *testing.T) {
	sink := &WebhookSink{URL: "://bad", Client: &http.Client{Timeout: time.Second}}
	if err := sink.Emit(context.Background(), Invocation{}, Result{}, nil); err == nil {
		t.Fatal("expected error on bad URL")
	}
}
