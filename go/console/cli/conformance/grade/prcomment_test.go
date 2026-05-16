package grade

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"hop.top/kit/go/conformance/client"
)

// TestParsePRNumberFromRef exercises the GITHUB_REF parser.
func TestParsePRNumberFromRef(t *testing.T) {
	tests := []struct {
		ref  string
		want int
		ok   bool
	}{
		{"refs/pull/42/merge", 42, true},
		{"refs/pull/7/head", 7, true},
		{"refs/heads/main", 0, false},
		{"", 0, false},
		{"refs/pull/abc/merge", 0, false},
	}
	for _, tc := range tests {
		got, err := parsePRNumberFromRef(tc.ref)
		if (err == nil) != tc.ok {
			t.Errorf("parsePRNumberFromRef(%q) err=%v, want ok=%v", tc.ref, err, tc.ok)
			continue
		}
		if got != tc.want {
			t.Errorf("parsePRNumberFromRef(%q) = %d, want %d", tc.ref, got, tc.want)
		}
	}
}

// TestBuildCommentBodyMarker asserts every body begins with the
// marker line — the idempotency contract depends on it.
func TestBuildCommentBodyMarker(t *testing.T) {
	r := &client.Result{
		ScenarioID: "t.body",
		Verdict:    client.VerdictPass,
		Tier:       1,
	}
	body := buildCommentBody(r)
	if !strings.HasPrefix(body, MarkerComment) {
		t.Fatalf("body missing marker prefix: %s", body[:80])
	}
	if !strings.Contains(body, "t.body") {
		t.Fatalf("body missing scenario id")
	}
}

// TestUpsertCommentInsert covers the "no existing comment" path.
func TestUpsertCommentInsert(t *testing.T) {
	var posted atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// list comments — return empty.
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]ghComment{})
		case http.MethodPost:
			posted.Store(true)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
		}
	}))
	defer srv.Close()

	g := &ghClient{baseURL: srv.URL, token: "t", repo: "o/r", http: http.DefaultClient}
	if err := g.UpsertPRComment(context.Background(), 1, "body"); err != nil {
		t.Fatalf("UpsertPRComment: %v", err)
	}
	if !posted.Load() {
		t.Fatal("expected POST to be issued")
	}
}

// TestUpsertCommentUpdate covers the "marker found" path.
func TestUpsertCommentUpdate(t *testing.T) {
	var patched atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]ghComment{
				{ID: 42, Body: MarkerComment + "\n\nold body"},
			})
		case http.MethodPatch:
			patched.Store(true)
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "new body") {
				t.Errorf("patch body missing new content: %s", body)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42})
		case http.MethodPost:
			t.Errorf("unexpected POST when PATCH was expected")
		}
	}))
	defer srv.Close()

	g := &ghClient{baseURL: srv.URL, token: "t", repo: "o/r", http: http.DefaultClient}
	if err := g.UpsertPRComment(context.Background(), 1, MarkerComment+"\n\nnew body"); err != nil {
		t.Fatalf("UpsertPRComment: %v", err)
	}
	if !patched.Load() {
		t.Fatal("expected PATCH to be issued")
	}
}

// TestBuildCheckRunPayloadConclusion covers the verdict→conclusion
// mapping table.
func TestBuildCheckRunPayloadConclusion(t *testing.T) {
	tests := []struct {
		verdict, want string
	}{
		{client.VerdictPass, "success"},
		{client.VerdictFail, "failure"},
		{client.VerdictUngradable, "neutral"},
		{"weird", "neutral"},
	}
	for _, tc := range tests {
		got := buildCheckRunPayload("sha", &client.Result{Verdict: tc.verdict, ScenarioID: "s"})
		if got["conclusion"] != tc.want {
			t.Errorf("verdict %q conclusion = %q, want %q", tc.verdict, got["conclusion"], tc.want)
		}
	}
}
