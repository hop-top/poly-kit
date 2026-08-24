package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/conformance/scenario"
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

// TestGradeDecodesAssertionTraces pins the /v1/grade wire contract:
// svc serializes the grader's scenario.Result verbatim under the
// "result" key (see svc.GradeResponse), so a tier-3 body looks like
//
//	{
//	  "result": {
//	    "scenario_id": "...", "schema_version": "1",
//	    "verdict": "fail", "reason": "...", "scored_at": "...",
//	    "grader_version": "...", "rules_version": "...", "tier": 3,
//	    "facets":     [{"factor": 11, "status": "fail"}],
//	    "assertions": [{"id": "exits-zero", "kind": "exit_code_equals",
//	                    "factor": 11, "status": "fail",
//	                    "observed": 3, "expected": 0,
//	                    "message": "exit code 3 != 0"}]
//	  },
//	  "service": {"version": "...", "request_id": "..."}
//	}
//
// The typed client must preserve the per-assertion trace through
// decode. The trace is probed via a re-marshal of the returned Result
// so the test compiles against any Result shape and fails at runtime
// if the trace is dropped (the original defect: the client decoded a
// "findings" field svc never sends, silently discarding every trace).
func TestGradeDecodesAssertionTraces(t *testing.T) {
	wire := &scenario.Result{
		ScenarioID:    "acme/widget",
		SchemaVersion: "1",
		Verdict:       scenario.VerdictFail,
		Reason:        "1 of 2 assertions failed",
		ScoredAt:      time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC),
		GraderVersion: "0.3.0",
		RulesVersion:  "2026-05-01",
		Tier:          3,
		Facets: []scenario.FactorFacet{
			{Factor: 1, Status: scenario.StatusPass},
			{Factor: 11, Status: scenario.StatusFail},
		},
		Assertions: []scenario.AssertionResult{
			{ID: "help-works", Kind: "exit_code_equals", Factor: 1,
				Status: scenario.StatusPass, Observed: 0, Expected: 0},
			{ID: "exits-zero", Kind: "exit_code_equals", Factor: 11,
				Status: scenario.StatusFail, Observed: 3, Expected: 0,
				Message: "exit code 3 != 0"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result":  wire,
			"service": map[string]string{"version": "0.1.0", "request_id": "req-1"},
		})
	}))
	defer srv.Close()

	c, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := c.Grade(context.Background(), GradeRequest{CassetteDir: buildFixtureDir(t)})
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}

	reencoded, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var probe struct {
		Facets []struct {
			Factor int    `json:"factor"`
			Status string `json:"status"`
		} `json:"facets"`
		Assertions []struct {
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Factor   int    `json:"factor"`
			Status   string `json:"status"`
			Observed any    `json:"observed"`
			Expected any    `json:"expected"`
			Message  string `json:"message"`
		} `json:"assertions"`
	}
	if err := json.Unmarshal(reencoded, &probe); err != nil {
		t.Fatalf("probe decode: %v", err)
	}
	if len(probe.Assertions) != 2 {
		t.Fatalf("per-assertion trace dropped by client decode; got %d assertions, want 2\nresult: %s",
			len(probe.Assertions), reencoded)
	}
	fail := probe.Assertions[1]
	if fail.ID != "exits-zero" || fail.Kind != "exit_code_equals" || fail.Factor != 11 {
		t.Errorf("assertion identity mangled: %+v", fail)
	}
	if fail.Status != "fail" {
		t.Errorf("Status = %q, want fail", fail.Status)
	}
	if got := fmt.Sprint(fail.Observed); got != "3" {
		t.Errorf("Observed = %v, want 3", fail.Observed)
	}
	if got := fmt.Sprint(fail.Expected); got != "0" {
		t.Errorf("Expected = %v, want 0", fail.Expected)
	}
	if fail.Message != "exit code 3 != 0" {
		t.Errorf("Message = %q, want %q", fail.Message, "exit code 3 != 0")
	}
	if len(probe.Facets) != 2 || probe.Facets[1].Factor != 11 || probe.Facets[1].Status != "fail" {
		t.Errorf("facet rollups mangled: %+v", probe.Facets)
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

// TestGradeSendsScenarioRefHeaders asserts the wire contract svc
// enforces: X-Kit-Scenario-Ref carries the manifest's scenario ref
// (with override + version), X-Kit-Tier carries the requested tier.
// Without these headers svc rejects every upload before grading.
func TestGradeSendsScenarioRefHeaders(t *testing.T) {
	var gotRef, gotTier string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRef = r.Header.Get("X-Kit-Scenario-Ref")
		gotTier = r.Header.Get("X-Kit-Tier")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{"scenario_id": "acme/widget", "verdict": VerdictPass},
		})
	}))
	defer srv.Close()

	c, _ := New(srv.URL)
	dir := t.TempDir()
	writeFixture(t, dir, "manifest.yaml",
		"schema_version: \"1\"\nscenario_id: acme/widget\nscenario_version: v1\n")
	writeFixture(t, dir, "steps/step-1/result.json", "{\"exit_code\":0}\n")

	if _, err := c.Grade(context.Background(), GradeRequest{CassetteDir: dir, Tier: 3}); err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if gotRef != "acme/widget@v1" {
		t.Errorf("X-Kit-Scenario-Ref = %q, want acme/widget@v1", gotRef)
	}
	if gotTier != "3" {
		t.Errorf("X-Kit-Tier = %q, want 3", gotTier)
	}

	// Explicit override wins over the manifest.
	if _, err := c.Grade(context.Background(),
		GradeRequest{CassetteDir: dir, ScenarioID: "other/thing@v2"}); err != nil {
		t.Fatalf("Grade with override: %v", err)
	}
	if gotRef != "other/thing@v2" {
		t.Errorf("override X-Kit-Scenario-Ref = %q, want other/thing@v2", gotRef)
	}
}

// TestGradeManifestRoundTripsServiceFields asserts Pack does not
// strip the svc-required manifest fields (binary, recorder,
// story_ref, steps[].captures) when re-materializing manifest.yaml.
func TestGradeManifestRoundTripsServiceFields(t *testing.T) {
	dir := t.TempDir()
	manifest := `schema_version: "1"
binary: example
recorder: xrr
recorder_version: 0.1.0
recorded_at: 2026-08-08T12:00:00Z
scenario_id: acme/widget
story_ref:
  story_id: example.story
  content_hash: sha256:deadbeef
steps:
  - id: step-1
    cassette_dir: steps/step-1/cassette
    captures: steps/step-1
`
	writeFixture(t, dir, "manifest.yaml", manifest)

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Binary != "example" || m.Recorder != "xrr" || m.RecorderVersion != "0.1.0" {
		t.Fatalf("recorder fields dropped: %+v", m)
	}
	if m.StoryRef.StoryID != "example.story" || m.StoryRef.ContentHash != "sha256:deadbeef" {
		t.Fatalf("story_ref dropped: %+v", m.StoryRef)
	}
	if len(m.Steps) != 1 || m.Steps[0].Captures != "steps/step-1" {
		t.Fatalf("steps[].captures dropped: %+v", m.Steps)
	}

	encoded, err := encodeManifest(m)
	if err != nil {
		t.Fatalf("encodeManifest: %v", err)
	}
	for _, want := range []string{"binary: example", "recorder: xrr", "content_hash", "captures: steps/step-1"} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("re-encoded manifest missing %q:\n%s", want, encoded)
		}
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
