package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"testing"
	"time"
)

// HTTP integration coverage for the branching wire contract introduced
// by the engine-versioned-branching track. T-0397 wires the routes in
// serve.go (List branches / Fork / Merge / History?topology=1);
// T-0398 (this file) drives them end-to-end through a real `kit serve`
// binary against an on-disk SQLite database.
//
// Three scenarios:
//
//   - TestServeBranching_FullCycle: exercises Create -> Update^2 ->
//     Fork -> Update -> Merge against the live HTTP routes and asserts
//     the wire shapes from spec §5 (heads count, parent_ids on the
//     merge tip, version count via History, topology payload shape).
//
//   - TestServeBranching_RestartPreservesBranchedHistory: builds a
//     branched + merged DAG, kills the server, restarts a fresh
//     process pointed at the same --data directory, and asserts the
//     /branches and /history?topology=1 responses are byte-equivalent
//     pre/post restart. This is the durability proof for branched
//     state on the SQLite backend.
//
//   - TestServeBranching_ErrorMapping: out-of-range fromSeq/sourceSeq/
//     targetSeq + missing-doc paths. Status codes match spec §5
//     (404 for missing document, 409 for out-of-range seq) and the
//     error envelope mirrors the existing /revert handler shape
//     ({"error": "..."}).

// branchHead is the per-head wire shape from GET /:type/:id/branches.
type branchHead struct {
	VersionID string   `json:"version_id"`
	Seq       int      `json:"seq"`
	ParentIDs []string `json:"parent_ids"`
	Timestamp string   `json:"timestamp"`
}

type branchesPayload struct {
	Heads []branchHead `json:"heads"`
}

type forkRequest struct {
	FromSeq int `json:"from_seq"`
}

type mergeRequest struct {
	SourceSeq int             `json:"source_seq"`
	TargetSeq int             `json:"target_seq"`
	Data      json.RawMessage `json:"data"`
}

// versionWire mirrors the existing /history payload from
// serve_history_test.go so we can reuse the linear shape.
type versionWire struct {
	Version   int             `json:"version"`
	Data      json.RawMessage `json:"data"`
	Timestamp string          `json:"timestamp"`
	Operation string          `json:"operation"`
}

// topologyEntry is the per-version wire shape inside the
// History?topology=1 response (spec §5).
type topologyEntry struct {
	VersionID string   `json:"version_id"`
	Seq       int      `json:"seq"`
	ParentIDs []string `json:"parent_ids"`
	Timestamp string   `json:"timestamp"`
}

type topologyPayload struct {
	Heads    []string        `json:"heads"`
	Versions []topologyEntry `json:"versions"`
}

// branchClient bundles the small set of HTTP helpers the branching
// integration tests need so individual cases stay readable. All
// helpers Fatal on transport / decode errors — the tests are
// integration-grade smoke; a transport flake is a real failure here,
// not something to swallow.
type branchClient struct {
	t             *testing.T
	base          string
	token         string
	shutdownToken string
	client        *http.Client
	docType       string
}

func newBranchClient(t *testing.T, base, token, shutdownToken, docType string) *branchClient {
	return &branchClient{
		t:             t,
		base:          base,
		token:         token,
		shutdownToken: shutdownToken,
		client:        &http.Client{Timeout: 5 * time.Second},
		docType:       docType,
	}
}

func (c *branchClient) authedDo(method, url, contentType string, body io.Reader) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		c.t.Fatalf("build %s %s: %v", method, url, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func (c *branchClient) create(id string, payload string) {
	c.t.Helper()
	url := fmt.Sprintf("%s/%s/", c.base, c.docType)
	resp := c.authedDo("POST", url, "application/json", bytes.NewReader([]byte(payload)))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		c.t.Fatalf("create %s/%s: want 201, got %d (%s)", c.docType, id, resp.StatusCode, body)
	}
}

func (c *branchClient) update(id string, payload string) {
	c.t.Helper()
	url := fmt.Sprintf("%s/%s/%s", c.base, c.docType, id)
	resp := c.authedDo("PUT", url, "application/json", bytes.NewReader([]byte(payload)))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.t.Fatalf("update %s/%s: want 200, got %d (%s)", c.docType, id, resp.StatusCode, body)
	}
}

func (c *branchClient) fork(id string, fromSeq int) branchHead {
	c.t.Helper()
	url := fmt.Sprintf("%s/%s/%s/fork", c.base, c.docType, id)
	body, _ := json.Marshal(forkRequest{FromSeq: fromSeq})
	resp := c.authedDo("POST", url, "application/json", bytes.NewReader(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		c.t.Fatalf("fork %s/%s from_seq=%d: want 201, got %d (%s)", c.docType, id, fromSeq, resp.StatusCode, raw)
	}
	var v branchHead
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		c.t.Fatalf("fork decode: %v", err)
	}
	return v
}

func (c *branchClient) merge(id string, sourceSeq, targetSeq int, data string) branchHead {
	c.t.Helper()
	url := fmt.Sprintf("%s/%s/%s/merge", c.base, c.docType, id)
	body, _ := json.Marshal(mergeRequest{
		SourceSeq: sourceSeq,
		TargetSeq: targetSeq,
		Data:      json.RawMessage(data),
	})
	resp := c.authedDo("POST", url, "application/json", bytes.NewReader(body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		c.t.Fatalf("merge %s/%s src=%d tgt=%d: want 201, got %d (%s)", c.docType, id, sourceSeq, targetSeq, resp.StatusCode, raw)
	}
	var v branchHead
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		c.t.Fatalf("merge decode: %v", err)
	}
	return v
}

func (c *branchClient) branches(id string) branchesPayload {
	c.t.Helper()
	url := fmt.Sprintf("%s/%s/%s/branches", c.base, c.docType, id)
	resp := c.authedDo("GET", url, "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		c.t.Fatalf("branches %s/%s: want 200, got %d (%s)", c.docType, id, resp.StatusCode, raw)
	}
	var p branchesPayload
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		c.t.Fatalf("branches decode: %v", err)
	}
	return p
}

func (c *branchClient) history(id string) []versionWire {
	c.t.Helper()
	url := fmt.Sprintf("%s/%s/%s/history", c.base, c.docType, id)
	resp := c.authedDo("GET", url, "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		c.t.Fatalf("history %s/%s: want 200, got %d (%s)", c.docType, id, resp.StatusCode, raw)
	}
	var p struct {
		Versions []versionWire `json:"versions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		c.t.Fatalf("history decode: %v", err)
	}
	return p.Versions
}

func (c *branchClient) historyTopology(id string) topologyPayload {
	c.t.Helper()
	url := fmt.Sprintf("%s/%s/%s/history?topology=1", c.base, c.docType, id)
	resp := c.authedDo("GET", url, "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		c.t.Fatalf("history?topology=1 %s/%s: want 200, got %d (%s)", c.docType, id, resp.StatusCode, raw)
	}
	var p topologyPayload
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		c.t.Fatalf("topology decode: %v", err)
	}
	return p
}

func (c *branchClient) shutdown() {
	c.t.Helper()
	url := c.base + "/shutdown"
	req, _ := http.NewRequest("POST", url, nil)
	req.Header.Set("Authorization", "Bearer "+c.shutdownToken)
	if resp, err := c.client.Do(req); err == nil {
		resp.Body.Close()
	}
}

// buildBranchedHistory drives the routes through the canonical
// Create -> Update^2 -> Fork(@2) -> Update -> Merge sequence used by
// both the full-cycle and restart-durability tests. It returns the
// merge tip head so callers can sanity-check it directly.
func buildBranchedHistory(c *branchClient, id string) branchHead {
	c.t.Helper()
	c.create(id, fmt.Sprintf(`{"id":%q,"title":"v1"}`, id))
	c.update(id, fmt.Sprintf(`{"id":%q,"title":"v2"}`, id))
	c.update(id, fmt.Sprintf(`{"id":%q,"title":"v3"}`, id))

	// Fork from seq=2 — the middle of the linear chain. After this
	// the document has 4 versions: 1, 2, 3 (linear tip), 4 (fork tip
	// duplicating seq=2's snapshot).
	forkTip := c.fork(id, 2)
	if forkTip.Seq != 4 {
		c.t.Fatalf("fork tip: want seq=4, got %d", forkTip.Seq)
	}

	// Update on the fork tip extends seq=4 to seq=5.
	c.update(id, fmt.Sprintf(`{"id":%q,"title":"v5-on-fork"}`, id))

	// At this point branches() should report 2 heads: seq=3 (the old
	// linear tip) and seq=5 (the new fork tip).
	pre := c.branches(id)
	if len(pre.Heads) != 2 {
		c.t.Fatalf("after fork+update: want 2 heads, got %d (%+v)", len(pre.Heads), pre.Heads)
	}

	// Merge the two heads. Source=5 (fork tip), target=3 (linear tip).
	mergeTip := c.merge(id, 5, 3, fmt.Sprintf(`{"id":%q,"title":"merged"}`, id))
	if mergeTip.Seq != 6 {
		c.t.Fatalf("merge tip: want seq=6, got %d", mergeTip.Seq)
	}
	if len(mergeTip.ParentIDs) != 2 {
		c.t.Fatalf("merge tip: want 2 parent_ids, got %d (%+v)", len(mergeTip.ParentIDs), mergeTip.ParentIDs)
	}
	return mergeTip
}

func TestServeBranching_FullCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E in short mode")
	}

	bin := buildBinary(t)
	info, cleanup := startServer(t, bin)
	defer cleanup()

	c := newBranchClient(t, baseURL(info), info.Token, info.ShutdownToken, "notes")
	const id = "fullcycle"

	mergeTip := buildBranchedHistory(c, id)

	// Post-merge: exactly one head, and that head IS the merge tip.
	post := c.branches(id)
	if len(post.Heads) != 1 {
		t.Fatalf("after merge: want 1 head, got %d (%+v)", len(post.Heads), post.Heads)
	}
	if post.Heads[0].VersionID != mergeTip.VersionID {
		t.Fatalf("after merge: head version_id mismatch, want %q got %q", mergeTip.VersionID, post.Heads[0].VersionID)
	}
	if post.Heads[0].Seq != 6 {
		t.Fatalf("after merge: head seq, want 6 got %d", post.Heads[0].Seq)
	}
	if !reflect.DeepEqual(sortedCopy(post.Heads[0].ParentIDs), sortedCopy(mergeTip.ParentIDs)) {
		t.Fatalf("after merge: parent_ids mismatch, want %v got %v", mergeTip.ParentIDs, post.Heads[0].ParentIDs)
	}

	// Linear history reports 6 versions: Create + 2 Updates + Fork +
	// Update-on-fork + Merge.
	hist := c.history(id)
	if len(hist) != 6 {
		t.Fatalf("history: want 6 versions, got %d", len(hist))
	}
	// Newest first per existing /history contract; merge is seq=6.
	if hist[0].Version != 6 {
		t.Fatalf("history[0].version: want 6, got %d", hist[0].Version)
	}
	if hist[len(hist)-1].Version != 1 {
		t.Fatalf("history[last].version: want 1, got %d", hist[len(hist)-1].Version)
	}

	// Topology view: 1 head + 6 versions, and the head's parent_ids
	// match the merge tip's parents.
	topo := c.historyTopology(id)
	if len(topo.Heads) != 1 {
		t.Fatalf("topology heads: want 1, got %d", len(topo.Heads))
	}
	if topo.Heads[0] != mergeTip.VersionID {
		t.Fatalf("topology head id: want %q got %q", mergeTip.VersionID, topo.Heads[0])
	}
	if len(topo.Versions) != 6 {
		t.Fatalf("topology versions: want 6, got %d", len(topo.Versions))
	}

	// Locate the merge tip in the topology versions; assert its
	// parent_ids set matches what /merge returned.
	var mergeEntry *topologyEntry
	for i := range topo.Versions {
		if topo.Versions[i].VersionID == mergeTip.VersionID {
			mergeEntry = &topo.Versions[i]
			break
		}
	}
	if mergeEntry == nil {
		t.Fatalf("topology: merge tip %q not present in versions", mergeTip.VersionID)
	}
	if len(mergeEntry.ParentIDs) != 2 {
		t.Fatalf("topology merge tip parent_ids: want 2, got %d", len(mergeEntry.ParentIDs))
	}
	if !reflect.DeepEqual(sortedCopy(mergeEntry.ParentIDs), sortedCopy(mergeTip.ParentIDs)) {
		t.Fatalf("topology merge tip parent_ids: want %v got %v",
			mergeTip.ParentIDs, mergeEntry.ParentIDs)
	}
}

func TestServeBranching_RestartPreservesBranchedHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E in short mode")
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	const id = "restart-branched"

	var preBranches branchesPayload
	var preTopology topologyPayload

	// --- Run 1: build branched + merged history, capture wire payloads ---
	{
		info, cleanup := startServerAt(t, bin, dataDir)
		c := newBranchClient(t, baseURL(info), info.Token, info.ShutdownToken, "notes")
		buildBranchedHistory(c, id)

		preBranches = c.branches(id)
		preTopology = c.historyTopology(id)

		// Post-merge: 1 head, 6 versions. Sanity-check before relying
		// on the captures for the post-restart comparison.
		if len(preBranches.Heads) != 1 {
			cleanup()
			t.Fatalf("pre-restart: want 1 head, got %d", len(preBranches.Heads))
		}
		if len(preTopology.Versions) != 6 {
			cleanup()
			t.Fatalf("pre-restart topology: want 6 versions, got %d", len(preTopology.Versions))
		}

		// Cleanly shut down so SQLite flushes before run 2 reopens the
		// DB. /shutdown may close the connection mid-write — that's OK.
		c.shutdown()
		cleanup()
	}

	// --- Run 2: fresh process, same data dir, fetch + compare ---
	{
		info, cleanup := startServerAt(t, bin, dataDir)
		defer cleanup()
		c := newBranchClient(t, baseURL(info), info.Token, info.ShutdownToken, "notes")

		got := c.branches(id)
		if !reflect.DeepEqual(got, preBranches) {
			t.Fatalf("branches not byte-equivalent across restart\n  pre  = %+v\n  post = %+v",
				preBranches, got)
		}

		gotTopo := c.historyTopology(id)
		// Heads slice is order-deterministic on the wire (spec §5
		// shows an array; the engine returns ascending seq), so equal
		// slices are required, not just equal sets. Same for versions.
		if !reflect.DeepEqual(gotTopo, preTopology) {
			t.Fatalf("history?topology=1 not byte-equivalent across restart\n  pre  = %+v\n  post = %+v",
				preTopology, gotTopo)
		}
	}
}

func TestServeBranching_ErrorMapping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E in short mode")
	}

	bin := buildBinary(t)
	info, cleanup := startServer(t, bin)
	defer cleanup()

	c := newBranchClient(t, baseURL(info), info.Token, info.ShutdownToken, "notes")
	const id = "errors"

	// Seed two versions so we have valid seqs to mix with invalid ones.
	c.create(id, fmt.Sprintf(`{"id":%q,"title":"v1"}`, id))
	c.update(id, fmt.Sprintf(`{"id":%q,"title":"v2"}`, id))

	// envelope is the existing error shape across /history and /revert
	// handlers ({"error": "..."} via jsonError). Spec §5 doesn't lock a
	// new envelope, so we mirror what's already on the wire.
	type envelope struct {
		Error string `json:"error"`
	}

	// 1. Fork on a non-existent (type, id) → 404 + envelope.
	{
		url := fmt.Sprintf("%s/notes/missing/fork", c.base)
		body, _ := json.Marshal(forkRequest{FromSeq: 1})
		resp := c.authedDo("POST", url, "application/json", bytes.NewReader(body))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("fork(missing doc): want 404, got %d (%s)", resp.StatusCode, raw)
		}
		var e envelope
		if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
			t.Fatalf("fork(missing) envelope decode: %v", err)
		}
		if e.Error == "" {
			t.Fatalf("fork(missing) envelope: empty error string")
		}
	}

	// 2. Fork with from_seq out of range → 409 + envelope (spec §5).
	{
		url := fmt.Sprintf("%s/notes/%s/fork", c.base, id)
		body, _ := json.Marshal(forkRequest{FromSeq: 999})
		resp := c.authedDo("POST", url, "application/json", bytes.NewReader(body))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusConflict {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("fork(out-of-range): want 409, got %d (%s)", resp.StatusCode, raw)
		}
		var e envelope
		if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
			t.Fatalf("fork(out-of-range) envelope decode: %v", err)
		}
		if e.Error == "" {
			t.Fatalf("fork(out-of-range) envelope: empty error string")
		}
	}

	// 3. Merge on a non-existent doc → 404.
	{
		url := fmt.Sprintf("%s/notes/missing/merge", c.base)
		body, _ := json.Marshal(mergeRequest{
			SourceSeq: 1,
			TargetSeq: 2,
			Data:      json.RawMessage(`{}`),
		})
		resp := c.authedDo("POST", url, "application/json", bytes.NewReader(body))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("merge(missing doc): want 404, got %d (%s)", resp.StatusCode, raw)
		}
	}

	// 4. Merge with source_seq valid but target_seq out of range → 409.
	{
		url := fmt.Sprintf("%s/notes/%s/merge", c.base, id)
		body, _ := json.Marshal(mergeRequest{
			SourceSeq: 1,
			TargetSeq: 999,
			Data:      json.RawMessage(`{}`),
		})
		resp := c.authedDo("POST", url, "application/json", bytes.NewReader(body))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusConflict {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("merge(out-of-range target): want 409, got %d (%s)", resp.StatusCode, raw)
		}
	}

	// 5. Merge with source_seq out of range, target_seq valid → 409.
	{
		url := fmt.Sprintf("%s/notes/%s/merge", c.base, id)
		body, _ := json.Marshal(mergeRequest{
			SourceSeq: 999,
			TargetSeq: 1,
			Data:      json.RawMessage(`{}`),
		})
		resp := c.authedDo("POST", url, "application/json", bytes.NewReader(body))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusConflict {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("merge(out-of-range source): want 409, got %d (%s)", resp.StatusCode, raw)
		}
	}

	// 6. GET /branches on a missing doc → 404 (spec §5).
	{
		url := fmt.Sprintf("%s/notes/missing/branches", c.base)
		resp := c.authedDo("GET", url, "", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("branches(missing): want 404, got %d (%s)", resp.StatusCode, raw)
		}
	}
}

// sortedCopy returns a sorted copy of s without mutating it. Used to
// compare parent_id slices order-independently — the storage contract
// preserves insertion order, but assertions across two responses
// (e.g. /merge response vs /branches head) can legitimately see the
// same set in different orders depending on which loader path
// hydrated them.
func sortedCopy(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	sort.Strings(out)
	return out
}
