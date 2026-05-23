package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// envSet sets env vars for the duration of the test, restoring the
// originals on cleanup. Avoids the t.Setenv requirement of a single
// goroutine — these tests don't go concurrent.
func envSet(t *testing.T, vals map[string]string) {
	t.Helper()
	for k, v := range vals {
		t.Setenv(k, v)
	}
}

func ingress(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRunFailOpenOn500: default behavior is fail-open; a 5xx response
// must yield a nil error (workflow step succeeds, CI does not break).
func TestRunFailOpenOn500(t *testing.T) {
	srv := ingress(t, http.StatusInternalServerError)
	envSet(t, map[string]string{
		"KIT_BUS_INGRESS_URL": srv.URL,
		"KIT_BUS_REPO":        "x/y",
		"KIT_BUS_ACTOR":       "octocat",
		"KIT_BUS_PR_NUMBER":   "1",
		"KIT_BUS_STRICT":      "", // explicit empty: not strict
	})
	if err := run([]string{"--kind", "pull.closed"}); err != nil {
		t.Fatalf("fail-open default: want nil, got %v", err)
	}
}

// TestRunFailClosedOn500: when KIT_BUS_STRICT="true", a 5xx surfaces
// as an error so the workflow step fails.
func TestRunFailClosedOn500(t *testing.T) {
	srv := ingress(t, http.StatusInternalServerError)
	envSet(t, map[string]string{
		"KIT_BUS_INGRESS_URL": srv.URL,
		"KIT_BUS_REPO":        "x/y",
		"KIT_BUS_ACTOR":       "octocat",
		"KIT_BUS_PR_NUMBER":   "1",
		"KIT_BUS_STRICT":      "true",
	})
	err := run([]string{"--kind", "pull.closed"})
	if err == nil {
		t.Fatal("strict mode: want error on 500, got nil")
	}
}

// TestRunNoIngress: when KIT_BUS_INGRESS_URL is empty, run exits 0
// (defence-in-depth — the workflow `if:` should have stopped this job
// from running in the first place).
func TestRunNoIngress(t *testing.T) {
	envSet(t, map[string]string{
		"KIT_BUS_INGRESS_URL": "",
		"KIT_BUS_REPO":        "x/y",
		"KIT_BUS_ACTOR":       "octocat",
		"KIT_BUS_PR_NUMBER":   "1",
	})
	if err := run([]string{"--kind", "pull.closed"}); err != nil {
		t.Fatalf("no-ingress: want nil, got %v", err)
	}
}

// TestRunUnknownKind: an unknown --kind is a usage error.
func TestRunUnknownKind(t *testing.T) {
	envSet(t, map[string]string{
		"KIT_BUS_INGRESS_URL": "https://example.invalid",
	})
	if err := run([]string{"--kind", "wrong"}); err == nil {
		t.Fatal("unknown kind: want error, got nil")
	}
}

// TestRunHappyPath2xx: a 2xx response yields nil error regardless of
// strict mode.
func TestRunHappyPath2xx(t *testing.T) {
	srv := ingress(t, http.StatusAccepted)
	envSet(t, map[string]string{
		"KIT_BUS_INGRESS_URL": srv.URL,
		"KIT_BUS_REPO":        "x/y",
		"KIT_BUS_ACTOR":       "octocat",
		"KIT_BUS_PR_NUMBER":   "1",
		"KIT_BUS_STRICT":      "true",
	})
	if err := run([]string{"--kind", "pull.merged"}); err != nil {
		t.Fatalf("happy path: %v", err)
	}
}
