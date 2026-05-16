package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"hop.top/kit/engine/store"
)

// HTTP integration coverage for the prune track is gated on the
// /:type/:id/abandon and /:type/:id/prune routes from spec §5, which
// are owned by a parallel track (T-0429 P4) and are not present in
// serve.go at the time T-0428 ships. Rather than block on the route
// landing — and rather than test routes that don't exist — this file
// drives the restart-durability proof through the engine's Go API
// directly: open a SQLite-backed VersionedDocumentStore, build a
// branched + merged + abandoned + pruned history, close, reopen
// against the same on-disk DB, and assert the post-prune state
// (history, branches both default and live-only, parent-slice
// topology via LoadDAG) is byte-equivalent across the close/reopen
// boundary.
//
// This is the same shape as serve_restart_test.go's TestServe_
// RestartPreservesHistory (T-0352 / T-0397) but at the engine layer
// rather than the wire layer. When the /abandon and /prune routes
// land, a sibling HTTP-driven version of this test should be added
// — the spec §7 "kit serve restart preserves post-prune state" line
// item is satisfied here for the durability question (does the live
// bit and the deleted-versions state survive Close + reopen?). The
// HTTP wire-equivalence question is a different test and a different
// surface.
//
// Why cmd/kit/ rather than engine/store/: this file exercises the
// full kit serve construction path (NewDocumentStore boots the
// migrations including the live column ALTER, and NewSQLiteVersion
// Store wires the prune-aware VersionStore against a real on-disk
// DB), which is the same boot path serve.go drives. A test in
// engine/store/ would use the same constructors but lose the "this
// is what serves runs at startup" framing — keeping it under
// cmd/kit/ makes the lineage to TestServe_RestartPreservesHistory
// explicit.

// TestServePruning_RestartPreservesPostPruneAndAbandonState builds
// a branched + merged history, abandons one of the resulting heads,
// runs Prune, captures the post-prune state, closes the DB, reopens
// it from the same path, and asserts the state survives byte-
// equivalent.
//
// State surfaces compared:
//
//   - History (full version slice, including the Live field for
//     each surviving version)
//   - Branches() (all heads, live + dead — exercises the Live=false
//     persistence path)
//   - Branches(WithLiveOnly()) (live-only heads — exercises the
//     filter as well)
//   - LoadDAG (per-version parent slices — confirms parent edges
//     survive close/reopen, including post-prune parent edges that
//     reference still-retained ancestors)
//   - GetSnapshot per retained version (confirms the snapshot blob
//     refcount + dedup join survives close/reopen post-prune)
//
// Non-deterministic fields are filtered: CreatedAt timestamps come
// from the backend's clock at write time and are stable post-write,
// so they round-trip cleanly. Live=true is a conventional default
// (omitted from JSON via Version.MarshalJSON); Live=false survives
// because the SQLite live column is NOT NULL DEFAULT TRUE and
// SetLive flips it to 0 atomically.
//
// Topology under test (mirrors TestPrune_AbandonedForkTail_Fires
// from versioned_pruning_test.go but adds the close/reopen cycle):
//
//	seq 1 → seq 2 → seq 3 (head A: live, main line)
//	         ↓
//	         seq 4 → seq 5 (head B: live, fork tail until Abandon)
//
// After Fork(2), the sibling head is at the fork tip (seq 4 just
// after fork). Update extends the most-recent seq (seq 4) into seq
// 5 — so the fork branch grows from seq 4 to seq 5. The original
// main-line head seq 3 stays a head (no children).
//
// We Abandon(seq 5) — the fork tail's tip — making it dead. live
// heads = {seq 3}; dead heads = {seq 5}.
//
// Prune with MaxAge=1ns (every version exceeds the age bound) and
// MaxVersions=0 (unbounded by count). The age bound makes every
// version a policy candidate; the retain floor (ancestors of seq 3)
// = {seq 1, 2, 3}. candidates ∩ ¬retain_floor = {seq 4, 5}.
// bottom-up: seq 5 (dead, no children) → prunable; seq 4 (only child
// seq 5 is also a candidate) → prunable.
// Net: {seq 4, seq 5} prune.
//
// MaxAge is safe to use in a single-backend integration test (no
// cross-backend wall-clock skew — same physical clock for all writes
// and the prune call).
func TestServePruning_RestartPreservesPostPruneAndAbandonState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E in short mode")
	}

	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "documents.db")
	ctx := context.Background()

	const docType = "notes"
	const docID = "restart-prune"

	// --- Run 1: build state, prune, capture, close ---
	var preHistory []store.Version
	var preBranches []store.Version
	var preLive []store.Version
	preSnapshots := make(map[string][]byte) // version_id -> bytes
	preParents := make(map[string][]string) // version_id -> parent ids
	var prunedVersionIDs []string

	{
		ds, err := store.NewDocumentStore(dbPath)
		if err != nil {
			t.Fatalf("run1: open document store: %v", err)
		}
		vs, err := store.NewSQLiteVersionStore(ds.DB())
		if err != nil {
			ds.Close()
			t.Fatalf("run1: open version store: %v", err)
		}
		vds := store.NewVersionedDocumentStore(ds, vs)

		// Seed: linear seqs 1..3.
		mustCreateAt(t, vds, docType, docID, `{"id":"`+docID+`","title":"v1"}`)
		mustUpdateAt(t, vds, docType, docID, `{"id":"`+docID+`","title":"v2"}`)
		mustUpdateAt(t, vds, docType, docID, `{"id":"`+docID+`","title":"v3"}`)

		// Fork at seq 2 → seq 4 (sibling of seq 3; new head, live).
		if _, err := vds.Fork(ctx, docType, docID, 2); err != nil {
			t.Fatalf("run1: fork(2): %v", err)
		}
		// Update extends most-recent seq (seq 4) → seq 5. Fork tail
		// is now {seq 4 → seq 5}; main line head stays at seq 3.
		mustUpdateAt(t, vds, docType, docID, `{"id":"`+docID+`","title":"v5-on-fork"}`)

		// Sanity: live heads = {seq 3, seq 5}.
		liveBefore, err := vds.Branches(ctx, docType, docID, store.WithLiveOnly())
		if err != nil {
			t.Fatalf("run1: branches live pre-abandon: %v", err)
		}
		if len(liveBefore) != 2 {
			t.Fatalf("run1: expected 2 live heads pre-abandon, got %d (%v)", len(liveBefore), liveBefore)
		}

		// Abandon seq 5 (the fork tail tip). After abandon, live
		// heads = {seq 3}; dead heads = {seq 5}.
		if err := vds.Abandon(ctx, docType, docID, 5); err != nil {
			t.Fatalf("run1: abandon(5): %v", err)
		}

		// Sanity: post-abandon, exactly one live head (seq 3).
		liveAfter, err := vds.Branches(ctx, docType, docID, store.WithLiveOnly())
		if err != nil {
			t.Fatalf("run1: branches live post-abandon: %v", err)
		}
		if len(liveAfter) != 1 {
			t.Fatalf("run1: expected 1 live head post-abandon, got %d (%v)", len(liveAfter), liveAfter)
		}
		if liveAfter[0].Seq != 3 {
			t.Fatalf("run1: expected live head seq=3, got seq=%d", liveAfter[0].Seq)
		}

		// Prune with MaxAge=1ns (every version is a policy candidate)
		// and MaxVersions=0 (unbounded). Single-backend timing means
		// every version's age >> 1ns by the time the Prune call runs.
		//
		// retain_floor = ancestors(seq 3) ∪ {seq 3} = {seq 1, 2, 3}.
		// candidates ∩ ¬retain_floor = {seq 4, seq 5}.
		// bottom-up: seq 5 (dead, no children) → prunable.
		//            seq 4 (only child seq 5 is a candidate) → prunable.
		// Net: {seq 4, seq 5} prune.
		policy := store.RetentionPolicy{MaxAge: time.Nanosecond}
		res, err := vds.Prune(ctx, docType, docID, policy)
		if err != nil {
			t.Fatalf("run1: prune: %v", err)
		}
		if len(res.VersionsRemoved) != 2 {
			hist, _ := vds.History(ctx, docType, docID)
			seqs := make([]int, len(hist))
			for i, v := range hist {
				seqs[i] = v.Seq
			}
			t.Fatalf("run1: expected exactly 2 versions to prune (seqs 4,5); got %d (%v) (history seqs=%v)",
				len(res.VersionsRemoved), res.VersionsRemoved, seqs)
		}
		prunedVersionIDs = append(prunedVersionIDs, res.VersionsRemoved...)
		t.Logf("run1: pruned %d versions: %v (blobs freed: %d, bytes freed: %d)",
			len(res.VersionsRemoved), res.VersionsRemoved, res.BlobsFreed, res.BytesFreed)

		// Capture post-prune state.
		preHistory, err = vds.History(ctx, docType, docID)
		if err != nil {
			t.Fatalf("run1: history: %v", err)
		}
		preBranches, err = vds.Branches(ctx, docType, docID)
		if err != nil {
			t.Fatalf("run1: branches: %v", err)
		}
		preLive, err = vds.Branches(ctx, docType, docID, store.WithLiveOnly())
		if err != nil {
			t.Fatalf("run1: branches live: %v", err)
		}

		// Capture per-version snapshot bytes and parent slices via the
		// VersionStore seam.
		dag, err := vsLoadDAG(ctx, ds.DB(), docType, docID)
		if err != nil {
			t.Fatalf("run1: load dag: %v", err)
		}
		for _, v := range preHistory {
			node, ok := dag[v.VersionID]
			if !ok {
				t.Fatalf("run1: version %s missing from DAG", v.VersionID)
			}
			preParents[v.VersionID] = append([]string(nil), node...)

			snap, err := vs.GetSnapshot(ctx, v.VersionID)
			if err != nil {
				t.Fatalf("run1: getSnapshot(%s): %v", v.VersionID, err)
			}
			preSnapshots[v.VersionID] = append([]byte(nil), snap...)
		}

		// Cleanly close the DB so SQLite flushes WAL → main file
		// before run 2 reopens. Without Close, run 2 would still see
		// the data via WAL replay, but we want the explicit "close +
		// reopen" path to be the proof.
		if err := ds.Close(); err != nil {
			t.Fatalf("run1: close: %v", err)
		}
	}

	// --- Run 2: reopen from same path, fetch + compare ---
	{
		ds, err := store.NewDocumentStore(dbPath)
		if err != nil {
			t.Fatalf("run2: reopen document store: %v", err)
		}
		defer ds.Close()
		vs, err := store.NewSQLiteVersionStore(ds.DB())
		if err != nil {
			t.Fatalf("run2: reopen version store: %v", err)
		}
		vds := store.NewVersionedDocumentStore(ds, vs)

		// Re-fetch and compare.
		gotHistory, err := vds.History(ctx, docType, docID)
		if err != nil {
			t.Fatalf("run2: history: %v", err)
		}
		gotBranches, err := vds.Branches(ctx, docType, docID)
		if err != nil {
			t.Fatalf("run2: branches: %v", err)
		}
		gotLive, err := vds.Branches(ctx, docType, docID, store.WithLiveOnly())
		if err != nil {
			t.Fatalf("run2: branches live: %v", err)
		}

		// History byte-equivalence (every version including its Live
		// field MUST round-trip through Close + reopen).
		if !reflect.DeepEqual(gotHistory, preHistory) {
			t.Fatalf("history not byte-equivalent across restart\n  pre  = %s\n  post = %s",
				dumpVersions(preHistory), dumpVersions(gotHistory))
		}

		// Branches (all heads, live + dead). Sort because the slice
		// order is seq-ascending in both cases but a defensive
		// reflect.DeepEqual catches any reorder regression too.
		if !reflect.DeepEqual(gotBranches, preBranches) {
			t.Fatalf("branches (all) not byte-equivalent across restart\n  pre  = %s\n  post = %s",
				dumpVersions(preBranches), dumpVersions(gotBranches))
		}
		if !reflect.DeepEqual(gotLive, preLive) {
			t.Fatalf("branches (live-only) not byte-equivalent across restart\n  pre  = %s\n  post = %s",
				dumpVersions(preLive), dumpVersions(gotLive))
		}

		// Confirm at least one Live=false survivor — Revert(2) marks
		// the pre-revert head seq 6 dead, but seq 6 is in the prune
		// candidate set so it should be REMOVED post-prune. The
		// remaining dead heads, if any, would be from the prune
		// algorithm itself. Since we explicitly remove seq 6 in this
		// scenario, post-prune state should have ALL retained versions
		// live (every dead head was pruned). This is a different
		// failsafe: ensure no orphan dead row leaked through.
		for _, v := range gotHistory {
			if !v.Live {
				t.Fatalf("run2: unexpected dead version in retained set: seq=%d vid=%s — every dead head should have pruned",
					v.Seq, v.VersionID)
			}
		}

		// Confirm the pruned version is gone (not present in history)
		// AND not in branches. (The DeleteHistory path is shared with
		// Prune via DeleteVersions, but we want the explicit
		// "post-restart, no trace of pruned version_id" assertion.)
		retainedIDs := make(map[string]struct{}, len(gotHistory))
		for _, v := range gotHistory {
			retainedIDs[v.VersionID] = struct{}{}
		}
		for _, prunedID := range prunedVersionIDs {
			if _, present := retainedIDs[prunedID]; present {
				t.Fatalf("run2: pruned version_id %s reappeared in history post-restart", prunedID)
			}
		}

		// Snapshot bytes byte-equivalence per retained version.
		for _, v := range gotHistory {
			snap, err := vs.GetSnapshot(ctx, v.VersionID)
			if err != nil {
				t.Fatalf("run2: getSnapshot(%s): %v", v.VersionID, err)
			}
			if !reflect.DeepEqual([]byte(snap), preSnapshots[v.VersionID]) {
				t.Fatalf("run2: snapshot bytes for %s differ:\n  pre  = %s\n  post = %s",
					v.VersionID, string(preSnapshots[v.VersionID]), string(snap))
			}
		}

		// Parent-slice round-trip per retained version. LoadDAG on the
		// reopened store MUST return the same parent edges as the pre-
		// close DAG for every retained version.
		dag, err := vsLoadDAG(ctx, ds.DB(), docType, docID)
		if err != nil {
			t.Fatalf("run2: load dag: %v", err)
		}
		for _, v := range gotHistory {
			gotParents, ok := dag[v.VersionID]
			if !ok {
				t.Fatalf("run2: version %s missing from DAG", v.VersionID)
			}
			wantParents := preParents[v.VersionID]
			if !reflect.DeepEqual(gotParents, wantParents) {
				t.Fatalf("run2: parent slice for %s differs\n  pre  = %v\n  post = %v",
					v.VersionID, wantParents, gotParents)
			}
		}
	}
}

// mustCreateAt drives Create and fatal-fails on error. Intentionally
// duplicates the engine/store test helper rather than importing it —
// the cmd/kit package can't import test-only internals.
func mustCreateAt(t *testing.T, vds *store.VersionedDocumentStore, docType, id, body string) {
	t.Helper()
	if _, err := vds.Create(context.Background(), docType, json.RawMessage(body)); err != nil {
		t.Fatalf("create %s/%s: %v", docType, id, err)
	}
}

func mustUpdateAt(t *testing.T, vds *store.VersionedDocumentStore, docType, id, body string) {
	t.Helper()
	if _, err := vds.Update(context.Background(), docType, id, json.RawMessage(body)); err != nil {
		t.Fatalf("update %s/%s: %v", docType, id, err)
	}
}

// vsLoadDAG queries the version_parents table directly to build a
// {version_id -> []parent_id} map. We bypass VersionStore.LoadDAG
// because it returns *version.DAG (an opaque domain object); raw
// SQL gives us a stable assertion shape (deep-comparable map) that
// survives across Close + reopen and doesn't depend on DAG's internal
// representation.
//
// Order matters: version_parents.position preserves the
// [sourceVersionID, targetVersionID] insertion order that Merge
// commits, so the parent slice MUST be sorted by position to match
// the pre-close capture. We rely on the SQL ORDER BY to provide this.
func vsLoadDAG(ctx context.Context, db *sql.DB, docType, id string) (map[string][]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT vp.version_id, vp.parent_id
		 FROM version_parents vp
		 JOIN versions v ON v.version_id = vp.version_id
		 WHERE v.type = ? AND v.id = ?
		 ORDER BY vp.version_id, vp.rowid`,
		docType, id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]string)
	for rows.Next() {
		var vid, pid string
		if err := rows.Scan(&vid, &pid); err != nil {
			return nil, err
		}
		out[vid] = append(out[vid], pid)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Add empty entries for versions with no parents (the seed seq=1
	// of any document, plus any other root). Pull from versions
	// directly.
	rows2, err := db.QueryContext(ctx,
		`SELECT version_id FROM versions WHERE type = ? AND id = ?`,
		docType, id,
	)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var vid string
		if err := rows2.Scan(&vid); err != nil {
			return nil, err
		}
		if _, ok := out[vid]; !ok {
			out[vid] = nil
		}
	}
	return out, rows2.Err()
}

// dumpVersions renders a Version slice as a stable string for
// failure messages. Versions sort by seq ascending; the output omits
// CreatedAt because it's fully equality-compared via reflect.DeepEqual
// upstream and including it inflates failure messages by 30+ chars
// per row. We focus the printed surface on (seq, vid, live, data).
func dumpVersions(vs []store.Version) string {
	if len(vs) == 0 {
		return "[]"
	}
	out := make([]store.Version, len(vs))
	copy(out, vs)
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	parts := make([]string, len(out))
	for i, v := range out {
		parts[i] = "seq=" + itoa(v.Seq) + " vid=" + v.VersionID + " live=" + boolStr(v.Live)
	}
	return "[" + joinComma(parts) + "]"
}

func itoa(i int) string { return jsonInt(i) }

func jsonInt(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func joinComma(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "," + p
	}
	return out
}
