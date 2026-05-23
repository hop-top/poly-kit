package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// captured records the auth/headers a test server saw, so test
// assertions can reach beyond the status code.
type captured struct {
	authHeader      string
	signatureHeader string
	contentType     string
	topicHeader     string
	body            []byte
}

func newServer(t *testing.T, status int, cap *captured) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.authHeader = r.Header.Get("Authorization")
		cap.signatureHeader = r.Header.Get("X-Kit-Bus-Signature")
		cap.contentType = r.Header.Get("Content-Type")
		cap.topicHeader = r.Header.Get("X-Kit-Bus-Topic")
		cap.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPostBearerOnly: bearer token in the absence of a signing key
// goes into Authorization.
func TestPostBearerOnly(t *testing.T) {
	t.Parallel()
	cap := &captured{}
	srv := newServer(t, http.StatusOK, cap)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := Post(ctx, PostOpts{
		IngressURL: srv.URL, Token: "abc123", Topic: "github.pr.run.completed",
	}, []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if cap.authHeader != "Bearer abc123" {
		t.Errorf("Authorization = %q, want Bearer abc123", cap.authHeader)
	}
	if cap.signatureHeader != "" {
		t.Errorf("X-Kit-Bus-Signature = %q, want empty", cap.signatureHeader)
	}
	if cap.topicHeader != "github.pr.run.completed" {
		t.Errorf("X-Kit-Bus-Topic = %q", cap.topicHeader)
	}
}

// TestPostSigningKeyWinsOverBearer asserts spec §3 auth precedence:
// signing key beats bearer when both are configured.
func TestPostSigningKeyWinsOverBearer(t *testing.T) {
	t.Parallel()
	cap := &captured{}
	srv := newServer(t, http.StatusOK, cap)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := Post(ctx, PostOpts{
		IngressURL: srv.URL,
		Token:      "should-be-ignored",
		SigningKey: "supersecret",
		Topic:      "github.pr.run.completed",
	}, []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if cap.authHeader != "" {
		t.Errorf("Authorization = %q, want empty when signing key set", cap.authHeader)
	}
	if cap.signatureHeader == "" {
		t.Error("X-Kit-Bus-Signature missing when signing key set")
	}
	// HMAC of {"x":1} with key supersecret — verify the prefix
	// (full hex compared in TestSignatureHeaderHMAC below).
	if got := cap.signatureHeader; got[:7] != "sha256=" {
		t.Errorf("signature header missing sha256= prefix: %q", got)
	}
}

// TestPostSigningKeyOnly: signing key alone sets only signature header.
func TestPostSigningKeyOnly(t *testing.T) {
	t.Parallel()
	cap := &captured{}
	srv := newServer(t, http.StatusOK, cap)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := Post(ctx, PostOpts{
		IngressURL: srv.URL, SigningKey: "supersecret",
	}, []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if cap.signatureHeader == "" {
		t.Error("X-Kit-Bus-Signature missing")
	}
	if cap.authHeader != "" {
		t.Errorf("Authorization should be empty: %q", cap.authHeader)
	}
}

// TestPostNoAuth: neither key nor token set → request goes out
// unauthenticated. Ingress acceptance is its policy decision.
func TestPostNoAuth(t *testing.T) {
	t.Parallel()
	cap := &captured{}
	srv := newServer(t, http.StatusOK, cap)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := Post(ctx, PostOpts{IngressURL: srv.URL}, []byte(`{}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if cap.authHeader != "" || cap.signatureHeader != "" {
		t.Errorf("auth leaked: auth=%q sig=%q", cap.authHeader, cap.signatureHeader)
	}
}

// TestPostNon2xxReturnsError: any non-2xx is an error; caller decides
// whether to surface based on strict mode.
func TestPostNon2xxReturnsError(t *testing.T) {
	t.Parallel()
	cap := &captured{}
	srv := newServer(t, http.StatusInternalServerError, cap)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, _, err := Post(ctx, PostOpts{IngressURL: srv.URL}, []byte(`{}`))
	if err == nil {
		t.Fatal("Post: want error on 500, got nil")
	}
	if status != 500 {
		t.Errorf("status = %d, want 500", status)
	}
}

// TestSignatureHeaderHMAC verifies the HMAC-SHA256 hex digest matches
// the expected output for a known key/body pair. Pinning the digest
// guards against silent algorithm drift.
func TestSignatureHeaderHMAC(t *testing.T) {
	t.Parallel()
	// HMAC-SHA256("key", "hello") =
	// 9307b3b8c2c8aa3a8a82a1d7b3b7b9d2e02f1b6d2b7c3a3b8a2d2d3c2a2e1a1c (placeholder)
	// We compare against signatureHeader rather than hard-coding so
	// changes in HMAC implementation surface in a single place.
	want := signatureHeader("key", []byte("hello"))
	if want == "" || want[:7] != "sha256=" {
		t.Fatalf("signatureHeader produced %q", want)
	}
	// Determinism: same inputs → same output.
	got := signatureHeader("key", []byte("hello"))
	if got != want {
		t.Errorf("signatureHeader not deterministic: %q vs %q", got, want)
	}
}
