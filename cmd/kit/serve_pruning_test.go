package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"hop.top/kit/engine/store"
	"hop.top/kit/go/transport/api"
)

// In-process smoke tests for the pruning routes (T-0429, spec
// docs/specs/engine-version-pruning.md §5):
//
//	POST /:type/:id/prune
//	POST /:type/:id/abandon
//	GET  /:type/:id/branches?live=1   (extension of branching route)
//
// End-to-end durability across a process restart is covered by
// serve_pruning_integration_test.go (T-0428) — that file currently
// drives through the engine Go API since the routes were not wired
// at write-time; an HTTP-driven sibling is a follow-up.
//
// We re-use the same fixture shape as serve_branches_test.go (real
// SQLite-backed VersionedDocumentStore wired through the production
// router) — handler logic is thin enough that mocking would be more
// code than it's worth, and this keeps the wire-contract tests
// honest about the actual store contracts.

func newPruningTestServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()

	tmp := t.TempDir()
	ds, err := store.NewDocumentStore(filepath.Join(tmp, "documents.db"))
	if err != nil {
		t.Fatalf("new document store: %v", err)
	}
	vstore, err := store.NewSQLiteVersionStore(ds.DB())
	if err != nil {
		_ = ds.Close()
		t.Fatalf("new sqlite version store: %v", err)
	}
	vds := store.NewVersionedDocumentStore(ds, vstore)

	router := api.NewRouter()
	registerDocumentRoutes(router, vds, nil)
	registerHistoryRoutes(router, vds, vstore)
	registerBranchingRoutes(router, vds, vstore)
	registerPruningRoutes(router, vds)

	srv := httptest.NewServer(router)
	cleanup := func() {
		srv.Close()
		_ = ds.Close()
	}
	return srv, cleanup
}

// seedAbandonedForkTail builds a doc with a linear main line and a
// fork tail that is then abandoned via the route, leaving a single
// live head (the linear tip) plus a dead fork-tail head — the
// canonical setup for exercising prune-fires-on-abandoned-subtree
// (decision #10) through the wire.
//
// Topology:
//
//	seq 1 → seq 2 → seq 3 (live linear head)
//	         ↓
//	         seq 4 (dead fork tail after Abandon)
//
// Returns the fork-tail seq (so tests can reference it by number).
func seedAbandonedForkTail(t *testing.T, base, docType, id string) int {
	t.Helper()
	seedDoc(t, base, docType, id)

	// Fork at seq 1 → fork tail is seq 4.
	resp := doJSON(t, "POST", base+"/"+docType+"/"+id+"/fork", map[string]any{"from_seq": 1})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("fork: want 201, got %d", resp.StatusCode)
	}

	// Abandon the fork tail. Now the linear tip (seq 3) is the only
	// live head.
	aresp := doJSON(t, "POST", base+"/"+docType+"/"+id+"/abandon", map[string]any{"seq": 4})
	aresp.Body.Close()
	if aresp.StatusCode != http.StatusOK {
		t.Fatalf("abandon: want 200, got %d", aresp.StatusCode)
	}
	return 4
}

// TestServePrune_HappyPath: doc with abandoned-fork-tail, POST prune
// with policy → 200 with non-empty versions_removed (the dead
// subtree is now prunable per decision #10).
//
// Uses max_age_seconds=1 + a >1s sleep to get every version under
// the bound; the filter then collapses to "not in retain_floor",
// which after Abandon is precisely the dead-fork-tail subtree. Same
// shape as engine/store/versioned_pruning_test.go's TestPrune_
// AbandonedForkTail_DeadHeadOnly, just driven through HTTP.
//
// max_versions=1 alone won't fire here because the dead fork tail
// has the highest seq (most recent) — position-from-tail is 1, not
// > 1, so the count bound doesn't exclude it. The age bound is the
// load-bearing knob. Sub-second policy isn't expressible through the
// wire (max_age_seconds is whole seconds per spec §5), so the small
// sleep is the price of admission for an HTTP-driven happy path.
func TestServePrune_HappyPath(t *testing.T) {
	srv, cleanup := newPruningTestServer(t)
	defer cleanup()

	seedAbandonedForkTail(t, srv.URL, "notes", "n1")

	// All seeded versions are <1s old at this point. Wait so the
	// max_age_seconds=1 bound makes every version a candidate.
	time.Sleep(1100 * time.Millisecond)

	resp := doJSON(t, "POST", srv.URL+"/notes/n1/prune", map[string]any{
		"max_age_seconds": 1,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("prune: want 200, got %d (%s)", resp.StatusCode, body)
	}
	var got pruneResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.VersionsRemoved) == 0 {
		t.Errorf("prune: want non-empty versions_removed for abandoned fork tail, got %+v", got)
	}
}

// TestServePrune_NoOpEmptyArray: linear history, no abandons, valid
// policy that nothing exceeds → 200 with empty (not nil)
// versions_removed array.
func TestServePrune_NoOpEmptyArray(t *testing.T) {
	srv, cleanup := newPruningTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1") // 3 linear versions, single live head.

	// max_versions=10 — nothing exceeds, so no-op. Spec §5 calls for
	// empty []string array, not null.
	resp := doJSON(t, "POST", srv.URL+"/notes/n1/prune", map[string]any{
		"max_versions": 10,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("prune(no-op): want 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// Wire shape MUST be `"versions_removed": []`, not `null`.
	if !contains(body, []byte(`"versions_removed":[]`)) {
		t.Errorf("prune(no-op): want versions_removed:[] in body, got %s", body)
	}
	var got pruneResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.VersionsRemoved) != 0 {
		t.Errorf("prune(no-op): want empty versions_removed, got %v", got.VersionsRemoved)
	}
	if got.BlobsFreed != 0 || got.BytesFreed != 0 {
		t.Errorf("prune(no-op): want zero blobs_freed/bytes_freed, got %+v", got)
	}
}

// TestServePrune_BothZero400: spec §5 explicitly rejects "policy
// with both bounds zero" — the no-op case is shape-ambiguous against
// "policy misconfigured" otherwise.
func TestServePrune_BothZero400(t *testing.T) {
	srv, cleanup := newPruningTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1")

	// Empty body — both fields default to 0 → 400.
	resp := doJSON(t, "POST", srv.URL+"/notes/n1/prune", map[string]any{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("prune(empty body): want 400, got %d", resp.StatusCode)
	}

	// Both explicit zero → also 400.
	resp = doJSON(t, "POST", srv.URL+"/notes/n1/prune", map[string]any{
		"max_versions":    0,
		"max_age_seconds": 0,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("prune(both zero): want 400, got %d", resp.StatusCode)
	}
}

// TestServePrune_UnknownDoc404 confirms the 404 mapping for a
// non-existent (type, id).
func TestServePrune_UnknownDoc404(t *testing.T) {
	srv, cleanup := newPruningTestServer(t)
	defer cleanup()

	resp := doJSON(t, "POST", srv.URL+"/notes/missing/prune", map[string]any{
		"max_versions": 1,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("prune(missing): want 404, got %d", resp.StatusCode)
	}
}

// TestServeAbandon_HappyPath: doc with two heads (after fork), POST
// abandon → 200 empty body. Subsequent /branches?live=1 returns one
// head.
func TestServeAbandon_HappyPath(t *testing.T) {
	srv, cleanup := newPruningTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1")
	r := doJSON(t, "POST", srv.URL+"/notes/n1/fork", map[string]any{"from_seq": 1})
	r.Body.Close()

	// Abandon the fork tail (seq 4).
	resp := doJSON(t, "POST", srv.URL+"/notes/n1/abandon", map[string]any{"seq": 4})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("abandon: want 200, got %d (%s)", resp.StatusCode, body)
	}

	// /branches?live=1 returns 1 head; default returns 2.
	bresp := doJSON(t, "GET", srv.URL+"/notes/n1/branches?live=1", nil)
	defer bresp.Body.Close()
	if bresp.StatusCode != http.StatusOK {
		t.Fatalf("branches?live=1: want 200, got %d", bresp.StatusCode)
	}
	var live struct {
		Heads []map[string]any `json:"heads"`
	}
	if err := json.NewDecoder(bresp.Body).Decode(&live); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(live.Heads) != 1 {
		t.Errorf("branches?live=1: want 1 live head, got %d (%+v)", len(live.Heads), live.Heads)
	}

	dresp := doJSON(t, "GET", srv.URL+"/notes/n1/branches", nil)
	defer dresp.Body.Close()
	var all struct {
		Heads []map[string]any `json:"heads"`
	}
	if err := json.NewDecoder(dresp.Body).Decode(&all); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(all.Heads) != 2 {
		t.Errorf("branches(default): want 2 heads, got %d", len(all.Heads))
	}
	// At least one head must carry "live": false now that one is dead.
	dead := 0
	for _, h := range all.Heads {
		if v, ok := h["live"]; ok {
			if b, _ := v.(bool); !b {
				dead++
			}
		}
	}
	if dead != 1 {
		t.Errorf("branches(default): want exactly 1 dead head with live=false, got %d", dead)
	}
}

// TestServeAbandon_Idempotent: abandoning the same head twice
// returns 200 both times (spec §5 idempotent contract).
func TestServeAbandon_Idempotent(t *testing.T) {
	srv, cleanup := newPruningTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1")
	r := doJSON(t, "POST", srv.URL+"/notes/n1/fork", map[string]any{"from_seq": 1})
	r.Body.Close()

	for i := 0; i < 2; i++ {
		resp := doJSON(t, "POST", srv.URL+"/notes/n1/abandon", map[string]any{"seq": 4})
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("abandon(call %d): want 200 (idempotent), got %d", i+1, resp.StatusCode)
		}
	}
}

// TestServeAbandon_NotAHead409: abandoning a non-head seq (a
// version with children) → 409 ErrNotAHead.
func TestServeAbandon_NotAHead409(t *testing.T) {
	srv, cleanup := newPruningTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1") // seq 1, 2, 3.

	// seq 1 has child seq 2 → not a head → 409.
	resp := doJSON(t, "POST", srv.URL+"/notes/n1/abandon", map[string]any{"seq": 1})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("abandon(non-head): want 409, got %d", resp.StatusCode)
	}
}

// TestServeAbandon_LastLiveHead409: doc with a single live head,
// abandon it → 409 ErrCannotAbandonLastLiveHead.
func TestServeAbandon_LastLiveHead409(t *testing.T) {
	srv, cleanup := newPruningTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1") // single live head at seq 3.

	resp := doJSON(t, "POST", srv.URL+"/notes/n1/abandon", map[string]any{"seq": 3})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("abandon(last live head): want 409, got %d", resp.StatusCode)
	}
}

// TestServeAbandon_UnknownDoc404 confirms 404 mapping when the
// document does not exist.
func TestServeAbandon_UnknownDoc404(t *testing.T) {
	srv, cleanup := newPruningTestServer(t)
	defer cleanup()

	resp := doJSON(t, "POST", srv.URL+"/notes/missing/abandon", map[string]any{"seq": 1})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("abandon(missing doc): want 404, got %d", resp.StatusCode)
	}
}

// TestServeAbandon_UnknownSeq404: known doc, but seq does not exist
// → 404. Spec §5 lumps "doc unknown" and "seq unknown for known
// doc" under the same 404 status.
func TestServeAbandon_UnknownSeq404(t *testing.T) {
	srv, cleanup := newPruningTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1")

	resp := doJSON(t, "POST", srv.URL+"/notes/n1/abandon", map[string]any{"seq": 99})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("abandon(unknown seq): want 404, got %d", resp.StatusCode)
	}
}

// TestServeAbandon_InvalidSeq400: seq <= 0 is rejected at the
// handler-level guard before reaching the engine — matches /fork's
// shape for the same input class.
func TestServeAbandon_InvalidSeq400(t *testing.T) {
	srv, cleanup := newPruningTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1")

	resp := doJSON(t, "POST", srv.URL+"/notes/n1/abandon", map[string]any{"seq": 0})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("abandon(seq=0): want 400, got %d", resp.StatusCode)
	}
}

// TestServeBranches_LiveQueryFiltersDead asserts the central
// /branches?live=1 contract: default returns ALL heads (with dead
// ones carrying "live": false), ?live=1 filters to live only.
func TestServeBranches_LiveQueryFiltersDead(t *testing.T) {
	srv, cleanup := newPruningTestServer(t)
	defer cleanup()

	seedAbandonedForkTail(t, srv.URL, "notes", "n1")

	// Default: 2 heads, one with live=false.
	dresp := doJSON(t, "GET", srv.URL+"/notes/n1/branches", nil)
	defer dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK {
		t.Fatalf("branches(default): want 200, got %d", dresp.StatusCode)
	}
	body, _ := io.ReadAll(dresp.Body)
	var all struct {
		Heads []map[string]any `json:"heads"`
	}
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(all.Heads) != 2 {
		t.Errorf("branches(default): want 2 heads, got %d (%s)", len(all.Heads), body)
	}
	// Exactly one head has live=false (the abandoned fork tail).
	deadCount := 0
	for _, h := range all.Heads {
		if v, ok := h["live"]; ok {
			if b, _ := v.(bool); !b {
				deadCount++
			}
		}
	}
	if deadCount != 1 {
		t.Errorf("branches(default): want exactly 1 dead head with live=false, got %d (body=%s)", deadCount, body)
	}

	// ?live=1: 1 head (linear tip). The live=false key MUST NOT
	// appear (filtered out, and the surviving head is live).
	lresp := doJSON(t, "GET", srv.URL+"/notes/n1/branches?live=1", nil)
	defer lresp.Body.Close()
	if lresp.StatusCode != http.StatusOK {
		t.Fatalf("branches?live=1: want 200, got %d", lresp.StatusCode)
	}
	lbody, _ := io.ReadAll(lresp.Body)
	if contains(lbody, []byte(`"live":false`)) {
		t.Errorf("branches?live=1: must not contain live=false head, got %s", lbody)
	}
	var live struct {
		Heads []map[string]any `json:"heads"`
	}
	if err := json.Unmarshal(lbody, &live); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(live.Heads) != 1 {
		t.Errorf("branches?live=1: want 1 live head, got %d", len(live.Heads))
	}
}

// contains is a tiny non-allocating substring check — avoids
// pulling bytes.Contains into the import set when we only need it
// for assertion helpers in this file.
func contains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	if len(haystack) < len(needle) {
		return false
	}
outer:
	for i := 0; i <= len(haystack)-len(needle); i++ {
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				continue outer
			}
		}
		return true
	}
	return false
}
