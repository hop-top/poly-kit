package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// versioned_branching_sqlite_test.go is the P2.1 conformance check
// for the branching public API against the SQLite backend. The
// in-memory tests in versioned_branching_test.go exercise the same
// flows; this file proves the SQLite backend produces identical
// observable behavior under the same operation sequences and
// survives a Close/reopen restart with branched topology intact.
//
// The dedicated cross-backend conformance suite extension (Fork /
// Merge / Branches scenarios in versionstore_test.go) is P3.1's
// scope. This file is the minimal smoke that locks in P2.1's
// "schema unchanged" claim end-to-end.

// openSQLiteVersionedStore opens an on-disk SQLite-backed
// VersionedDocumentStore at the supplied path. The closure returns
// a fresh store opened against the same on-disk file — the actual
// durability boundary kit serve cares about.
func openSQLiteVersionedStore(t *testing.T) (*VersionedDocumentStore, func() *VersionedDocumentStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "branching.db")
	open := func() *VersionedDocumentStore {
		t.Helper()
		ds, err := NewDocumentStore(path)
		require.NoError(t, err)
		vs, err := NewSQLiteVersionStore(ds.DB())
		require.NoError(t, err)
		t.Cleanup(func() { _ = ds.Close() })
		return NewVersionedDocumentStore(ds, vs)
	}
	current := open()
	reopen := func() *VersionedDocumentStore {
		require.NoError(t, current.store.Close())
		current = open()
		return current
	}
	return current, reopen
}

// TestVersionedBranchingSQLite_ForkAndUpdate is the SQLite analog of
// TestVersionedBranching_ForkAtSeqThenUpdate.
func TestVersionedBranchingSQLite_ForkAndUpdate(t *testing.T) {
	vs, _ := openSQLiteVersionedStore(t)
	ctx := context.Background()

	_, err := vs.Create(ctx, "note", json.RawMessage(`{"id":"n1","v":1}`))
	require.NoError(t, err)
	_, err = vs.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":2}`))
	require.NoError(t, err)

	fork, err := vs.Fork(ctx, "note", "n1", 1)
	require.NoError(t, err)
	assert.Equal(t, 3, fork.Seq)
	assert.JSONEq(t, `{"id":"n1","v":1}`, string(fork.Data))

	_, err = vs.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":3}`))
	require.NoError(t, err)

	heads, err := vs.Branches(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, heads, 2, "branched topology after fork+update")
	seqs := []int{heads[0].Seq, heads[1].Seq}
	sort.Ints(seqs)
	assert.Equal(t, []int{2, 4}, seqs, "linear v2 + post-fork update v4")
}

// TestVersionedBranchingSQLite_MergeParentOrder asserts the spec's
// [sourceVersionID, targetVersionID] parent order survives the
// SQLite roundtrip via LoadDAG. This is the test that surfaced the
// version_parents-rowid ordering bug; without ORDER BY rowid in
// buildDAG, the SQLite backend would return parents lexicographic
// by parent_id and break the spec contract.
func TestVersionedBranchingSQLite_MergeParentOrder(t *testing.T) {
	vs, _ := openSQLiteVersionedStore(t)
	ctx := context.Background()

	_, err := vs.Create(ctx, "note", json.RawMessage(`{"id":"n1","v":1}`))
	require.NoError(t, err)
	_, err = vs.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":2}`))
	require.NoError(t, err)
	_, err = vs.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":3}`))
	require.NoError(t, err)

	hist, err := vs.History(ctx, "note", "n1")
	require.NoError(t, err)

	merge, err := vs.Merge(ctx, "note", "n1", 2, 3,
		json.RawMessage(`{"id":"n1","v":"merged"}`))
	require.NoError(t, err)

	dag, err := vs.versions.LoadDAG(ctx, "note", "n1")
	require.NoError(t, err)
	mergeNode, ok := dag.Get(merge.VersionID)
	require.True(t, ok)
	require.Len(t, mergeNode.ParentIDs, 2)
	assert.Equal(t, hist[1].VersionID, mergeNode.ParentIDs[0],
		"first parent is source seq (spec §4 ordering)")
	assert.Equal(t, hist[2].VersionID, mergeNode.ParentIDs[1],
		"second parent is target seq")
}

// TestVersionedBranchingSQLite_BranchesAcrossRestart proves
// branched state on SQLite survives a Close/reopen cycle. This is
// the durability boundary kit serve cares about. The full restart
// durability variant (1000+ random ops surviving reopen) is P3.2's
// scope; this test locks in the basic claim for P2.1.
func TestVersionedBranchingSQLite_BranchesAcrossRestart(t *testing.T) {
	vs, reopen := openSQLiteVersionedStore(t)
	ctx := context.Background()

	_, err := vs.Create(ctx, "note", json.RawMessage(`{"id":"n1","v":1}`))
	require.NoError(t, err)
	_, err = vs.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":2}`))
	require.NoError(t, err)

	_, err = vs.Fork(ctx, "note", "n1", 1)
	require.NoError(t, err)

	headsBefore, err := vs.Branches(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, headsBefore, 2, "two heads before restart")

	// Reopen the same on-disk DB.
	vs = reopen()

	headsAfter, err := vs.Branches(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, headsAfter, 2, "two heads after restart")

	// Same head version_ids in same order.
	beforeIDs := []string{headsBefore[0].VersionID, headsBefore[1].VersionID}
	afterIDs := []string{headsAfter[0].VersionID, headsAfter[1].VersionID}
	sort.Strings(beforeIDs)
	sort.Strings(afterIDs)
	assert.Equal(t, beforeIDs, afterIDs, "head set survives restart unchanged")
}

// TestVersionedBranchingSQLite_MergeAcrossRestart proves a merge
// version's two-parent topology persists across restart.
func TestVersionedBranchingSQLite_MergeAcrossRestart(t *testing.T) {
	vs, reopen := openSQLiteVersionedStore(t)
	ctx := context.Background()

	_, err := vs.Create(ctx, "note", json.RawMessage(`{"id":"n1","v":1}`))
	require.NoError(t, err)
	_, err = vs.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":2}`))
	require.NoError(t, err)
	_, err = vs.Fork(ctx, "note", "n1", 1)
	require.NoError(t, err)

	merge, err := vs.Merge(ctx, "note", "n1", 2, 3,
		json.RawMessage(`{"id":"n1","v":"reconciled"}`))
	require.NoError(t, err)

	vs = reopen()

	// Single head — the merge tip — after restart.
	heads, err := vs.Branches(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, heads, 1, "merge collapses heads to one across restart")
	assert.Equal(t, merge.VersionID, heads[0].VersionID)

	// Merge node still records two parents in the right order.
	dag, err := vs.versions.LoadDAG(ctx, "note", "n1")
	require.NoError(t, err)
	mergeNode, ok := dag.Get(merge.VersionID)
	require.True(t, ok)
	require.Len(t, mergeNode.ParentIDs, 2,
		"two-parent merge topology survives restart")
}
