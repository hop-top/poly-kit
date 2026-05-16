package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// versioned_dedup_property_test.go is the cross-backend property
// test for content-addressed snapshot dedup added by track
// engine-snapshot-dedup. The shape mirrors
// versioned_branching_property_test.go (its immediate predecessor)
// so the two read as siblings: same fixed-seed pattern, fresh-stores-
// per-iteration, outcome-divergence-is-signal-not-flake stance.
//
// The dedup-specific addition is a "duplicate-update" op that re-
// uses an earlier op's data verbatim. That forces the AppendVersion
// dedup hot path (INSERT OR IGNORE INTO snapshot_blobs ON HASH
// MATCH → bump refcount) to be exercised on every iteration that
// rolls one.
//
// What this catches that the unit + conformance + branching property
// tests don't:
//
//   - Cross-backend divergence on the dedup path under randomized
//     mixes of Update/Fork/Merge/Revert/duplicate-update sequences.
//     If the in-memory backend's snapshot map and the SQLite
//     backend's snapshot_blobs+version_snapshots join disagree on
//     ANY observable (history length, version ID, parent slice,
//     snapshot bytes), this test fails on the iteration that
//     produced it.
//
//   - Refcount semantics under random branching topology.
//     Implicit: GetSnapshot for a duplicate-data version returns
//     bytes identical to the original — already implied by cross-
//     backend equivalence (both backends must return the same
//     bytes for the same version ID), but the duplicate-update op
//     ensures the equivalence is checked on hashes that have
//     refcount >= 2 in the SQLite backend.
//
// Per the task brief and the branching property test's lead: a
// divergence here is a real finding. STOP and report rather than
// reroll the seed to silence it.

// dedupPropertySeed is the fixed RNG seed driving
// TestVersionedDedup_Property. Bumping it is allowed when adding a
// new op kind; do NOT bump to make a flake go away.
const dedupPropertySeed int64 = 0xDED0_2026_05_07

// dedupPropertyIterations is the number of randomized sequences the
// property test runs. Matches the branching property test's count
// (spec §7 calls for "at least 1000").
const dedupPropertyIterations = 1000

// dedupPropertyMinOps / dedupPropertyMaxOps bound the per-iteration
// op count. Inclusive on both ends.
const (
	dedupPropertyMinOps = 5
	dedupPropertyMaxOps = 30
)

// dedupOp encodes one randomized operation. Adds a "dup-update"
// kind on top of branchingOp — same shape otherwise so the runner
// can fall back to the branching-op semantics for non-dup kinds.
//
// data is the exact bytes the op will write. For "dup-update", the
// generator copies an earlier op's data field verbatim so the
// resulting AppendVersion call hits the dedup path with a hash that
// already exists in snapshot_blobs.
type dedupOp struct {
	kind string // "update" | "fork" | "merge" | "revert" | "dup-update"
	seq1 int    // primary seq target (fork / merge source / revert)
	seq2 int    // secondary seq target (merge target only)
	data json.RawMessage
}

// dedupOpsStats captures per-suite generator outcomes — surfaced via
// t.Logf so the test log records the empirical mix actually rolled.
// Lets us confirm the dup-update op is exercised at the expected
// rate without re-running the generator outside the test.
type dedupOpsStats struct {
	update    int
	fork      int
	merge     int
	revert    int
	dupUpdate int
}

// runDedupOps applies the operation sequence to a fresh
// VersionedDocumentStore and returns (history, branches) for cross-
// backend comparison. Mirrors runBranchingOps with the addition of
// the "dup-update" kind which uses Update under the hood — the
// dedup path is fully internal to AppendVersion, so the public API
// surface is unchanged.
func runDedupOps(t *testing.T, vds *VersionedDocumentStore, ops []dedupOp, label string, iter int) (history []Version, branches []Version) {
	t.Helper()
	ctx := context.Background()
	docType := "doc"
	docID := "prop"

	// Initial Create. Inject the id so both backends produce the same
	// document id (otherwise Create generates a random one and every
	// downstream version_id diverges). withID is reused from the
	// branching property test in this same package.
	initial := withID(json.RawMessage(`{"v":0}`), docID)
	if _, err := vds.Create(ctx, docType, initial); err != nil {
		t.Fatalf("[%s] iter=%d create: %v", label, iter, err)
	}

	for i, op := range ops {
		switch op.kind {
		case "update", "dup-update":
			// dup-update goes through Update with caller-chosen bytes
			// that match an earlier op's payload. The dedup path lives
			// inside AppendVersion; the public API surface is identical.
			if _, err := vds.Update(ctx, docType, docID, op.data); err != nil {
				t.Fatalf("[%s] iter=%d op[%d] %s: %v", label, iter, i, op.kind, err)
			}
		case "fork":
			if _, err := vds.Fork(ctx, docType, docID, op.seq1); err != nil {
				t.Fatalf("[%s] iter=%d op[%d] fork(seq=%d): %v", label, iter, i, op.seq1, err)
			}
		case "merge":
			if _, err := vds.Merge(ctx, docType, docID, op.seq1, op.seq2, op.data); err != nil {
				t.Fatalf("[%s] iter=%d op[%d] merge(%d,%d): %v", label, iter, i, op.seq1, op.seq2, err)
			}
		case "revert":
			if _, err := vds.Revert(ctx, docType, docID, op.seq1); err != nil {
				t.Fatalf("[%s] iter=%d op[%d] revert(seq=%d): %v", label, iter, i, op.seq1, err)
			}
		default:
			t.Fatalf("[%s] iter=%d op[%d]: unknown kind %q", label, iter, i, op.kind)
		}
	}

	hist, err := vds.History(ctx, docType, docID)
	require.NoErrorf(t, err, "[%s] iter=%d: History after ops", label, iter)
	br, err := vds.Branches(ctx, docType, docID)
	require.NoErrorf(t, err, "[%s] iter=%d: Branches after ops", label, iter)
	return hist, br
}

// generateDedupOps produces a random op sequence valid against the
// version graph as it grows. Mirrors generateBranchingOps but adds
// a "dup-update" kind whose data is a verbatim copy of an earlier
// op's data — exactly the pattern the dedup hot path expects.
//
// Op weights (after Create at seq 1):
//
//	update     ≈ 30%
//	fork       ≈ 20%
//	merge      ≈ 15%  (requires nextSeq >= 3 so merge has two distinct seqs)
//	revert     ≈ 15%
//	dup-update ≈ 20%  (requires at least one prior data-bearing op)
//
// Every op appends exactly one version, so nextSeq increments by
// exactly one per op regardless of kind.
//
// The dup-update fallback (when no prior data-bearing op exists) is
// a plain update — the generator never produces a structurally
// invalid sequence the runner would have to skip.
func generateDedupOps(rng *rand.Rand, n int) ([]dedupOp, dedupOpsStats) {
	ops := make([]dedupOp, 0, n)
	stats := dedupOpsStats{}
	nextSeq := 1 // after the initial Create

	// dataPool tracks every data payload the generator has produced
	// so dup-update can pick a verbatim copy uniformly at random.
	// Seeded with the initial Create's data so dup-update is legal
	// from the first op forward.
	dataPool := []json.RawMessage{withID(json.RawMessage(`{"v":0}`), "prop")}

	for i := 0; i < n; i++ {
		kindRoll := rng.Float64()
		var kind string
		switch {
		case nextSeq < 2:
			// Only seq 1 exists; bias the first hop to update for the
			// same reason the branching test does — give later ops a
			// substrate to branch from / dup against.
			kind = "update"
		case kindRoll < 0.30:
			kind = "update"
		case kindRoll < 0.50:
			kind = "fork"
		case kindRoll < 0.65 && nextSeq >= 3:
			kind = "merge"
		case kindRoll < 0.65:
			// Couldn't merge (need two distinct seqs); fall back to fork.
			kind = "fork"
		case kindRoll < 0.80:
			kind = "revert"
		default:
			kind = "dup-update"
		}

		op := dedupOp{kind: kind}
		switch kind {
		case "update":
			op.data = json.RawMessage(fmt.Sprintf(`{"i":%d,"k":%q,"n":%d}`, i, kind, rng.Intn(1<<30)))
			dataPool = append(dataPool, op.data)
			stats.update++
		case "dup-update":
			// Pick a verbatim-copy of an earlier payload. Every op so
			// far has contributed to dataPool (including merges and
			// reverts), so the pool is non-empty.
			op.data = dataPool[rng.Intn(len(dataPool))]
			dataPool = append(dataPool, op.data)
			stats.dupUpdate++
		case "fork":
			op.seq1 = 1 + rng.Intn(nextSeq) // any existing seq
			// Fork reuses the source seq's bytes verbatim — feed the
			// fork's source data into the pool too. We can't know the
			// resulting bytes at generation time without resolving the
			// graph, so dup-update against a fork's bytes happens via
			// whichever earlier op produced those bytes (the seed Create
			// or the prior update/dup-update). Fork doesn't append to
			// dataPool — the pool tracks payloads we KNOW the value of.
			stats.fork++
		case "merge":
			// Two distinct existing seqs.
			op.seq1 = 1 + rng.Intn(nextSeq)
			op.seq2 = 1 + rng.Intn(nextSeq)
			for op.seq2 == op.seq1 {
				op.seq2 = 1 + rng.Intn(nextSeq)
			}
			op.data = json.RawMessage(fmt.Sprintf(`{"i":%d,"k":%q,"n":%d}`, i, kind, rng.Intn(1<<30)))
			dataPool = append(dataPool, op.data)
			stats.merge++
		case "revert":
			op.seq1 = 1 + rng.Intn(nextSeq)
			// Revert reuses target seq's bytes — same caveat as fork:
			// don't add to dataPool unless we resolve the graph.
			stats.revert++
		}
		ops = append(ops, op)
		nextSeq++ // every op (update/fork/merge/revert/dup-update) appends one version
	}
	return ops, stats
}

// formatDedupOps renders an op sequence as a compact string for
// failure messages — drops payload (noise for a topology bug) and
// keeps just kind+seq targets. Mirrors formatOps for the branching
// property test.
func formatDedupOps(ops []dedupOp) string {
	parts := make([]string, len(ops))
	for i, o := range ops {
		switch o.kind {
		case "update":
			parts[i] = "u"
		case "dup-update":
			parts[i] = "d"
		case "fork":
			parts[i] = fmt.Sprintf("f(%d)", o.seq1)
		case "merge":
			parts[i] = fmt.Sprintf("m(%d,%d)", o.seq1, o.seq2)
		case "revert":
			parts[i] = fmt.Sprintf("r(%d)", o.seq1)
		default:
			parts[i] = "?"
		}
	}
	return "[" + joinShort(parts) + "]"
}

// TestVersionedDedup_Property runs randomized Create + (Update /
// Fork / Merge / Revert / dup-update) sequences against both
// backends and asserts observable equivalence:
//
//  1. History length parity — same number of versions in both.
//  2. Per-seq version_id and data byte-identity — same hash inputs
//     → same util.Short output, so the two backends MUST agree on
//     every (seq, version_id, data) tuple. This implicitly covers
//     "GetSnapshot on a duplicate-data version returns byte-
//     identical results" since data is sourced from ListVersions
//     which goes through the SQLite version_snapshots+snapshot_
//     blobs join (vs. the in-memory snapshot map). Divergence here
//     means the dedup join lookup is corrupting bytes — a real bug.
//  3. Per-version parent-slice equivalence via LoadDAG — the load-
//     bearing assertion. Same as branching property test §3.
//  4. Branches set parity — same head version_ids, same count.
//
// On any divergence, the failure message includes the seed +
// iteration number + op sequence so the failure reproduces.
func TestVersionedDedup_Property(t *testing.T) {
	t.Logf("seed=0x%X iterations=%d ops=%d..%d",
		dedupPropertySeed, dedupPropertyIterations,
		dedupPropertyMinOps, dedupPropertyMaxOps)

	rng := rand.New(rand.NewSource(dedupPropertySeed))

	totals := dedupOpsStats{}

	for iter := 0; iter < dedupPropertyIterations; iter++ {
		iter := iter
		n := dedupPropertyMinOps + rng.Intn(dedupPropertyMaxOps-dedupPropertyMinOps+1)
		ops, stats := generateDedupOps(rng, n)
		totals.update += stats.update
		totals.fork += stats.fork
		totals.merge += stats.merge
		totals.revert += stats.revert
		totals.dupUpdate += stats.dupUpdate

		// Fresh stores per iteration so prior iterations don't leak
		// state. Each iteration is a self-contained property check.
		memVDS := makeInMemoryVersionedStore(t)
		sqliteVDS := makeSQLiteVersionedStore(t)

		memHist, memBranches := runDedupOps(t, memVDS, ops, "mem", iter)
		sqlHist, sqlBranches := runDedupOps(t, sqliteVDS, ops, "sqlite", iter)

		// 1. History length parity.
		require.Equalf(t, len(memHist), len(sqlHist),
			"iter=%d (n=%d): cross-backend history length divergence (mem=%d sqlite=%d) ops=%s",
			iter, n, len(memHist), len(sqlHist), formatDedupOps(ops),
		)

		// 2. Per-seq version_id + data byte-identity. The data field
		//    on memHist[k] / sqlHist[k] is hydrated from the backend's
		//    snapshot store — the SQLite path goes through
		//    version_snapshots → snapshot_blobs, which is exactly the
		//    dedup join. Divergence here means the join is returning
		//    different bytes than what was written.
		for k := range memHist {
			require.Equalf(t, memHist[k].Seq, sqlHist[k].Seq,
				"iter=%d k=%d: seq divergence (mem=%d sqlite=%d) ops=%s",
				iter, k, memHist[k].Seq, sqlHist[k].Seq, formatDedupOps(ops),
			)
			require.Equalf(t, k+1, memHist[k].Seq,
				"iter=%d k=%d: in-memory seq monotonicity broken (got %d, want %d) ops=%s",
				iter, k, memHist[k].Seq, k+1, formatDedupOps(ops),
			)
			require.Equalf(t, memHist[k].VersionID, sqlHist[k].VersionID,
				"iter=%d seq=%d: version_id divergence (mem=%s sqlite=%s) ops=%s",
				iter, memHist[k].Seq, memHist[k].VersionID, sqlHist[k].VersionID, formatDedupOps(ops),
			)
			require.Equalf(t, []byte(memHist[k].Data), []byte(sqlHist[k].Data),
				"iter=%d seq=%d: snapshot data byte divergence ops=%s",
				iter, memHist[k].Seq, formatDedupOps(ops),
			)
		}

		// 3. Parent-slice equivalence via LoadDAG. Same load-bearing
		//    assertion as the branching property test — present here so
		//    a future dedup change can't quietly regress branching
		//    semantics either.
		ctx := context.Background()
		memDAG, err := memVDS.versions.LoadDAG(ctx, "doc", "prop")
		require.NoErrorf(t, err, "iter=%d: mem LoadDAG", iter)
		sqlDAG, err := sqliteVDS.versions.LoadDAG(ctx, "doc", "prop")
		require.NoErrorf(t, err, "iter=%d: sqlite LoadDAG", iter)

		for _, v := range memHist {
			memNode, ok := memDAG.Get(v.VersionID)
			require.Truef(t, ok, "iter=%d seq=%d vid=%s: missing in mem DAG", iter, v.Seq, v.VersionID)
			sqlNode, ok := sqlDAG.Get(v.VersionID)
			require.Truef(t, ok, "iter=%d seq=%d vid=%s: missing in sqlite DAG", iter, v.Seq, v.VersionID)
			require.Equalf(t, memNode.ParentIDs, sqlNode.ParentIDs,
				"iter=%d seq=%d vid=%s: parent IDs differ between backends (mem=%v sqlite=%v) ops=%s",
				iter, v.Seq, v.VersionID, memNode.ParentIDs, sqlNode.ParentIDs, formatDedupOps(ops),
			)
		}

		// 4. Branches parity: same set of head version_ids, same count.
		require.Equalf(t, len(memBranches), len(sqlBranches),
			"iter=%d: branches count divergence (mem=%d sqlite=%d) ops=%s",
			iter, len(memBranches), len(sqlBranches), formatDedupOps(ops),
		)
		memHeads := versionIDs(memBranches)
		sqlHeads := versionIDs(sqlBranches)
		sort.Strings(memHeads)
		sort.Strings(sqlHeads)
		require.Equalf(t, memHeads, sqlHeads,
			"iter=%d: branches set divergence (mem=%v sqlite=%v) ops=%s",
			iter, memHeads, sqlHeads, formatDedupOps(ops),
		)
	}

	// Surface the empirical op mix — confirms the dup-update path
	// was actually exercised. Total ops varies per run within the
	// 5..30 inclusive range × dedupPropertyIterations iterations.
	total := totals.update + totals.fork + totals.merge + totals.revert + totals.dupUpdate
	t.Logf("op mix over %d iterations (total %d ops): update=%d fork=%d merge=%d revert=%d dup-update=%d",
		dedupPropertyIterations, total,
		totals.update, totals.fork, totals.merge, totals.revert, totals.dupUpdate,
	)
	require.Greaterf(t, totals.dupUpdate, 0,
		"dup-update op never exercised across %d iterations — generator regression",
		dedupPropertyIterations,
	)
}
