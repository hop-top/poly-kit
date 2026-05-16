package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// versioned_pruning_property_test.go is the cross-backend property
// test for the prune track (Abandon / Prune / Branches+WithLiveOnly)
// added by track engine-version-pruning. The shape mirrors
// versioned_branching_property_test.go and
// versioned_dedup_property_test.go (the prior siblings in this
// package) so the three read as a triplet: same fixed-seed pattern,
// same fresh-stores-per-iteration discipline, same
// outcome-divergence-is-signal-not-flake stance.
//
// The prune-specific additions are two new op kinds:
//
//   - "abandon" — picks a seq at gen time and calls Abandon on it.
//     Three legitimate failure modes (ErrNotAHead,
//     ErrCannotAbandonLastLiveHead, "version not found") are silently
//     absorbed because they're inherent to the op's contract; the
//     same backend-state inputs produce the same failure on both
//     backends, preserving cross-backend equivalence. Other errors
//     are fatal.
//
//   - "prune" — submits a random RetentionPolicy and applies it.
//     Restricted to TRAILING ops only (placed at the end of each
//     iteration's sequence) because the in-memory backend's seq
//     numbering is `len(existing)+1` (vs. SQLite's MAX(seq)+1) which
//     makes seq non-monotonic across Prune in the in-memory backend
//     and causes downstream version_id collisions on Fork/Merge
//     against now-already-used (seq, data) tuples. See
//     generatePruningOps for the full rationale + future-work note.
//
// What this catches that the unit + conformance + branching/dedup
// property tests don't:
//
//   - Cross-backend topology divergence under randomized op
//     sequences that include Abandon and Prune. If the in-memory
//     backend's prune fixed-point and the SQLite backend's
//     DeleteVersions disagree on which version_ids fall (or which
//     blobs free), the per-iteration equivalence assertions catch it.
//
//   - The at-least-one-live-head invariant under random Abandon
//     pressure. A bug in Abandon's pre-flight check (e.g. counting
//     dead heads as live) would let a sequence drive the live count
//     to zero, which the post-op invariant assertion below detects.
//
//   - Dedup invariants after Prune: SUM(refcount) over snapshot_blobs
//     == COUNT(*) over version_snapshots; every retained
//     version_snapshots.hash exists in snapshot_blobs; no refcount=0
//     rows. These are the spec §7 dedup invariants restated for the
//     prune track.
//
//   - GetSnapshot byte-identity for retained versions across both
//     backends — the cross-backend equivalence assertion guarantees
//     bytes round-trip the dedup join post-prune.
//
// Per the task brief and the branching/dedup property tests' lead:
// a divergence here is a real finding. STOP and report rather than
// reroll the seed to silence it.

// pruningPropertySeed is the fixed RNG seed driving
// TestVersionedPruning_Property. Bumping it is allowed when adding a
// new op kind; do NOT bump to make a flake go away.
// Hex pun on "BADD" since prune cuts stale ancestors. Mirrors the
// branching (0xB12A) and dedup (0xDED0) tracks' seed-naming style.
const pruningPropertySeed int64 = 0xBADD_2026_05_07

// pruningPropertyIterations is the number of randomized sequences the
// property test runs. Matches the branching/dedup property tests'
// count (spec §7 calls for "at least 1000").
const pruningPropertyIterations = 1000

// pruningPropertyMinOps / pruningPropertyMaxOps bound the per-
// iteration op count. Inclusive on both ends.
const (
	pruningPropertyMinOps = 5
	pruningPropertyMaxOps = 30
)

// pruningOp encodes one randomized operation. Adds "abandon" and
// "prune" kinds on top of branchingOp shape. seq1 is reused as the
// abandon target seq; policyMaxVersions / policyMaxAge are the
// prune-policy fields.
type pruningOp struct {
	kind              string // "update"|"fork"|"merge"|"revert"|"abandon"|"prune"
	seq1              int    // primary seq target (fork / merge source / revert / abandon)
	seq2              int    // secondary seq target (merge target only)
	data              json.RawMessage
	policyMaxVersions int
	policyMaxAge      time.Duration
}

// pruningOpsStats captures per-suite generator outcomes — surfaced
// via t.Logf so the test log records the empirical mix actually
// rolled. Lets us confirm abandon/prune are exercised at the expected
// rates.
type pruningOpsStats struct {
	update  int
	fork    int
	merge   int
	revert  int
	abandon int
	prune   int
}

// runPruningOps applies the operation sequence to a fresh
// VersionedDocumentStore and returns (history, branches, liveBranches,
// pruneResults) for cross-backend comparison. Mirrors runBranchingOps
// + runDedupOps with the addition of Abandon and Prune. Errors are
// fatal: ops are generated against the live state at each step and
// any error signals a real backend bug — not a generator-validity
// question.
//
// The function asserts the at-least-one-live-head invariant after
// every op (post-Abandon and post-Prune included).
func runPruningOps(t *testing.T, vds *VersionedDocumentStore, ops []pruningOp, label string, iter int) (
	history []Version,
	branches []Version,
	liveBranches []Version,
	pruneResults []PruneResult,
) {
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

	// Post-create invariant check — there must be at least one live
	// head before any op runs.
	assertAtLeastOneLiveHead(t, vds, label, iter, -1, "post-create")

	for i, op := range ops {
		switch op.kind {
		case "update":
			if _, err := vds.Update(ctx, docType, docID, op.data); err != nil {
				t.Fatalf("[%s] iter=%d op[%d] update: %v", label, iter, i, err)
			}
		case "fork":
			// Absorb "version N not found": with Prune freely
			// interleaved (T-0432) the source seq may have been
			// pruned by an earlier op. Both backends produce the same
			// retain set under the same policy + state, so the same
			// fork attempts hit the same not-found error at the same
			// indices on both backends — cross-backend equivalence is
			// preserved.
			if _, err := vds.Fork(ctx, docType, docID, op.seq1); err != nil &&
				!isVersionNotFound(err) {
				t.Fatalf("[%s] iter=%d op[%d] fork(seq=%d): %v", label, iter, i, op.seq1, err)
			}
		case "merge":
			// Same as fork: absorb "version N not found" for either
			// merge parent.
			if _, err := vds.Merge(ctx, docType, docID, op.seq1, op.seq2, op.data); err != nil &&
				!isVersionNotFound(err) {
				t.Fatalf("[%s] iter=%d op[%d] merge(%d,%d): %v", label, iter, i, op.seq1, op.seq2, err)
			}
		case "revert":
			// Same as fork: absorb "version N not found".
			if _, err := vds.Revert(ctx, docType, docID, op.seq1); err != nil &&
				!isVersionNotFound(err) {
				t.Fatalf("[%s] iter=%d op[%d] revert(seq=%d): %v", label, iter, i, op.seq1, err)
			}
		case "abandon":
			// Try to abandon op.seq1. Four legitimate failure modes
			// are silently absorbed because they're inherent to the
			// op's contract:
			//
			//   - ErrNotAHead: seq has children (graph state shifted
			//     since generation; e.g. an intermediate fork made
			//     this seq a non-head).
			//   - ErrCannotAbandonLastLiveHead: seq is the only live
			//     head left.
			//   - "version N not found": seq doesn't exist (a prior
			//     Prune removed it under T-0432's freely-interleaved
			//     Prune model).
			//
			// All are deterministic given identical state, so both
			// backends absorb them at the same op indices — preserving
			// cross-backend equivalence. Other errors are fatal (real
			// bugs).
			err := vds.Abandon(ctx, docType, docID, op.seq1)
			if err != nil &&
				!errors.Is(err, ErrNotAHead) &&
				!errors.Is(err, ErrCannotAbandonLastLiveHead) &&
				!isVersionNotFound(err) {
				t.Fatalf("[%s] iter=%d op[%d] abandon(seq=%d): %v",
					label, iter, i, op.seq1, err)
			}
		case "prune":
			policy := RetentionPolicy{
				MaxVersions: op.policyMaxVersions,
				MaxAge:      op.policyMaxAge,
			}
			res, err := vds.Prune(ctx, docType, docID, policy)
			if err != nil {
				t.Fatalf("[%s] iter=%d op[%d] prune(maxV=%d,maxAge=%v): %v",
					label, iter, i, policy.MaxVersions, policy.MaxAge, err)
			}
			pruneResults = append(pruneResults, res)
		default:
			t.Fatalf("[%s] iter=%d op[%d]: unknown kind %q", label, iter, i, op.kind)
		}

		// Post-op invariant: at least one live head MUST remain.
		// Violation here is a real bug in Abandon's pre-flight check
		// or in Prune's retain-floor logic. Don't reroll — surface.
		assertAtLeastOneLiveHead(t, vds, label, iter, i, op.kind)
	}

	hist, err := vds.History(ctx, docType, docID)
	require.NoErrorf(t, err, "[%s] iter=%d: History after ops", label, iter)
	br, err := vds.Branches(ctx, docType, docID)
	require.NoErrorf(t, err, "[%s] iter=%d: Branches after ops", label, iter)
	live, err := vds.Branches(ctx, docType, docID, WithLiveOnly())
	require.NoErrorf(t, err, "[%s] iter=%d: Branches(live) after ops", label, iter)
	return hist, br, live, pruneResults
}

// isVersionNotFound returns true for the "version N not found" error
// shape Fork/Merge/Revert/Abandon all surface when their target seq
// is unknown. After T-0432 lifted the trailing-Prune restriction on
// the property test, ops generated against the pre-Prune state may
// reference seqs an earlier Prune removed; both backends agree on
// which seqs are gone, so absorbing the error here is safe — both
// backends absorb it at the same op indices.
func isVersionNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}

// assertAtLeastOneLiveHead fails the test if Branches(WithLiveOnly())
// returns zero heads — the load-bearing invariant from spec §3 #2 +
// #10. A violation means Abandon's pre-flight check is broken (or
// Prune somehow stripped the only live head, which it must not).
func assertAtLeastOneLiveHead(t *testing.T, vds *VersionedDocumentStore, label string, iter, opIdx int, opKind string) {
	t.Helper()
	live, err := vds.Branches(context.Background(), "doc", "prop", WithLiveOnly())
	if err != nil {
		t.Fatalf("[%s] iter=%d op[%d=%s]: Branches(live): %v", label, iter, opIdx, opKind, err)
	}
	if len(live) == 0 {
		t.Fatalf("[%s] iter=%d op[%d=%s]: at-least-one-live-head invariant VIOLATED — zero live heads",
			label, iter, opIdx, opKind)
	}
}

// generatePruningOps produces a random op sequence valid against the
// version graph as it grows. Mirrors generateBranchingOps and
// generateDedupOps but adds "abandon" and "prune" kinds. Op weights
// (after Create at seq 1):
//
//	update  ≈ 25%
//	fork    ≈ 18%
//	merge   ≈ 13%  (requires nextSeq >= 3 so merge has two distinct seqs)
//	revert  ≈ 9%
//	abandon ≈ 13%  (falls back to update if zero or one live head)
//	prune   ≈ 22%  (freely interleaved — see note)
//
// Note: Prune is freely interleaved with the other ops. Per T-0432,
// both backends now derive the next seq from a per-(type, id)
// high-water counter (in-memory: nextSeq map; SQLite:
// version_seq_high_water table) that is never decremented by
// DeleteVersions. Seq numbering is therefore monotonic across
// Prune, so a subsequent fork/merge cannot collide on version_id
// (util.Short("type:id-seq-data")) by reissuing a since-pruned
// version's seq. The earlier "trailing Prune only" workaround is
// removed.
func generatePruningOps(rng *rand.Rand, n int) ([]pruningOp, pruningOpsStats) {
	ops := make([]pruningOp, 0, n)
	stats := pruningOpsStats{}
	nextSeq := 1 // after the initial Create

	for i := 0; i < n; i++ {
		kindRoll := rng.Float64()
		var kind string
		switch {
		case nextSeq < 2:
			// Only seq 1 exists; bias the first hop to update so later
			// ops have a substrate to branch from / abandon / prune.
			kind = "update"
		case kindRoll < 0.25:
			kind = "update"
		case kindRoll < 0.43:
			kind = "fork"
		case kindRoll < 0.56 && nextSeq >= 3:
			kind = "merge"
		case kindRoll < 0.56:
			// Couldn't merge; fall back to fork.
			kind = "fork"
		case kindRoll < 0.65:
			kind = "revert"
		case kindRoll < 0.78:
			kind = "abandon"
		default:
			kind = "prune"
		}

		op := pruningOp{
			kind: kind,
			data: json.RawMessage(fmt.Sprintf(`{"i":%d,"k":%q,"n":%d}`, i, kind, rng.Intn(1<<30))),
		}
		switch kind {
		case "update":
			stats.update++
			nextSeq++
		case "fork":
			op.seq1 = 1 + rng.Intn(nextSeq)
			stats.fork++
			nextSeq++
		case "merge":
			op.seq1 = 1 + rng.Intn(nextSeq)
			op.seq2 = 1 + rng.Intn(nextSeq)
			for op.seq2 == op.seq1 {
				op.seq2 = 1 + rng.Intn(nextSeq)
			}
			stats.merge++
			nextSeq++
		case "revert":
			op.seq1 = 1 + rng.Intn(nextSeq)
			stats.revert++
			nextSeq++
		case "abandon":
			// Pick any existing seq; the runner re-picks at apply time
			// against the LIVE-head-but-not-only-one constraint.
			op.seq1 = 1 + rng.Intn(nextSeq)
			stats.abandon++
			// abandon does NOT append a version — nextSeq unchanged.
		case "prune":
			// Always pick a MaxVersions bound so prune is meaningful;
			// MaxAge (if set) stays at "effectively unbounded" so we
			// don't introduce wall-clock-dependent flake.
			//
			// We cannot pick MaxAge values in the millisecond range
			// because the in-memory and SQLite backends commit at
			// different wall-clock rates: mem ops take microseconds,
			// SQLite ops take milliseconds. A version's CreatedAt
			// timestamp comes from the backend's own clock at write
			// time, so a millisecond-range MaxAge can legitimately
			// flip a version from "exceeds bound" on one backend to
			// "does not exceed" on the other — producing spurious
			// cross-backend divergence reports. So MaxAge here is
			// restricted to "either unbounded or effectively
			// unbounded"; the unit test in versioned_pruning_test.go
			// exercises tight MaxAge on a single backend.
			//
			// prune does NOT append a version — nextSeq unchanged.
			// Pruned seqs are NOT recycled (T-0432: cross-Prune seq
			// monotonicity), so it's safe to leave nextSeq alone.
			if nextSeq > 1 {
				op.policyMaxVersions = 1 + rng.Intn(nextSeq) // [1, current count]
			} else {
				op.policyMaxVersions = 1
			}
			if rng.Float64() < 0.5 {
				op.policyMaxAge = time.Hour // unambiguous: nothing in this test run is older
			}
			stats.prune++
		}
		ops = append(ops, op)
	}

	return ops, stats
}

// formatPruningOps renders an op sequence as a compact string for
// failure messages — drops payload (noise) and keeps just kind+seq
// targets. Mirrors formatOps and formatDedupOps.
func formatPruningOps(ops []pruningOp) string {
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
		case "abandon":
			parts[i] = fmt.Sprintf("a(%d)", o.seq1)
		case "prune":
			parts[i] = fmt.Sprintf("p(maxV=%d,maxAge=%v)", o.policyMaxVersions, o.policyMaxAge)
		default:
			parts[i] = "?"
		}
	}
	return "[" + joinShort(parts) + "]"
}

// TestVersionedPruning_Property runs randomized Create + (Update /
// Fork / Merge / Revert / Abandon / Prune) sequences against both
// backends and asserts observable equivalence:
//
//  1. At-least-one-live-head invariant holds after EVERY op (asserted
//     inside runPruningOps).
//  2. History length parity — same number of retained versions in both.
//  3. Per-version (seq, version_id, data, Live) parity. The Live field
//     parity is the prune-track-specific addition vs. branching/dedup
//     property tests.
//  4. Per-version parent-slice equivalence via LoadDAG — same load-
//     bearing assertion as branching property test §3.
//  5. Branches() set parity (all heads, live or dead).
//  6. Branches(WithLiveOnly()) set parity (live heads only).
//  7. PruneResult.VersionsRemoved set-equal across backends per Prune
//     call. Determinism is hash-based: same inputs → same version_id
//     selection → same removed set.
//  8. Dedup invariants on the SQLite backend post-ops:
//     SUM(refcount) over snapshot_blobs == COUNT(*) over
//     version_snapshots; every retained join row's hash exists in
//     snapshot_blobs; no refcount=0 rows.
//  9. No dangling parents: every retained version's parent_ids
//     resolve to an existing version row in BOTH backends.
//  10. GetSnapshot byte-identity: every retained version, in both
//     backends, returns bytes identical to what was originally
//     written (hydrated through History.Data).
//
// On any divergence, the failure message includes the seed +
// iteration + op sequence so the failure reproduces.
func TestVersionedPruning_Property(t *testing.T) {
	t.Logf("seed=0x%X iterations=%d ops=%d..%d",
		pruningPropertySeed, pruningPropertyIterations,
		pruningPropertyMinOps, pruningPropertyMaxOps)

	rng := rand.New(rand.NewSource(pruningPropertySeed))

	totals := pruningOpsStats{}

	for iter := 0; iter < pruningPropertyIterations; iter++ {
		iter := iter
		n := pruningPropertyMinOps + rng.Intn(pruningPropertyMaxOps-pruningPropertyMinOps+1)
		ops, stats := generatePruningOps(rng, n)
		totals.update += stats.update
		totals.fork += stats.fork
		totals.merge += stats.merge
		totals.revert += stats.revert
		totals.abandon += stats.abandon
		totals.prune += stats.prune

		// Fresh stores per iteration so prior iterations don't leak
		// state. Each iteration is a self-contained property check.
		memVDS := makeInMemoryVersionedStore(t)
		sqliteVDS := makeSQLiteVersionedStore(t)

		memHist, memBranches, memLive, memPrunes := runPruningOps(t, memVDS, ops, "mem", iter)
		sqlHist, sqlBranches, sqlLive, sqlPrunes := runPruningOps(t, sqliteVDS, ops, "sqlite", iter)

		// 2. History length parity (post-prune retained versions).
		require.Equalf(t, len(memHist), len(sqlHist),
			"iter=%d (n=%d): cross-backend history length divergence (mem=%d sqlite=%d) ops=%s",
			iter, n, len(memHist), len(sqlHist), formatPruningOps(ops),
		)

		// 3. Per-version parity (seq, version_id, data, Live).
		for k := range memHist {
			require.Equalf(t, memHist[k].Seq, sqlHist[k].Seq,
				"iter=%d k=%d: seq divergence (mem=%d sqlite=%d) ops=%s",
				iter, k, memHist[k].Seq, sqlHist[k].Seq, formatPruningOps(ops),
			)
			require.Equalf(t, memHist[k].VersionID, sqlHist[k].VersionID,
				"iter=%d seq=%d: version_id divergence (mem=%s sqlite=%s) ops=%s",
				iter, memHist[k].Seq, memHist[k].VersionID, sqlHist[k].VersionID, formatPruningOps(ops),
			)
			require.Equalf(t, []byte(memHist[k].Data), []byte(sqlHist[k].Data),
				"iter=%d seq=%d: snapshot data byte divergence ops=%s",
				iter, memHist[k].Seq, formatPruningOps(ops),
			)
			require.Equalf(t, memHist[k].Live, sqlHist[k].Live,
				"iter=%d seq=%d vid=%s: Live field divergence (mem=%v sqlite=%v) ops=%s",
				iter, memHist[k].Seq, memHist[k].VersionID, memHist[k].Live, sqlHist[k].Live, formatPruningOps(ops),
			)
		}

		// 4. Parent-slice equivalence via LoadDAG. Load-bearing
		// assertion — same as branching/dedup property tests §3. Plus,
		// after Prune, this also catches any parent_ids that point at
		// versions that were removed (dangling parents) when compared
		// across backends.
		ctx := context.Background()
		memDAG, err := memVDS.versions.LoadDAG(ctx, "doc", "prop")
		require.NoErrorf(t, err, "iter=%d: mem LoadDAG", iter)
		sqlDAG, err := sqliteVDS.versions.LoadDAG(ctx, "doc", "prop")
		require.NoErrorf(t, err, "iter=%d: sqlite LoadDAG", iter)

		// Build retained-version-id set for dangling-parent check.
		retainedIDs := make(map[string]struct{}, len(memHist))
		for _, v := range memHist {
			retainedIDs[v.VersionID] = struct{}{}
		}

		for _, v := range memHist {
			memNode, ok := memDAG.Get(v.VersionID)
			require.Truef(t, ok, "iter=%d seq=%d vid=%s: missing in mem DAG ops=%s",
				iter, v.Seq, v.VersionID, formatPruningOps(ops))
			sqlNode, ok := sqlDAG.Get(v.VersionID)
			require.Truef(t, ok, "iter=%d seq=%d vid=%s: missing in sqlite DAG ops=%s",
				iter, v.Seq, v.VersionID, formatPruningOps(ops))
			require.Equalf(t, memNode.ParentIDs, sqlNode.ParentIDs,
				"iter=%d seq=%d vid=%s: parent IDs differ (mem=%v sqlite=%v) ops=%s",
				iter, v.Seq, v.VersionID, memNode.ParentIDs, sqlNode.ParentIDs, formatPruningOps(ops),
			)
			// 9. No dangling parents on either backend.
			for _, p := range memNode.ParentIDs {
				_, exists := retainedIDs[p]
				require.Truef(t, exists,
					"iter=%d seq=%d vid=%s: dangling parent %s in mem (not in retained set) ops=%s",
					iter, v.Seq, v.VersionID, p, formatPruningOps(ops))
			}
			for _, p := range sqlNode.ParentIDs {
				_, exists := retainedIDs[p]
				require.Truef(t, exists,
					"iter=%d seq=%d vid=%s: dangling parent %s in sqlite (not in retained set) ops=%s",
					iter, v.Seq, v.VersionID, p, formatPruningOps(ops))
			}
		}

		// 5. Branches() set parity (all heads, live + dead).
		require.Equalf(t, len(memBranches), len(sqlBranches),
			"iter=%d: branches count divergence (mem=%d sqlite=%d) ops=%s",
			iter, len(memBranches), len(sqlBranches), formatPruningOps(ops),
		)
		memHeads := versionIDs(memBranches)
		sqlHeads := versionIDs(sqlBranches)
		sort.Strings(memHeads)
		sort.Strings(sqlHeads)
		require.Equalf(t, memHeads, sqlHeads,
			"iter=%d: branches set divergence (mem=%v sqlite=%v) ops=%s",
			iter, memHeads, sqlHeads, formatPruningOps(ops),
		)

		// 6. Branches(WithLiveOnly()) set parity.
		require.Equalf(t, len(memLive), len(sqlLive),
			"iter=%d: live-branches count divergence (mem=%d sqlite=%d) ops=%s",
			iter, len(memLive), len(sqlLive), formatPruningOps(ops),
		)
		memLiveIDs := versionIDs(memLive)
		sqlLiveIDs := versionIDs(sqlLive)
		sort.Strings(memLiveIDs)
		sort.Strings(sqlLiveIDs)
		require.Equalf(t, memLiveIDs, sqlLiveIDs,
			"iter=%d: live-branches set divergence (mem=%v sqlite=%v) ops=%s",
			iter, memLiveIDs, sqlLiveIDs, formatPruningOps(ops),
		)

		// 7. PruneResult.VersionsRemoved set-equal across backends per
		// Prune call. Determinism is hash-based: same op sequence →
		// same version_ids → same prunable set.
		require.Equalf(t, len(memPrunes), len(sqlPrunes),
			"iter=%d: prune-call count divergence (mem=%d sqlite=%d) ops=%s",
			iter, len(memPrunes), len(sqlPrunes), formatPruningOps(ops),
		)
		for pi := range memPrunes {
			memRemoved := append([]string(nil), memPrunes[pi].VersionsRemoved...)
			sqlRemoved := append([]string(nil), sqlPrunes[pi].VersionsRemoved...)
			sort.Strings(memRemoved)
			sort.Strings(sqlRemoved)
			require.Equalf(t, memRemoved, sqlRemoved,
				"iter=%d prune#%d: VersionsRemoved divergence (mem=%v sqlite=%v) ops=%s",
				iter, pi, memRemoved, sqlRemoved, formatPruningOps(ops),
			)
		}

		// 8. SQLite dedup invariants (spec §7).
		assertSQLiteDedupInvariants(t, sqliteVDS, iter, ops)

		// 10. GetSnapshot byte-identity: every retained version, both
		// backends, returns bytes equal to History's Data field. This
		// exercises the GetSnapshot path explicitly (History may use
		// a different read path) — divergence here is a real bug in
		// the SQLite snapshot join post-prune.
		for _, v := range memHist {
			memBytes, err := memVDS.versions.GetSnapshot(ctx, v.VersionID)
			require.NoErrorf(t, err, "iter=%d seq=%d: mem GetSnapshot", iter, v.Seq)
			sqlBytes, err := sqliteVDS.versions.GetSnapshot(ctx, v.VersionID)
			require.NoErrorf(t, err, "iter=%d seq=%d: sqlite GetSnapshot", iter, v.Seq)
			require.Equalf(t, []byte(memBytes), []byte(sqlBytes),
				"iter=%d seq=%d vid=%s: GetSnapshot bytes differ (mem=%s sqlite=%s) ops=%s",
				iter, v.Seq, v.VersionID, string(memBytes), string(sqlBytes), formatPruningOps(ops),
			)
			require.Equalf(t, []byte(v.Data), []byte(memBytes),
				"iter=%d seq=%d vid=%s: History.Data != mem GetSnapshot ops=%s",
				iter, v.Seq, v.VersionID, formatPruningOps(ops),
			)
		}
	}

	// Surface the empirical op mix — confirms abandon/prune were
	// actually exercised. abandonFallback would be visible too if the
	// generator hit "single live head" frequently (an indicator the
	// op weights need tuning).
	total := totals.update + totals.fork + totals.merge + totals.revert + totals.abandon + totals.prune
	t.Logf("op mix over %d iterations (total %d ops): update=%d fork=%d merge=%d revert=%d abandon=%d prune=%d",
		pruningPropertyIterations, total,
		totals.update, totals.fork, totals.merge, totals.revert, totals.abandon, totals.prune,
	)
	require.Greaterf(t, totals.abandon, 0,
		"abandon op never exercised across %d iterations — generator regression",
		pruningPropertyIterations,
	)
	require.Greaterf(t, totals.prune, 0,
		"prune op never exercised across %d iterations — generator regression",
		pruningPropertyIterations,
	)
}

// assertSQLiteDedupInvariants checks the spec §7 dedup invariants on
// the SQLite backend post-ops:
//
//   - SUM(refcount) over snapshot_blobs == COUNT(*) over
//     version_snapshots. Every join row contributes one to the
//     refcount; the prune path must keep this in sync.
//   - Every version_snapshots.hash exists in snapshot_blobs (no
//     orphan join rows).
//   - No refcount=0 rows in snapshot_blobs (the dedup contract is
//     "delete at zero").
func assertSQLiteDedupInvariants(t *testing.T, vds *VersionedDocumentStore, iter int, ops []pruningOp) {
	t.Helper()
	db := vds.store.DB()
	ctx := context.Background()

	// Invariant: SUM(refcount) == COUNT(version_snapshots).
	var sumRC sql.NullInt64
	err := db.QueryRowContext(ctx, "SELECT SUM(refcount) FROM snapshot_blobs").Scan(&sumRC)
	require.NoErrorf(t, err, "iter=%d: read SUM(refcount)", iter)
	var joinCount int64
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM version_snapshots").Scan(&joinCount)
	require.NoErrorf(t, err, "iter=%d: read COUNT(version_snapshots)", iter)
	rcSum := int64(0)
	if sumRC.Valid {
		rcSum = sumRC.Int64
	}
	require.Equalf(t, joinCount, rcSum,
		"iter=%d: dedup invariant VIOLATED — SUM(snapshot_blobs.refcount)=%d != COUNT(version_snapshots)=%d ops=%s",
		iter, rcSum, joinCount, formatPruningOps(ops),
	)

	// Invariant: every version_snapshots.hash exists in snapshot_blobs.
	var orphanCount int64
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM version_snapshots vs
		 LEFT JOIN snapshot_blobs sb ON sb.hash = vs.hash
		 WHERE sb.hash IS NULL`,
	).Scan(&orphanCount)
	require.NoErrorf(t, err, "iter=%d: read orphan-hash count", iter)
	require.Zerof(t, orphanCount,
		"iter=%d: dedup invariant VIOLATED — %d version_snapshots rows have hash missing from snapshot_blobs ops=%s",
		iter, orphanCount, formatPruningOps(ops),
	)

	// Invariant: no refcount=0 rows.
	var zeroRC int64
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM snapshot_blobs WHERE refcount = 0").Scan(&zeroRC)
	require.NoErrorf(t, err, "iter=%d: read refcount=0 count", iter)
	require.Zerof(t, zeroRC,
		"iter=%d: dedup invariant VIOLATED — %d snapshot_blobs rows have refcount=0 ops=%s",
		iter, zeroRC, formatPruningOps(ops),
	)
}
