package store

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// versioned_branching_test.go covers the public Fork / Merge /
// Branches surface added by track engine-versioned-branching.
//
// Scope (P1.2): tests run against the in-memory backend only via
// newVersionedStore. The cross-backend conformance — proving the
// SQLite backend produces identical observable behavior under the
// same operation sequences — is P2.1's scope and lives separately
// (see versioned_branching_sqlite_test.go for the SQLite smoke and
// versionstore_test.go for the canonical conformance suite the P3
// agent extends).

// seedLinear creates a document and appends additional Updates so
// the DAG ends with payloads[0] at seq 1, payloads[1] at seq 2, and
// so on. Returns the resulting version slice.
func seedLinear(t *testing.T, vs *VersionedDocumentStore, docType, id string, payloads ...json.RawMessage) []Version {
	t.Helper()
	require.NotEmpty(t, payloads, "seedLinear needs at least one payload")
	ctx := context.Background()

	_, err := vs.Create(ctx, docType, payloads[0])
	require.NoError(t, err)
	for _, p := range payloads[1:] {
		_, err := vs.Update(ctx, docType, id, p)
		require.NoError(t, err)
	}

	versions, err := vs.History(ctx, docType, id)
	require.NoError(t, err)
	require.Len(t, versions, len(payloads))
	return versions
}

// TestVersionedBranching_ForkAtSeqThenUpdate exercises the spec's
// canonical case: fork an older seq, then Update — produces a 4+
// version DAG with two heads.
func TestVersionedBranching_ForkAtSeqThenUpdate(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()
	// Linear v1, v2.
	seedLinear(t, vs, "note", "n1",
		json.RawMessage(`{"id":"n1","v":1}`),
		json.RawMessage(`{"id":"n1","v":2}`),
	)

	// Fork at seq 1: appends a sibling of v1 carrying v1's data. The
	// fork tip becomes the latest seq, so subsequent Update parents
	// on it naturally — that's how this MVP expresses divergence
	// without an UpdateAt(branchTip) surface (deferred per spec §3
	// decision 2).
	fork, err := vs.Fork(ctx, "note", "n1", 1)
	require.NoError(t, err)
	assert.Equal(t, 3, fork.Seq, "fork tip is the latest seq after Fork appends")
	assert.JSONEq(t, `{"id":"n1","v":1}`, string(fork.Data),
		"fork tip carries fromSeq's snapshot bytes")

	// Update parents on the latest seq (now the fork tip), extending
	// the forked branch. The original linear head (seq 2) remains a
	// head of the DAG.
	_, err = vs.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":3}`))
	require.NoError(t, err)

	hist, err := vs.History(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, hist, 4, "v1, v2, fork-tip(=v3), update(=v4)")

	dag, err := vs.versions.LoadDAG(ctx, "note", "n1")
	require.NoError(t, err)
	heads := dag.Heads()
	assert.Len(t, heads, 2, "branched topology: linear head + post-fork update")
}

// TestVersionedBranching_MergeAppendsTwoParentVersion verifies that
// Merge records both parent edges in spec-mandated order.
func TestVersionedBranching_MergeAppendsTwoParentVersion(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()
	hist := seedLinear(t, vs, "note", "n1",
		json.RawMessage(`{"id":"n1","v":1}`),
		json.RawMessage(`{"id":"n1","v":2}`),
		json.RawMessage(`{"id":"n1","v":3}`),
	)

	// Source seq 2, target seq 3, merged data is caller-supplied.
	merge, err := vs.Merge(ctx, "note", "n1", 2, 3,
		json.RawMessage(`{"id":"n1","v":"merged"}`))
	require.NoError(t, err)
	assert.Equal(t, 4, merge.Seq, "merge appends a new version")
	assert.JSONEq(t, `{"id":"n1","v":"merged"}`, string(merge.Data),
		"merge tip carries caller-supplied payload")

	dag, err := vs.versions.LoadDAG(ctx, "note", "n1")
	require.NoError(t, err)
	mergeNode, ok := dag.Get(merge.VersionID)
	require.True(t, ok, "merge version reachable via DAG")
	require.Len(t, mergeNode.ParentIDs, 2, "merge records two parent edges")

	// Order matters per spec §4: [sourceVersionID, targetVersionID].
	assert.Equal(t, hist[1].VersionID, mergeNode.ParentIDs[0], "first parent is source seq")
	assert.Equal(t, hist[2].VersionID, mergeNode.ParentIDs[1], "second parent is target seq")
}

// TestVersionedBranching_BranchesOnLinearHistory: a linear history
// always has exactly one head.
func TestVersionedBranching_BranchesOnLinearHistory(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()
	seedLinear(t, vs, "note", "n1",
		json.RawMessage(`{"id":"n1","v":1}`),
		json.RawMessage(`{"id":"n1","v":2}`),
		json.RawMessage(`{"id":"n1","v":3}`),
	)

	heads, err := vs.Branches(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, heads, 1)
	assert.Equal(t, 3, heads[0].Seq, "linear head is the last appended seq")
}

// TestVersionedBranching_BranchesOnForkedHistory: post-Fork the DAG
// surfaces two heads.
func TestVersionedBranching_BranchesOnForkedHistory(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()
	seedLinear(t, vs, "note", "n1",
		json.RawMessage(`{"id":"n1","v":1}`),
		json.RawMessage(`{"id":"n1","v":2}`),
	)

	_, err := vs.Fork(ctx, "note", "n1", 1)
	require.NoError(t, err)

	heads, err := vs.Branches(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, heads, 2, "forked history surfaces two heads")

	// Heads are returned ordered ascending by seq.
	seqs := make([]int, len(heads))
	for i, h := range heads {
		seqs[i] = h.Seq
	}
	assert.True(t, sort.IntsAreSorted(seqs), "heads ordered by ascending seq: %v", seqs)
	assert.Equal(t, []int{2, 3}, seqs, "linear v2 + fork-tip v3 are the two heads")
}

// TestVersionedBranching_BranchesOnMergedHistory: a Merge collapses
// the previous heads back to one (the merge tip).
func TestVersionedBranching_BranchesOnMergedHistory(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()
	seedLinear(t, vs, "note", "n1",
		json.RawMessage(`{"id":"n1","v":1}`),
		json.RawMessage(`{"id":"n1","v":2}`),
	)

	// Fork at seq 1 → seq 3 carrying v1's data; heads = {v2, v3}.
	_, err := vs.Fork(ctx, "note", "n1", 1)
	require.NoError(t, err)

	merge, err := vs.Merge(ctx, "note", "n1", 2, 3,
		json.RawMessage(`{"id":"n1","v":"reconciled"}`))
	require.NoError(t, err)

	heads, err := vs.Branches(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, heads, 1, "merge collapses heads to the merge tip")
	assert.Equal(t, merge.VersionID, heads[0].VersionID,
		"sole remaining head is the merge tip")
	assert.Equal(t, 4, heads[0].Seq, "merge tip is the latest seq")
}

// TestVersionedBranching_ForkOutOfRangeRejected: Fork at a seq that
// doesn't exist returns an error mirroring Revert's shape.
func TestVersionedBranching_ForkOutOfRangeRejected(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()
	seedLinear(t, vs, "note", "n1", json.RawMessage(`{"id":"n1","v":1}`))

	_, err := vs.Fork(ctx, "note", "n1", 99)
	require.Error(t, err, "out-of-range fromSeq is rejected")
}

// TestVersionedBranching_ForkUnknownDocRejected: Fork on a document
// with no history returns an error.
func TestVersionedBranching_ForkUnknownDocRejected(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()
	_, err := vs.Fork(ctx, "note", "missing", 1)
	require.Error(t, err, "fork on unknown doc is rejected")
}

// TestVersionedBranching_MergeOutOfRangeRejected: either seq out of
// range returns an error.
func TestVersionedBranching_MergeOutOfRangeRejected(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()
	seedLinear(t, vs, "note", "n1",
		json.RawMessage(`{"id":"n1","v":1}`),
		json.RawMessage(`{"id":"n1","v":2}`),
	)

	_, err := vs.Merge(ctx, "note", "n1", 99, 2, json.RawMessage(`{}`))
	require.Error(t, err, "out-of-range source seq rejected")

	_, err = vs.Merge(ctx, "note", "n1", 1, 99, json.RawMessage(`{}`))
	require.Error(t, err, "out-of-range target seq rejected")
}

// TestVersionedBranching_BranchesUnknownDocRejected: Branches on a
// document with no history returns an error (mirrors History).
func TestVersionedBranching_BranchesUnknownDocRejected(t *testing.T) {
	vs := newVersionedStore(t)
	ctx := context.Background()
	_, err := vs.Branches(ctx, "note", "missing")
	require.Error(t, err, "branches on unknown doc is rejected")
}
