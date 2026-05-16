package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewRequiresBaseURL asserts the no-default-service-URL contract:
// adopters must pass a base URL or get a usage error.
func TestNewRequiresBaseURL(t *testing.T) {
	_, err := New("")
	if err == nil {
		t.Fatal("New(\"\") succeeded; want usage error")
	}
	if !errors.Is(err, ErrServiceUsage) {
		t.Fatalf("New(\"\") returned %v, want errors.Is ErrServiceUsage", err)
	}
}

// TestGradeSyncPass: svc returns 200 with verdict=pass on first try.
func TestGradeSyncPass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/grade" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != CassetteMIMEType {
			t.Errorf("Content-Type = %q, want %q", ct, CassetteMIMEType)
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Error("missing Idempotency-Key header")
		}
		if r.Header.Get("Authorization") != "Bearer t0k3n" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"grade_id": "g-1",
			"result": map[string]any{
				"scenario_id":    "t.sync.pass",
				"verdict":        VerdictPass,
				"exit_code":      0,
				"grader_version": "1.0.0",
			},
		})
	}))
	defer srv.Close()

	c, err := New(srv.URL, WithToken("t0k3n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dir := buildFixtureDir(t)

	res, err := c.Grade(context.Background(), GradeRequest{CassetteDir: dir})
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if res.Verdict != VerdictPass {
		t.Fatalf("Verdict = %q, want %q", res.Verdict, VerdictPass)
	}
	if res.ScenarioID != "t.sync.pass" {
		t.Fatalf("ScenarioID = %q", res.ScenarioID)
	}
}

// TestGradeAsyncPoll: svc returns 202 then 200 on poll.
func TestGradeAsyncPoll(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/grade":
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"grade_id":            "g-2",
				"poll_url":            "/v1/grade/g-2",
				"retry_after_seconds": 0,
			})
		case "/v1/grade/g-2":
			n := hits.Add(1)
			if n == 1 {
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"retry_after_seconds": 0,
				})
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"scenario_id": "t.async",
					"verdict":     VerdictFail,
					"exit_code":   2,
					"reason":      "3 assertions failed",
				},
			})
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Speed up polling.
	c.backoff.MaxBackoff = 50 * time.Millisecond

	dir := buildFixtureDir(t)
	res, err := c.Grade(context.Background(), GradeRequest{CassetteDir: dir})
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if res.Verdict != VerdictFail {
		t.Fatalf("Verdict = %q, want %q", res.Verdict, VerdictFail)
	}
	if hits.Load() < 2 {
		t.Fatalf("poll hits = %d, want >=2", hits.Load())
	}
}

// TestGradeRetriesOn5xx: first attempt returns 503, second 200.
func TestGradeRetriesOn5xx(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "down")
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"scenario_id": "t.retry",
				"verdict":     VerdictPass,
			},
		})
	}))
	defer srv.Close()

	c, err := New(srv.URL, WithMaxAttempts(3),
		WithBackoff(1*time.Millisecond, 5*time.Millisecond, 2.0, 0))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dir := buildFixtureDir(t)
	res, err := c.Grade(context.Background(), GradeRequest{CassetteDir: dir})
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if res.Verdict != VerdictPass {
		t.Fatalf("Verdict = %q", res.Verdict)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("hits = %d, want 2", got)
	}
}

// TestGradeAuthFailure: 401 returns ErrServiceAuthFailed (terminal).
func TestGradeAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "bad token")
	}))
	defer srv.Close()

	c, _ := New(srv.URL, WithToken("bad"))
	dir := buildFixtureDir(t)
	_, err := c.Grade(context.Background(), GradeRequest{CassetteDir: dir})
	if err == nil {
		t.Fatal("Grade succeeded; want auth failure")
	}
	if !errors.Is(err, ErrServiceAuthFailed) {
		t.Fatalf("err = %v, want errors.Is ErrServiceAuthFailed", err)
	}
}

// TestStatus directly fetches by grade-id.
func TestStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/grade/") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"scenario_id": "t.status",
				"verdict":     VerdictPass,
			},
		})
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	c.backoff.InitialBackoff = time.Millisecond
	c.backoff.MaxBackoff = 5 * time.Millisecond
	res, err := c.Status(context.Background(), "g-xyz")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if res.Verdict != VerdictPass {
		t.Fatalf("Verdict = %q", res.Verdict)
	}
}

// TestIsRetryable covers the predicate.
func TestIsRetryable(t *testing.T) {
	if IsRetryable(nil) {
		t.Error("nil should not be retryable")
	}
	if !IsRetryable(ServiceUnavailableError("x", "", "")) {
		t.Error("ErrServiceUnavailable should be retryable")
	}
	if !IsRetryable(RateLimitedError("x")) {
		t.Error("ErrRateLimited should be retryable")
	}
	if IsRetryable(ServiceAuthFailedError("x", "", "")) {
		t.Error("auth failure should NOT be retryable")
	}
	if IsRetryable(GradeFailError("s", "r")) {
		t.Error("grade-fail should NOT be retryable")
	}
}

// buildFixtureDir constructs a minimal cassette dir on disk for the
// httptest-driven Grade tests.
func buildFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "manifest.yaml", "schema_version: \"1\"\nscenario_id: t.from.manifest\n")
	writeFixture(t, dir, "steps/launch/cassette/keep", "")
	return dir
}
