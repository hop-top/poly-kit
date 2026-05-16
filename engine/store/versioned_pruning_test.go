package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPrune_NoHistory: Prune on (type, id) with no recorded history
// returns an error (matches History's contract).
func TestPrune_NoHistory(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	_, err := vs.Prune(ctx, "note", "ghost", RetentionPolicy{MaxVersions: 1})
	assert.Error(t, err)
}

// TestPrune_EmptyPolicy: a zero-value policy (no bounds) is a no-op
// — every version is "under the limit" on every dimension.
func TestPrune_EmptyPolicy(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":3}`)

	res, err := vs.Prune(ctx, "note", doc.ID, RetentionPolicy{})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved)
	assert.Equal(t, 0, res.BlobsFreed)
	assert.Equal(t, int64(0), res.BytesFreed)

	// History intact.
	versions, err := vs.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 3)
}

// TestPrune_LinearHistory_NoOp confirms spec §3 #3: in a linear
// history with a single head, every ancestor of the head is a
// retained descendant of the next-older candidate, so the bottom-up
// fixed-point removes every candidate from the prune set. Linear
// histories without abandoned branches NEVER prune.
//
// Setup: 5 sequential versions, MaxVersions=2. Naive count-based
// pruning would pick {seq 1, 2, 3} as candidates. The DAG-walk rule
// retains them all because seq 3's child is seq 4 (retained), so
// seq 3 is retained, then seq 2 (child seq 3 retained), then seq 1.
func TestPrune_LinearHistory_NoOp(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	for i := 2; i <= 5; i++ {
		mustUpdate(t, vs, "note", doc.ID, fmt.Sprintf(`{"v":%d}`, i))
	}

	res, err := vs.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxVersions: 2})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved, "linear history with single head must never prune")

	versions, err := vs.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 5)
}

// TestPrune_AbandonedForkTail confirms the prime use case: a fork
// that was never extended produces a sibling head that is
// "abandoned"; Prune leaves it alone (heads are retained per §3 #2).
//
// To exercise the actual prune fire path, this test uses an
// abandoned-tail pattern: linear history, fork at an early seq,
// extend the fork branch a few steps, then leave that fork tail
// alone. The fork tail's head is retained; the fork's intermediate
// versions become prunable iff their MaxVersions bound exceeds them.
//
// Concrete topology:
//
//	seq 1 → seq 2 → seq 3 → seq 4 → seq 5 (head A: main line)
//	         ↓
//	         seq 6 (Fork at seq 2)
//	          ↓
//	          seq 7 (extend fork; head B)
//
// MaxVersions=2 → candidates are {seq 1, seq 2, seq 3, seq 4, seq 6}
// (heads seq 5, seq 7 retained). Apply bottom-up:
//   - seq 1 child = seq 2 (candidate) → still a candidate
//   - seq 2 children = seq 3 (candidate) AND seq 6 (candidate) → still
//   - seq 3 children = seq 4 (candidate) → still
//   - seq 4 children = seq 5 (RETAINED head) → seq 4 retained, drop
//   - seq 6 children = seq 7 (RETAINED head) → seq 6 retained, drop
//   - second pass: seq 3 children include seq 4 (now retained) → drop
//   - third: seq 2 children include seq 3 (retained) → drop
//   - fourth: seq 1 child seq 2 (retained) → drop
//
// Final: nothing prunable. This confirms the spec's locked-in
// behavior — even with a fork, every ancestor of EITHER head is
// retained transitively. Pruning fires only on truly orphaned
// subtrees (which the spec §3 explicitly notes are the use case;
// reachable through Revert / Merge artifacts that abandon a tail).
func TestPrune_ForkBothHeadsAlive_NoOp(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	doc := mustCreate(t, vs, "note", `{"v":1}`)  // seq 1
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`) // seq 2
	mustUpdate(t, vs, "note", doc.ID, `{"v":3}`) // seq 3
	mustUpdate(t, vs, "note", doc.ID, `{"v":4}`) // seq 4
	mustUpdate(t, vs, "note", doc.ID, `{"v":5}`) // seq 5 (head A)

	// Fork at seq 2; produces seq 6.
	_, err := vs.Fork(ctx, "note", doc.ID, 2)
	require.NoError(t, err)
	// Extend fork: seq 7 is head B.
	mustUpdate(t, vs, "note", doc.ID, `{"v":7,"branch":"fork"}`)

	res, err := vs.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxVersions: 2})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved,
		"every ancestor of either head is retained transitively per spec §3 #3/#4")
}

// TestPrune_MergedBranchTip_Prunable demonstrates the realistic case
// where Prune actually removes versions: a branch that was merged.
//
// Topology:
//
//	seq 1 → seq 2 → seq 3 → seq 4 → seq 7 (merge; head)
//	         ↓               ↑
//	         seq 5 → seq 6 ──┘
//
// After Merge, seq 4 is no longer a head (the merge inherits both
// parents). Same for seq 6. With MaxVersions=2:
//   - heads = {seq 7}
//   - candidates per policy = {seq 1, seq 2, seq 3, seq 4, seq 5, seq 6}
//   - bottom-up: seq 5 (children: seq 6 candidate) → kept; seq 6
//     (children: seq 7 head, RETAINED) → drop seq 6 from candidates;
//     seq 5 (children: seq 6 retained) → drop; seq 4 (children: seq 7
//     retained) → drop; seq 3 (children: seq 4 retained) → drop ...
//
// All candidates drain, so still no-op. This too is spec-correct
// behavior. The actually-prunable case requires a tail that has NO
// retained descendant — i.e. an abandoned head that the operator
// would manually mark prunable, OR a Revert-style topology where a
// version's only descendant is itself a candidate.
//
// Keeping this test as a scenario-documentation check: the result is
// no-op for now; P3 conformance will exercise prune-fires cases.
func TestPrune_MergedBranches_NoOp(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	doc := mustCreate(t, vs, "note", `{"v":1}`)  // seq 1
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`) // seq 2
	mustUpdate(t, vs, "note", doc.ID, `{"v":3}`) // seq 3
	mustUpdate(t, vs, "note", doc.ID, `{"v":4}`) // seq 4 (left tip)

	// Fork at seq 2 to produce seq 5 (left line: seq 1→2→3→4 stays
	// on the new fork's parent chain). seq 5's parent is seq 2.
	_, err := vs.Fork(ctx, "note", doc.ID, 2)
	require.NoError(t, err)
	// Extend fork: seq 6 (right tip).
	mustUpdate(t, vs, "note", doc.ID, `{"v":6,"branch":"fork"}`)

	// Merge seq 6 into seq 4 → seq 7 with parents [seq 6, seq 4];
	// seq 4 and seq 6 are no longer heads.
	_, err = vs.Merge(ctx, "note", doc.ID, 6, 4, json.RawMessage(`{"v":7,"merged":true}`))
	require.NoError(t, err)

	res, err := vs.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxVersions: 2})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved,
		"merge tip extends through every ancestor; no candidate is orphaned")
}

// TestPrune_HeadRetainedOnTinyDoc: a single-version doc with any
// policy is a no-op (the only version is a head; spec §3 #2).
func TestPrune_HeadRetainedOnTinyDoc(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	res, err := vs.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxVersions: 0, MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved)

	versions, err := vs.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 1)
}

// TestPrune_Idempotent: a second Prune call with the same policy on
// the same doc is a no-op (the first call removed everything that
// was prunable).
func TestPrune_Idempotent(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	for i := 2; i <= 8; i++ {
		mustUpdate(t, vs, "note", doc.ID, fmt.Sprintf(`{"v":%d}`, i))
	}

	policy := RetentionPolicy{MaxVersions: 3}
	r1, err := vs.Prune(ctx, "note", doc.ID, policy)
	require.NoError(t, err)
	r2, err := vs.Prune(ctx, "note", doc.ID, policy)
	require.NoError(t, err)
	assert.Equal(t, r1.VersionsRemoved, r2.VersionsRemoved,
		"second Prune must observe the post-first-Prune state; both are no-op on linear history")
}

// TestPrune_AgeBasedNoOpOnFreshDoc: a fresh doc with MaxAge well in
// the past has every version as a candidate; bottom-up reduces to
// no-op for linear history (heads + their transitive ancestors are
// all retained).
func TestPrune_AgeBased_LinearNoOp(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":3}`)

	// MaxAge=1ns → every version older than 1ns is a candidate.
	// On linear history, all collapse to no-op.
	res, err := vs.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved)
}

// TestPrune_AbandonedForkTail_Fires demonstrates the headline use
// case for the live/dead head model (decision #10): an explicitly
// Abandoned fork tail becomes prunable because its head is no longer
// in the retain floor.
//
// Topology:
//
//	seq 1 → seq 2 → seq 3 (head A: main, live)
//	         ↓
//	         seq 4 → seq 5 (head B: fork tail)
//
// After Abandon(seq 5):
//   - live_heads = {seq 3}
//   - retain_floor = ancestors(seq 3) ∪ {seq 3} = {seq 1, 2, 3}
//   - candidates by policy MaxVersions=2 (keep most recent 2): seqs at
//     positions 0..2 = {seq 1, 2, 4}. seq 3, 5 are most-recent-2.
//   - candidates ∩ ¬retain_floor = {seq 4}
//   - seq 5 is also outside retain_floor BUT is not a candidate by
//     policy (it's seq 5, in most-recent-2). It stays.
//
// To make seq 5 prune too, use MaxAge=1ns so every version is a
// candidate by policy; intersect with ¬retain_floor: {seq 4, seq 5}.
// Then bottom-up: seq 5 has no children → prunable; seq 4's only
// child seq 5 is also prunable → prunable. Both removed.
func TestPrune_AbandonedForkTail_Fires(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	doc := mustCreate(t, vs, "note", `{"v":1}`)  // seq 1
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`) // seq 2
	mustUpdate(t, vs, "note", doc.ID, `{"v":3}`) // seq 3 (head A: main)

	// Fork at seq 2 → seq 4 (sibling tip).
	_, err := vs.Fork(ctx, "note", doc.ID, 2)
	require.NoError(t, err)
	// Extend fork: seq 5 (head B: fork tail).
	mustUpdate(t, vs, "note", doc.ID, `{"v":5,"branch":"fork"}`)

	// Pre-Abandon: every version is in some live retain floor →
	// no-op prune even with MaxAge=1ns.
	preRes, err := vs.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Empty(t, preRes.VersionsRemoved, "pre-Abandon: every version retained")

	// Abandon seq 5 (head B). Now live_heads = {seq 3}; the fork
	// subtree (seqs 4, 5) falls out of the retain floor.
	require.NoError(t, vs.Abandon(ctx, "note", doc.ID, 5))

	// Sanity: WithLiveOnly returns just head A.
	live, err := vs.Branches(ctx, "note", doc.ID, WithLiveOnly())
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.Equal(t, 3, live[0].Seq, "only live head is seq 3")

	// Prune with MaxAge=1ns (every version is a candidate by policy).
	// Expected: seqs 4 and 5 prune; main line untouched.
	res, err := vs.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Len(t, res.VersionsRemoved, 2,
		"seqs 4 and 5 (abandoned fork subtree) prune")

	// Verify: history is now {seq 1, 2, 3}.
	hist, err := vs.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	assert.Len(t, hist, 3)
	for _, v := range hist {
		assert.Contains(t, []int{1, 2, 3}, v.Seq, "fork subtree seqs 4, 5 removed")
	}
}

// TestPrune_AbandonedForkTail_DeadHeadOnly: with Abandon but no
// fork extension (the fork tip itself is the head), Abandon makes
// just that one version prunable.
func TestPrune_AbandonedForkTail_DeadHeadOnly(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`)
	// Fork at seq 1 → seq 3 (single-version fork tail).
	_, err := vs.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)

	// heads = {seq 2 (live), seq 3 (live)}. Abandon seq 3.
	require.NoError(t, vs.Abandon(ctx, "note", doc.ID, 3))

	// Prune with MaxAge=1ns. seq 3 is dead head, no descendants →
	// vacuously prunable. seq 1, 2 are in the live retain floor.
	res, err := vs.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Len(t, res.VersionsRemoved, 1, "single dead head prunes")

	hist, err := vs.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	assert.Len(t, hist, 2, "linear history retained")
}

// TestPrune_RevertOrphan_Fires demonstrates the revert-orphan case:
// Revert marks the pre-revert head dead. If the revert tip's data
// matches an earlier seq in linear history (no branching), the
// pre-revert head is now dead but its only descendant chain leads
// nowhere outside its own ancestors — wait, in a linear history
// after Revert there's only one branch. The pre-revert head IS dead
// but its descendant is the revert tip (live). So the dead head is
// still in retain_floor (ancestor of live revert tip). No prune.
//
// To make Revert orphan a subtree, we need branching: a fork that
// extends, then Revert on the fork branch back to the fork point.
// The pre-revert head (the fork tail) is dead; its descendants on
// that branch (the revert tip) is live but the original fork tail
// versions BETWEEN the fork point and the pre-revert head are
// ancestors of the dead head, NOT ancestors of any live head (the
// revert tip's ancestors go: revert-tip → pre-revert-head → … → seq
// 1; the live-head retain floor includes them transitively).
//
// Hmm, actually the revert tip's parent is the pre-revert head per
// our Revert impl (it goes through Update, which sets parents to
// [most-recent head]). So the revert tip → pre-revert head → fork
// chain → seq 1. The live retain floor = ancestors of revert tip =
// the entire chain. Nothing falls out.
//
// To get a true revert-orphan we need an explicit Abandon of a fork
// branch combined with Revert on the main line. Practically:
// Revert-on-main creates a tip whose ancestor chain doesn't include
// the abandoned fork tail. The fork tail subtree is prunable.
// That's just the AbandonedForkTail case we already tested.
//
// What this test demonstrates: after Revert in a linear history,
// nothing is prunable because the revert tip's ancestor chain spans
// the entire pre-revert subtree.
func TestPrune_RevertLinear_NoOp(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":3}`)
	// Revert to seq 1. seq 4 is the revert tip with seq 1's data;
	// seq 3 (pre-revert head) is marked dead.
	_, err := vs.Revert(ctx, "note", doc.ID, 1)
	require.NoError(t, err)

	hist, err := vs.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	require.Len(t, hist, 4)

	// seq 3 (dead) is still in retain floor (ancestor of live seq 4).
	res, err := vs.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved,
		"linear-history Revert: pre-revert head is ancestor of revert tip → still retained")
}

// TestPrune_BlobRefcountDecrement: prune that fires also decrements
// blob refcounts. The fork tail seq 4 (Fork at seq 1) carries the
// SAME bytes as seq 1, so they share a blob (refcount=2). Pruning
// seq 4 decrements that blob's refcount to 1; the blob is NOT
// freed (seq 1 still references it). Seq 5 has unique bytes →
// freed.
//
// Verifies dedup composition (spec §3 #5): pruning calls
// unrefBlob / decrementSnapshotBlob, which delete-on-zero. Shared
// blobs survive.
func TestPrune_BlobRefcountDecrement(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":3}`)
	_, err := vs.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)
	mustUpdate(t, vs, "note", doc.ID, `{"v":99,"branch":"fork"}`)

	require.NoError(t, vs.Abandon(ctx, "note", doc.ID, 5))

	res, err := vs.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Equal(t, 2, len(res.VersionsRemoved), "2 versions pruned (seq 4, 5)")
	assert.Equal(t, 1, res.BlobsFreed,
		"only the unique seq-5 blob is freed; the shared seq-1/seq-4 blob survives at refcount=1")
	assert.Greater(t, res.BytesFreed, int64(0), "BytesFreed reflects the freed blob size")
}

// TestPrune_PruneAfterMerge_NoOp: after Merge, source/target are
// dead but they're ancestors of the live merge tip → still retained.
// Verifies the merged-tail topology behaves correctly.
func TestPrune_PruneAfterMerge_NoOp(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`)
	_, err := vs.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)
	// Merge seq 2 and seq 3 into seq 4 (the merge tip; live).
	_, err = vs.Merge(ctx, "note", doc.ID, 2, 3, json.RawMessage(`{"v":"merged"}`))
	require.NoError(t, err)

	// Even with MaxAge=1ns, every version is in the live merge tip's
	// retain floor → no-op.
	res, err := vs.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved,
		"merge tip's retain floor covers all ancestors")
}

// mustCreate is a thin helper for prune tests.
func mustCreate(t *testing.T, vs *VersionedDocumentStore, docType, body string) Document {
	t.Helper()
	doc, err := vs.Create(context.Background(), docType, json.RawMessage(body))
	require.NoError(t, err)
	return doc
}

// mustUpdate is a thin helper for prune tests.
func mustUpdate(t *testing.T, vs *VersionedDocumentStore, docType, id, body string) Document {
	t.Helper()
	doc, err := vs.Update(context.Background(), docType, id, json.RawMessage(body))
	require.NoError(t, err)
	return doc
}
