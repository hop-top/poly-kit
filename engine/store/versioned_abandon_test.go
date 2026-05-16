package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAbandon_HappyPath: Abandon on a dead-eligible head flips Live
// to false. Subsequent ListVersions shows the dead state.
//
// Setup needs at least 2 live heads so the at-least-one-live-head
// invariant doesn't reject the call.
func TestAbandon_HappyPath(t *testing.T) {
	ctx := context.Background()
	vs := newVersionedStore(t)

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`)
	// Fork at seq 1 to create a sibling head (so we have 2 live heads).
	_, err := vs.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)

	// Now we have heads {seq 2, seq 3}. Abandon seq 3 (the fork tip).
	require.NoError(t, vs.Abandon(ctx, "note", doc.ID, 3))

	versions, err := vs.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	for _, v := range versions {
		if v.Seq == 3 {
			assert.False(t, v.Live, "abandoned head must be Live=false")
		} else {
			assert.True(t, v.Live, "non-abandoned versions stay Live=true")
		}
	}
}

// TestAbandon_Idempotent: a second Abandon on the same head is a
// successful no-op.
func TestAbandon_Idempotent(t *testing.T) {
	ctx := context.Background()
	vs := newVersionedStore(t)

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`)
	_, err := vs.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)

	require.NoError(t, vs.Abandon(ctx, "note", doc.ID, 3))
	// Second call is a no-op; no error.
	require.NoError(t, vs.Abandon(ctx, "note", doc.ID, 3))
}

// TestAbandon_NonHead: Abandon on a version that has children
// returns ErrNotAHead.
func TestAbandon_NonHead(t *testing.T) {
	ctx := context.Background()
	vs := newVersionedStore(t)

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":3}`)

	// seq 1 has child seq 2 → not a head.
	err := vs.Abandon(ctx, "note", doc.ID, 1)
	assert.True(t, errors.Is(err, ErrNotAHead),
		"Abandon on non-head must return ErrNotAHead, got: %v", err)
}

// TestAbandon_LastLiveHead: Abandon refuses to drop the only live
// head. Operators wanting to drop the doc entirely should call
// Delete.
func TestAbandon_LastLiveHead(t *testing.T) {
	ctx := context.Background()
	vs := newVersionedStore(t)

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":3}`)
	// Linear history: only one head (seq 3).

	err := vs.Abandon(ctx, "note", doc.ID, 3)
	assert.True(t, errors.Is(err, ErrCannotAbandonLastLiveHead),
		"Abandon on last live head must return ErrCannotAbandonLastLiveHead, got: %v", err)
}

// TestAbandon_LastLiveHead_AfterAbandoningSibling: with two heads,
// abandoning one is fine; abandoning the second one (now the last
// live head) must fail.
func TestAbandon_LastLiveHead_AfterAbandoningSibling(t *testing.T) {
	ctx := context.Background()
	vs := newVersionedStore(t)

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`)
	_, err := vs.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)
	// Heads: {seq 2, seq 3}, both live.

	// First Abandon: seq 3.
	require.NoError(t, vs.Abandon(ctx, "note", doc.ID, 3))
	// Second Abandon on seq 2: now the last live head → reject.
	err = vs.Abandon(ctx, "note", doc.ID, 2)
	assert.True(t, errors.Is(err, ErrCannotAbandonLastLiveHead),
		"second Abandon must reject, got: %v", err)
}

// TestAbandon_NoHistory: Abandon on a (type, id) with no recorded
// history returns an error (matches History's contract).
func TestAbandon_NoHistory(t *testing.T) {
	vs := newVersionedStore(t)

	err := vs.Abandon(context.Background(), "note", "ghost", 1)
	assert.Error(t, err)
}

// TestAbandon_UnknownSeq: a seq that doesn't exist returns an error.
func TestAbandon_UnknownSeq(t *testing.T) {
	ctx := context.Background()
	vs := newVersionedStore(t)

	doc := mustCreate(t, vs, "note", `{"v":1}`)

	err := vs.Abandon(ctx, "note", doc.ID, 99)
	assert.Error(t, err)
}

// TestBranches_WithLiveOnly: with one head dead, default Branches
// returns both heads; WithLiveOnly() filters to the live one.
func TestBranches_WithLiveOnly(t *testing.T) {
	ctx := context.Background()
	vs := newVersionedStore(t)

	doc := mustCreate(t, vs, "note", `{"v":1}`)
	mustUpdate(t, vs, "note", doc.ID, `{"v":2}`)
	_, err := vs.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)
	// Heads: {seq 2, seq 3}.

	// Default: both heads.
	all, err := vs.Branches(ctx, "note", doc.ID)
	require.NoError(t, err)
	assert.Len(t, all, 2, "default Branches returns all heads (live + dead)")

	// Abandon seq 3.
	require.NoError(t, vs.Abandon(ctx, "note", doc.ID, 3))

	// Default still returns both (dead head still topology-head).
	all, err = vs.Branches(ctx, "note", doc.ID)
	require.NoError(t, err)
	assert.Len(t, all, 2, "default Branches still returns dead heads")

	// WithLiveOnly: only seq 2.
	live, err := vs.Branches(ctx, "note", doc.ID, WithLiveOnly())
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.Equal(t, 2, live[0].Seq, "WithLiveOnly filters out dead heads")
	assert.True(t, live[0].Live)
}

// TestMerge_MarksParentsDead: after Merge, source and target are
// Live=false.
func TestMerge_MarksParentsDead(t *testing.T) {
	if testing.Short() {
		t.Skip("merge live-bit test relies on in-memory backend")
	}
	ctx := context.Background()
	vds := newInMemoryVDS()

	_, err := vds.Create(ctx, "note", json.RawMessage(`{"id":"n1","v":1}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":2}`))
	require.NoError(t, err)
	// Fork at seq 1 → seq 3 (sibling head).
	_, err = vds.Fork(ctx, "note", "n1", 1)
	require.NoError(t, err)

	// Pre-merge: heads {seq 2, seq 3}, both live.
	hist, err := vds.History(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, hist, 3)
	v2, v3 := hist[1], hist[2]
	require.True(t, v2.Live)
	require.True(t, v3.Live)

	// Merge seq 2 (source) and seq 3 (target) → seq 4.
	_, err = vds.Merge(ctx, "note", "n1", 2, 3, json.RawMessage(`{"id":"n1","v":"merged"}`))
	require.NoError(t, err)

	// Post-merge: seq 2 and seq 3 are dead, seq 4 (merge tip) is live.
	hist, err = vds.History(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, hist, 4)
	for _, v := range hist {
		switch v.Seq {
		case 1:
			assert.True(t, v.Live, "seq 1 (untouched ancestor) stays live")
		case 2, 3:
			assert.False(t, v.Live, "merge consumes seq %d → dead", v.Seq)
		case 4:
			assert.True(t, v.Live, "merge tip seq 4 is born live")
		}
	}
}

// TestRevert_MarksPreRevertHeadDead: after Revert, the pre-revert
// head is Live=false; the revert tip is Live=true.
func TestRevert_MarksPreRevertHeadDead(t *testing.T) {
	if testing.Short() {
		t.Skip("revert live-bit test relies on in-memory backend")
	}
	ctx := context.Background()
	vds := newInMemoryVDS()

	_, err := vds.Create(ctx, "note", json.RawMessage(`{"id":"n1","v":1}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":2}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":3}`))
	require.NoError(t, err)
	// Linear: heads = {seq 3}.

	hist, err := vds.History(ctx, "note", "n1")
	require.NoError(t, err)
	preRevertHead := hist[2] // seq 3
	require.True(t, preRevertHead.Live)

	// Revert to seq 1 → appends seq 4 with seq 1's data.
	_, err = vds.Revert(ctx, "note", "n1", 1)
	require.NoError(t, err)

	hist, err = vds.History(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, hist, 4)
	for _, v := range hist {
		switch v.Seq {
		case 1, 2:
			assert.True(t, v.Live, "ancestors stay live")
		case 3:
			assert.False(t, v.Live, "pre-revert head seq 3 is dead")
		case 4:
			assert.True(t, v.Live, "revert tip seq 4 is born live")
		}
	}
}

// newInMemoryVDS is a small helper that wires a fresh in-memory
// backed VersionedDocumentStore. Independent of newVersionedStore
// (which runs against both backends) so the live-bit tests can pin
// the in-memory backend explicitly when needed. Cross-backend Merge
// + Revert parity is exercised through the conformance suite
// (TestVersionedDocumentStorePruningConformance).
func newInMemoryVDS() *VersionedDocumentStore {
	ds, err := NewDocumentStore(":memory:")
	if err != nil {
		panic(err)
	}
	return NewInMemoryVersionedDocumentStore(ds)
}
