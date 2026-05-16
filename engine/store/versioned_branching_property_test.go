package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// versioned_branching_property_test.go is the cross-backend property
// test for the branching public API (Fork / Merge / Branches) added
// by track engine-versioned-branching. The shape mirrors
// versioned_property_test.go (the linear-history property test) so
// the two read as siblings: same fixed-seed pattern, same
// fresh-stores-per-iteration discipline, same outcome-divergence-is-
// signal-not-flake stance.
//
// What this catches that the unit + conformance suites don't:
//
//   - Cross-backend topology divergence under randomized op
//     sequences. The unit tests assert hand-crafted DAG shapes; this
//     test rolls 1000 random Create+Update/Fork/Merge/Revert
//     sequences and asserts the in-memory and SQLite backends agree
//     on the resulting graph byte-for-byte. The pre-fe875f7 SQLite
//     parent-ordering bug — buildDAG returning version_parents rows
//     unordered, so Merge's [sourceVersionID, targetVersionID]
//     contract collapsed to lexicographic-by-parent-id — would fail
//     the parent-slice equivalence assertion below on iteration 1.
//
//   - Per-seq version_id determinism across backends. Both backends
//     derive version_id from util.Short([]byte("type:id-seq-data"),
//     16); same inputs must produce same output. If a future backend
//     deviates, this test surfaces it as a per-iteration divergence.
//
// Per the task brief: a divergence here is a real finding. STOP and
// report rather than reroll the seed to silence it.

// branchingPropertySeed is the fixed RNG seed driving
// TestVersionedBranching_Property. Bumping it is allowed when adding
// a new op kind; do NOT bump to make a flake go away.
const branchingPropertySeed int64 = 0xB12A_2026_05_07

// branchingPropertyIterations is the number of randomized sequences
// the property test runs. Spec §7 calls for "at least 1000".
const branchingPropertyIterations = 1000

// branchingPropertyMinOps / branchingPropertyMaxOps bound the per-
// iteration op count. The range is inclusive on both ends — see
// generateBranchingOps.
const (
	branchingPropertyMinOps = 5
	branchingPropertyMaxOps = 30
)

// branchingOp encodes one randomized operation in a property
// sequence. seq1 / seq2 reference seq numbers as they exist at the
// point in the sequence the op runs (the generator constrains them
// to existing seqs so neither backend errors on a generation we know
// is invalid).
type branchingOp struct {
	kind string // "update" | "fork" | "merge" | "revert"
	seq1 int    // primary seq target (fork / merge source / revert)
	seq2 int    // secondary seq target (merge target only)
	data json.RawMessage
}

// withID injects {"id": <id>, ...} into a JSON object payload so the
// underlying DocumentStore.Create takes the deterministic path
// (extractID returning a non-empty string) instead of generating a
// random id. Without this, the in-memory and SQLite backends would
// produce different doc IDs for the same Create call and every
// downstream version_id would diverge.
//
// The helper is intentionally minimal: it assumes data is a JSON
// object literal whose first byte is '{' and rewrites just the head
// to insert the id field. We control the payloads in this file so
// the assumption holds.
func withID(data json.RawMessage, id string) json.RawMessage {
	if len(data) == 0 || data[0] != '{' {
		return json.RawMessage(fmt.Sprintf(`{"id":%q}`, id))
	}
	// Empty object: replace with {"id":"..."}.
	if len(data) == 2 {
		return json.RawMessage(fmt.Sprintf(`{"id":%q}`, id))
	}
	// Non-empty object: insert id as the first field.
	return json.RawMessage(fmt.Sprintf(`{"id":%q,%s`, id, string(data[1:])))
}

// makeInMemoryVersionedStore returns a fresh in-memory
// VersionedDocumentStore. Mirrors newVersionedStore for naming
// parallelism with makeSQLiteVersionedStore.
func makeInMemoryVersionedStore(t *testing.T) *VersionedDocumentStore {
	t.Helper()
	return newVersionedStore(t)
}

// makeSQLiteVersionedStore returns a fresh on-disk-SQLite
// VersionedDocumentStore. The path lives under t.TempDir() so the
// test framework cleans it up.
func makeSQLiteVersionedStore(t *testing.T) *VersionedDocumentStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "branching-prop.db")
	ds, err := NewDocumentStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ds.Close() })
	vs, err := NewSQLiteVersionStore(ds.DB())
	require.NoError(t, err)
	return NewVersionedDocumentStore(ds, vs)
}

// runBranchingOps applies the operation sequence to a fresh
// VersionedDocumentStore and returns the resulting (history,
// branches) for cross-backend comparison. Errors are fatal: ops are
// generated against the version graph as it grows, so any error
// signals a real backend bug — not a generator-validity question.
func runBranchingOps(t *testing.T, vds *VersionedDocumentStore, ops []branchingOp, label string, iter int) (history []Version, branches []Version) {
	t.Helper()
	ctx := context.Background()
	docType := "doc"
	docID := "prop"

	// Initial Create. Inject the id so both backends produce the same
	// document id (otherwise Create generates a random one and every
	// downstream version_id diverges).
	initial := withID(json.RawMessage(`{"v":0}`), docID)
	if _, err := vds.Create(ctx, docType, initial); err != nil {
		t.Fatalf("[%s] iter=%d create: %v", label, iter, err)
	}

	for i, op := range ops {
		switch op.kind {
		case "update":
			if _, err := vds.Update(ctx, docType, docID, op.data); err != nil {
				t.Fatalf("[%s] iter=%d op[%d] update: %v", label, iter, i, err)
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

// generateBranchingOps produces a random op sequence valid against
// the version graph as it grows. seq targets are sampled from "seqs
// that exist at this point in the sequence" so the sequence never
// asks Fork/Merge/Revert about a seq the backend doesn't have.
//
// Op weights (after Create at seq 1):
//
//	update  ≈ 40%  (or 100% when only seq 1 exists — fork/merge/revert
//	                still legal at nextSeq>=2 but we bias to update for
//	                the first hop so most sequences exercise linear
//	                growth before branching)
//	fork    ≈ 25%
//	merge   ≈ 20%  (requires nextSeq >= 3 so merge has two distinct seqs)
//	revert  ≈ 15%
//
// Every op appends exactly one version, so nextSeq increments by
// exactly one per op regardless of kind.
func generateBranchingOps(rng *rand.Rand, n int) []branchingOp {
	ops := make([]branchingOp, 0, n)
	nextSeq := 1 // after the initial Create

	for i := 0; i < n; i++ {
		kindRoll := rng.Float64()
		var kind string
		switch {
		case nextSeq < 2:
			// Only seq 1 exists; fork/merge/revert can technically run
			// against seq 1, but we keep the first hop linear so
			// sequences typically have a substrate to branch from.
			kind = "update"
		case kindRoll < 0.40:
			kind = "update"
		case kindRoll < 0.65:
			kind = "fork"
		case kindRoll < 0.85 && nextSeq >= 3:
			kind = "merge"
		case kindRoll < 0.85:
			// Couldn't merge (need two distinct seqs); fall back to fork.
			kind = "fork"
		default:
			kind = "revert"
		}

		op := branchingOp{
			kind: kind,
			data: json.RawMessage(fmt.Sprintf(`{"i":%d,"k":%q,"n":%d}`, i, kind, rng.Intn(1<<30))),
		}
		switch kind {
		case "fork":
			op.seq1 = 1 + rng.Intn(nextSeq) // any existing seq (1..nextSeq)
		case "merge":
			// Two distinct existing seqs.
			op.seq1 = 1 + rng.Intn(nextSeq)
			op.seq2 = 1 + rng.Intn(nextSeq)
			for op.seq2 == op.seq1 {
				op.seq2 = 1 + rng.Intn(nextSeq)
			}
		case "revert":
			op.seq1 = 1 + rng.Intn(nextSeq)
		}
		ops = append(ops, op)
		nextSeq++ // every op (update/fork/merge/revert) appends one version
	}
	return ops
}

// TestVersionedBranching_Property runs randomized Create + (Update /
// Fork / Merge / Revert) sequences against both backends and asserts
// observable equivalence at the end:
//
//  1. History length parity — same number of versions in both.
//  2. Per-seq version_id and data byte-identity — same hash inputs →
//     same util.Short output, so the two backends MUST agree on
//     every (seq, version_id, data) tuple.
//  3. Per-version parent-slice equivalence via LoadDAG — the
//     load-bearing assertion. This is what catches the fe875f7
//     SQLite parent-ordering bug: pre-fix, buildDAG returned
//     version_parents rows unordered, so Merge's
//     [sourceVersionID, targetVersionID] contract collapsed and
//     mergeNode.ParentIDs differed between mem and SQLite. Post-fix,
//     this assertion is silent.
//  4. Branches set parity — same head version_ids, same count.
//
// On any divergence, the failure message includes the seed +
// iteration number + op sequence so the failure reproduces. We
// surface the seed at the top of the test log too.
func TestVersionedBranching_Property(t *testing.T) {
	t.Logf("seed=0x%X iterations=%d ops=%d..%d",
		branchingPropertySeed, branchingPropertyIterations,
		branchingPropertyMinOps, branchingPropertyMaxOps)

	rng := rand.New(rand.NewSource(branchingPropertySeed))

	for iter := 0; iter < branchingPropertyIterations; iter++ {
		iter := iter
		n := branchingPropertyMinOps + rng.Intn(branchingPropertyMaxOps-branchingPropertyMinOps+1)
		ops := generateBranchingOps(rng, n)

		// Fresh stores per iteration so prior iterations don't leak
		// state. Each iteration is a self-contained property check.
		memVDS := makeInMemoryVersionedStore(t)
		sqliteVDS := makeSQLiteVersionedStore(t)

		memHist, memBranches := runBranchingOps(t, memVDS, ops, "mem", iter)
		sqlHist, sqlBranches := runBranchingOps(t, sqliteVDS, ops, "sqlite", iter)

		// 1. History length parity.
		require.Equalf(t, len(memHist), len(sqlHist),
			"iter=%d (n=%d): cross-backend history length divergence (mem=%d sqlite=%d) ops=%s",
			iter, n, len(memHist), len(sqlHist), formatOps(ops),
		)

		// 2. Per-seq version_id + data byte-identity.
		for k := range memHist {
			require.Equalf(t, memHist[k].Seq, sqlHist[k].Seq,
				"iter=%d k=%d: seq divergence (mem=%d sqlite=%d) ops=%s",
				iter, k, memHist[k].Seq, sqlHist[k].Seq, formatOps(ops),
			)
			require.Equalf(t, k+1, memHist[k].Seq,
				"iter=%d k=%d: in-memory seq monotonicity broken (got %d, want %d) ops=%s",
				iter, k, memHist[k].Seq, k+1, formatOps(ops),
			)
			require.Equalf(t, memHist[k].VersionID, sqlHist[k].VersionID,
				"iter=%d seq=%d: version_id divergence (mem=%s sqlite=%s) ops=%s",
				iter, memHist[k].Seq, memHist[k].VersionID, sqlHist[k].VersionID, formatOps(ops),
			)
			require.Equalf(t, []byte(memHist[k].Data), []byte(sqlHist[k].Data),
				"iter=%d seq=%d: snapshot data byte divergence ops=%s",
				iter, memHist[k].Seq, formatOps(ops),
			)
		}

		// 3. Parent-slice equivalence via LoadDAG. THIS is the
		//    load-bearing assertion: it's what would have caught the
		//    pre-fe875f7 SQLite parent-ordering bug. For every version
		//    common to both DAGs, mem.ParentIDs MUST equal
		//    sqlite.ParentIDs slice-by-slice (order matters per spec
		//    §4 for Merge).
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
				iter, v.Seq, v.VersionID, memNode.ParentIDs, sqlNode.ParentIDs, formatOps(ops),
			)
		}

		// 4. Branches parity: same set of head version_ids, same count.
		require.Equalf(t, len(memBranches), len(sqlBranches),
			"iter=%d: branches count divergence (mem=%d sqlite=%d) ops=%s",
			iter, len(memBranches), len(sqlBranches), formatOps(ops),
		)
		memHeads := versionIDs(memBranches)
		sqlHeads := versionIDs(sqlBranches)
		sort.Strings(memHeads)
		sort.Strings(sqlHeads)
		require.Equalf(t, memHeads, sqlHeads,
			"iter=%d: branches set divergence (mem=%v sqlite=%v) ops=%s",
			iter, memHeads, sqlHeads, formatOps(ops),
		)
	}
}

// versionIDs extracts the version_id field from each Version.
func versionIDs(vs []Version) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.VersionID
	}
	return out
}

// formatOps renders an op sequence as a compact string for failure
// messages. Drops the data payload (it's noise for a topology bug)
// and keeps just kind+seq targets.
func formatOps(ops []branchingOp) string {
	parts := make([]string, len(ops))
	for i, o := range ops {
		switch o.kind {
		case "update":
			parts[i] = "u"
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

// joinShort joins parts with comma — pulled out so we don't pull in
// strings just for one Join call (already imported elsewhere in the
// package, but keeping this file's import surface minimal).
func joinShort(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "," + p
	}
	return out
}
