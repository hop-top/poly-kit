package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/engine/store"
	"hop.top/kit/go/transport/api"
)

// In-process smoke tests for the branching routes (T-0397, spec
// docs/specs/engine-versioned-branching.md §5). Exhaustive end-to-end
// coverage (multi-step DAG topology, restart durability) lives in
// serve_branches_integration_test.go (T-0398). These tests focus on
// happy-path wire shape + basic error mapping for each route, plus
// the backward-compat guarantee on /history without ?topology.
//
// We wire registerBranchingRoutes + registerHistoryRoutes against
// real in-package stores rather than mocking — the handler logic is
// thin enough that mocking would be more code than it's worth, and
// this keeps the test honest about the actual store contracts the
// handlers depend on.

func newBranchingTestServer(t *testing.T) (*httptest.Server, func()) {
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

	srv := httptest.NewServer(router)
	cleanup := func() {
		srv.Close()
		_ = ds.Close()
	}
	return srv, cleanup
}

func doJSON(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("build req: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// seedDoc creates a doc with id `id` and updates it twice so the
// version DAG has three linear versions (seq 1, 2, 3). Returns the
// final document so callers can read its id back if they passed
// auto-generation.
func seedDoc(t *testing.T, base, docType, id string) {
	t.Helper()
	resp := doJSON(t, "POST", base+"/"+docType+"/", map[string]any{
		"id":   id,
		"data": map[string]string{"title": "v1"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed create: want 201, got %d", resp.StatusCode)
	}
	for _, payload := range []map[string]any{
		{"id": id, "data": map[string]string{"title": "v2"}},
		{"id": id, "data": map[string]string{"title": "v3"}},
	} {
		r := doJSON(t, "PUT", base+"/"+docType+"/"+id, payload)
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("seed update: want 200, got %d", r.StatusCode)
		}
	}
}

// TestServeBranches_ForkHappyPath exercises POST /:type/:id/fork
// with a valid from_seq, asserting the spec §5 wire shape (201,
// version_id, seq, parent_ids, timestamp).
func TestServeBranches_ForkHappyPath(t *testing.T) {
	srv, cleanup := newBranchingTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1")

	resp := doJSON(t, "POST", srv.URL+"/notes/n1/fork", map[string]any{"from_seq": 2})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("fork: want 201, got %d (%s)", resp.StatusCode, body)
	}
	var got struct {
		VersionID string   `json:"version_id"`
		Seq       int      `json:"seq"`
		ParentIDs []string `json:"parent_ids"`
		Timestamp string   `json:"timestamp"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.VersionID == "" {
		t.Errorf("fork: missing version_id")
	}
	// Seed had seq 1,2,3. Fork at seq=2 appends as seq=4.
	if got.Seq != 4 {
		t.Errorf("fork: want seq=4, got %d", got.Seq)
	}
	if len(got.ParentIDs) != 1 {
		t.Errorf("fork: want 1 parent_id, got %d (%v)", len(got.ParentIDs), got.ParentIDs)
	}
	if got.Timestamp == "" {
		t.Errorf("fork: missing timestamp")
	}
}

// TestServeBranches_ForkInvalid covers the two reject cases the
// route surfaces today: out-of-range from_seq → 409 (mirrors
// /revert), and from_seq <= 0 → 400.
func TestServeBranches_ForkInvalid(t *testing.T) {
	srv, cleanup := newBranchingTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1")

	// Out-of-range seq: store says "version 99 not found" → 409.
	resp := doJSON(t, "POST", srv.URL+"/notes/n1/fork", map[string]any{"from_seq": 99})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("fork(out-of-range): want 409, got %d", resp.StatusCode)
	}

	// Non-positive seq: handler-level guard → 400.
	resp = doJSON(t, "POST", srv.URL+"/notes/n1/fork", map[string]any{"from_seq": 0})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("fork(seq=0): want 400, got %d", resp.StatusCode)
	}

	// Unknown doc → 404.
	resp = doJSON(t, "POST", srv.URL+"/notes/missing/fork", map[string]any{"from_seq": 1})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("fork(missing doc): want 404, got %d", resp.StatusCode)
	}
}

// TestServeBranches_BranchesAfterFork exercises GET /:type/:id/branches
// against a forked document: two heads expected, ordered most-recent-first.
func TestServeBranches_BranchesAfterFork(t *testing.T) {
	srv, cleanup := newBranchingTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1")

	// Fork at seq 1 → fork tip is seq 4, linear tip is seq 3.
	resp := doJSON(t, "POST", srv.URL+"/notes/n1/fork", map[string]any{"from_seq": 1})
	resp.Body.Close()

	hresp := doJSON(t, "GET", srv.URL+"/notes/n1/branches", nil)
	defer hresp.Body.Close()
	if hresp.StatusCode != http.StatusOK {
		t.Fatalf("branches: want 200, got %d", hresp.StatusCode)
	}
	var payload struct {
		Heads []struct {
			VersionID string   `json:"version_id"`
			Seq       int      `json:"seq"`
			ParentIDs []string `json:"parent_ids"`
			Timestamp string   `json:"timestamp"`
		} `json:"heads"`
	}
	if err := json.NewDecoder(hresp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Heads) != 2 {
		t.Fatalf("branches: want 2 heads, got %d (%+v)", len(payload.Heads), payload.Heads)
	}
	// Most-recent-first: fork tip (seq 4) before linear tip (seq 3).
	if payload.Heads[0].Seq != 4 {
		t.Errorf("branches[0]: want seq=4 (most-recent), got %d", payload.Heads[0].Seq)
	}
	if payload.Heads[1].Seq != 3 {
		t.Errorf("branches[1]: want seq=3, got %d", payload.Heads[1].Seq)
	}
}

// TestServeBranches_BranchesUnknownDoc covers the 404 path.
func TestServeBranches_BranchesUnknownDoc(t *testing.T) {
	srv, cleanup := newBranchingTestServer(t)
	defer cleanup()

	resp := doJSON(t, "GET", srv.URL+"/notes/nope/branches", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("branches(missing): want 404, got %d", resp.StatusCode)
	}
}

// TestServeBranches_MergeHappyPath exercises POST /:type/:id/merge
// with two valid seqs. Asserts spec wire shape and that the merge
// version has 2 parent_ids.
func TestServeBranches_MergeHappyPath(t *testing.T) {
	srv, cleanup := newBranchingTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1")

	// Fork at seq=1 → seq=4. Now seq=3 (linear tip) and seq=4 (fork
	// tip) are both heads.
	r := doJSON(t, "POST", srv.URL+"/notes/n1/fork", map[string]any{"from_seq": 1})
	r.Body.Close()

	mresp := doJSON(t, "POST", srv.URL+"/notes/n1/merge", map[string]any{
		"source_seq": 4,
		"target_seq": 3,
		"data":       map[string]string{"title": "merged"},
	})
	defer mresp.Body.Close()
	if mresp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(mresp.Body)
		t.Fatalf("merge: want 201, got %d (%s)", mresp.StatusCode, body)
	}
	var got struct {
		VersionID string   `json:"version_id"`
		Seq       int      `json:"seq"`
		ParentIDs []string `json:"parent_ids"`
		Timestamp string   `json:"timestamp"`
	}
	if err := json.NewDecoder(mresp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Seq != 5 {
		t.Errorf("merge: want seq=5, got %d", got.Seq)
	}
	if len(got.ParentIDs) != 2 {
		t.Errorf("merge: want 2 parent_ids, got %d (%v)", len(got.ParentIDs), got.ParentIDs)
	}
}

// TestServeBranches_MergeInvalid covers the rejection paths:
// out-of-range source/target seq → 409, missing data → 400.
func TestServeBranches_MergeInvalid(t *testing.T) {
	srv, cleanup := newBranchingTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1")

	// Out-of-range target → 409.
	resp := doJSON(t, "POST", srv.URL+"/notes/n1/merge", map[string]any{
		"source_seq": 1,
		"target_seq": 99,
		"data":       map[string]string{"title": "x"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("merge(out-of-range target): want 409, got %d", resp.StatusCode)
	}

	// Missing data → 400.
	resp = doJSON(t, "POST", srv.URL+"/notes/n1/merge", map[string]any{
		"source_seq": 1,
		"target_seq": 2,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("merge(no data): want 400, got %d", resp.StatusCode)
	}

	// Non-positive seq → 400.
	resp = doJSON(t, "POST", srv.URL+"/notes/n1/merge", map[string]any{
		"source_seq": 0,
		"target_seq": 2,
		"data":       map[string]string{"title": "x"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("merge(seq=0): want 400, got %d", resp.StatusCode)
	}
}

// TestServeBranches_HistoryTopology asserts that GET
// /:type/:id/history?topology=1 returns the topology envelope per
// spec §5: top-level `heads` array of version IDs plus per-version
// entries with `version_id`, `seq`, `parent_ids`, `timestamp`.
func TestServeBranches_HistoryTopology(t *testing.T) {
	srv, cleanup := newBranchingTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1")

	resp := doJSON(t, "GET", srv.URL+"/notes/n1/history?topology=1", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history(topology): want 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var payload struct {
		Heads    []string `json:"heads"`
		Versions []struct {
			VersionID string   `json:"version_id"`
			Seq       int      `json:"seq"`
			ParentIDs []string `json:"parent_ids"`
			Timestamp string   `json:"timestamp"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v (raw: %s)", err, body)
	}
	if len(payload.Versions) != 3 {
		t.Errorf("topology: want 3 versions, got %d", len(payload.Versions))
	}
	if len(payload.Heads) != 1 {
		t.Errorf("topology: linear history should have exactly 1 head, got %d", len(payload.Heads))
	}
	// Newest-first ordering preserved on topology variant too.
	if payload.Versions[0].Seq != 3 {
		t.Errorf("topology[0]: want seq=3, got %d", payload.Versions[0].Seq)
	}
	// The first (create) version has no parents.
	if payload.Versions[2].Seq != 1 {
		t.Errorf("topology[2]: want seq=1, got %d", payload.Versions[2].Seq)
	}
	if len(payload.Versions[2].ParentIDs) != 0 {
		t.Errorf("topology[seq=1].parent_ids: want empty, got %v", payload.Versions[2].ParentIDs)
	}
	// Subsequent versions have one parent each.
	if len(payload.Versions[1].ParentIDs) != 1 {
		t.Errorf("topology[seq=2].parent_ids: want 1 parent, got %d", len(payload.Versions[1].ParentIDs))
	}
	// parent_ids field must be present on every entry (`[]` not `null`).
	if !strings.Contains(string(body), `"parent_ids"`) {
		t.Errorf("topology body must contain parent_ids field; got %s", body)
	}
}

// TestServeBranches_HistoryDefaultUnchanged guarantees backward
// compatibility: GET /:type/:id/history (no topology query) returns
// the same shape T-0353 established — `versions` array with
// `version`, `data`, `timestamp`, `operation`, no `heads` key.
func TestServeBranches_HistoryDefaultUnchanged(t *testing.T) {
	srv, cleanup := newBranchingTestServer(t)
	defer cleanup()

	seedDoc(t, srv.URL, "notes", "n1")

	resp := doJSON(t, "GET", srv.URL+"/notes/n1/history", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history: want 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, has := payload["heads"]; has {
		t.Errorf("history(default): unexpected heads key — must be absent for backward compat")
	}
	versionsRaw, ok := payload["versions"]
	if !ok {
		t.Fatalf("history(default): missing versions key")
	}
	var versions []struct {
		Version   int    `json:"version"`
		Operation string `json:"operation"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(versionsRaw, &versions); err != nil {
		t.Fatalf("decode versions: %v", err)
	}
	if len(versions) != 3 {
		t.Errorf("history(default): want 3 versions, got %d", len(versions))
	}
	if versions[0].Version != 3 || versions[0].Operation != "update" {
		t.Errorf("history(default)[0]: want {version:3, operation:update}, got %+v", versions[0])
	}
	if versions[2].Version != 1 || versions[2].Operation != "create" {
		t.Errorf("history(default)[2]: want {version:1, operation:create}, got %+v", versions[2])
	}
}
