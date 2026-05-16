package svc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/transport/api"
)

// fakeGrader is a test ScenarioGrader. It returns a canned passing
// result so handler-level wiring can be exercised without depending on
// the parallel scen track.
type fakeGrader struct{ err error }

func (f fakeGrader) Grade(_ context.Context, in GradeInput) (*Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &Result{
		ScenarioID:    in.Scenario.Namespace + "/" + in.Scenario.ID,
		SchemaVersion: "1",
		Verdict:       "pass",
		ScoredAt:      time.Now().UTC(),
		GraderVersion: "test-1.0",
		Tier:          3,
		Facets:        map[string]any{"steps": len(in.StepCaptures)},
		Assertions:    []AssertionResult{{Verb: "exit_code_eq", Pass: true}},
		JudgeTraces:   []JudgeTrace{{Model: "stub", Verdict: "pass"}},
	}, nil
}

func newTestService(t *testing.T) (*Service, *SQLClaimStore, string, string) {
	t.Helper()
	tmp := t.TempDir()
	// Seed scenario layout: acme/widget/v1/scenario.yaml.
	scYAML := []byte("schema_version: \"1\"\n")
	if err := os.MkdirAll(filepath.Join(tmp, "scenarios/acme/widget/v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "scenarios/acme/widget/v1/scenario.yaml"), scYAML, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := NewFSStore(context.Background(), tmp)
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}

	claims, err := OpenSQLClaimStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLClaimStore: %v", err)
	}
	t.Cleanup(func() { _ = claims.Close() })

	_, token, err := claims.Mint(context.Background(), MintInput{
		Tenant:  "acme",
		Scopes:  []string{"grade:acme", "meta:acme"},
		TierMax: 3,
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	svc := NewService(store, claims, fakeGrader{})
	return svc, claims, token, tmp
}

func mount(svc *Service) *api.Router {
	r := api.NewRouter(api.WithMiddleware(api.RequestID()))
	svc.Mount(r)
	return r
}

func TestHandleGrade_HappyPath(t *testing.T) {
	svc, _, token, _ := newTestService(t)
	router := mount(svc)
	srv := httptest.NewServer(router)
	defer srv.Close()

	body := buildCassette(t, nil)
	req, _ := http.NewRequest("POST", srv.URL+"/v1/grade", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", CassetteContentType)
	req.Header.Set("X-Kit-Scenario-Ref", "acme/widget@v1")
	req.Header.Set("X-Kit-Tier", "2")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/grade: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var out GradeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Result == nil || out.Result.Verdict != "pass" {
		t.Fatalf("result: %+v", out.Result)
	}
	if out.Result.Tier != 2 {
		t.Errorf("tier: got %d, want 2 (truncated)", out.Result.Tier)
	}
	// Tier 2 keeps Facets, strips Assertions + JudgeTraces.
	if out.Result.Assertions != nil {
		t.Errorf("Assertions should be nil at tier 2, got %v", out.Result.Assertions)
	}
	if out.Result.JudgeTraces != nil {
		t.Errorf("JudgeTraces should be nil at tier 2, got %v", out.Result.JudgeTraces)
	}
	if len(out.Result.Facets) == 0 {
		t.Errorf("Facets should be non-nil at tier 2")
	}
}

func TestHandleGrade_MissingBearer(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	router := mount(svc)
	srv := httptest.NewServer(router)
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/v1/grade", strings.NewReader(""))
	req.Header.Set("Content-Type", CassetteContentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", resp.StatusCode)
	}
}

func TestHandleGrade_ScopeDenied(t *testing.T) {
	svc, claims, _, _ := newTestService(t)
	// Mint a second token with mismatched scope.
	_, otherToken, err := claims.Mint(context.Background(), MintInput{
		Tenant:  "other",
		Scopes:  []string{"grade:other-ns"},
		TierMax: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	router := mount(svc)
	srv := httptest.NewServer(router)
	defer srv.Close()

	body := buildCassette(t, nil)
	req, _ := http.NewRequest("POST", srv.URL+"/v1/grade", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+otherToken)
	req.Header.Set("Content-Type", CassetteContentType)
	req.Header.Set("X-Kit-Scenario-Ref", "acme/widget@v1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", resp.StatusCode)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env.Error.Code != CodeScopeDenied {
		t.Errorf("code: got %q, want %q", env.Error.Code, CodeScopeDenied)
	}
}

func TestHandleGrade_TierExceedsClaim(t *testing.T) {
	svc, claims, _, _ := newTestService(t)
	_, t1Token, _ := claims.Mint(context.Background(), MintInput{
		Tenant: "acme", Scopes: []string{"grade:acme"}, TierMax: 1,
	})
	router := mount(svc)
	srv := httptest.NewServer(router)
	defer srv.Close()

	body := buildCassette(t, nil)
	req, _ := http.NewRequest("POST", srv.URL+"/v1/grade", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+t1Token)
	req.Header.Set("Content-Type", CassetteContentType)
	req.Header.Set("X-Kit-Scenario-Ref", "acme/widget@v1")
	req.Header.Set("X-Kit-Tier", "3")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", resp.StatusCode)
	}
}

func TestHandleGrade_ScenarioNotFound(t *testing.T) {
	svc, _, token, _ := newTestService(t)
	router := mount(svc)
	srv := httptest.NewServer(router)
	defer srv.Close()

	body := buildCassette(t, nil)
	req, _ := http.NewRequest("POST", srv.URL+"/v1/grade", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", CassetteContentType)
	req.Header.Set("X-Kit-Scenario-Ref", "acme/does-not-exist")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestHandleGrade_StoryHashMismatch(t *testing.T) {
	svc, _, token, _ := newTestService(t)
	router := mount(svc)
	srv := httptest.NewServer(router)
	defer srv.Close()

	body := buildCassette(t, func(s *cassetteSpec) {
		s.storyBytes = []byte("tampered\n")
	})
	req, _ := http.NewRequest("POST", srv.URL+"/v1/grade", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", CassetteContentType)
	req.Header.Set("X-Kit-Scenario-Ref", "acme/widget@v1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status: got %d, want 409", resp.StatusCode)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env.Error.Code != CodeStoryHashMismatch {
		t.Errorf("code: got %q, want %q", env.Error.Code, CodeStoryHashMismatch)
	}
}

func TestHandleRunAndGrade_501(t *testing.T) {
	svc, _, token, _ := newTestService(t)
	router := mount(svc)
	srv := httptest.NewServer(router)
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/v1/run-and-grade", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status: got %d, want 501", resp.StatusCode)
	}
}

func TestHandleHealthAndReady(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	router := mount(svc)
	srv := httptest.NewServer(router)
	defer srv.Close()

	for _, p := range []string{"/healthz", "/readyz", "/v1/capabilities"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s status: got %d, want 200", p, resp.StatusCode)
		}
	}
}

func TestHandleGrade_GraderError(t *testing.T) {
	svc, _, token, _ := newTestService(t)
	svc.Grader = fakeGrader{err: errors.New("simulated failure")}
	router := mount(svc)
	srv := httptest.NewServer(router)
	defer srv.Close()

	body := buildCassette(t, nil)
	req, _ := http.NewRequest("POST", srv.URL+"/v1/grade", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", CassetteContentType)
	req.Header.Set("X-Kit-Scenario-Ref", "acme/widget@v1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("grader error: got %d, want 500", resp.StatusCode)
	}
}
