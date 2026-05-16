package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSQLiteSetLive_HappyPath: SetLive(false) on a head flips the
// `live` column; ListVersions reflects the change.
func TestSQLiteSetLive_HappyPath(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	require.True(t, v1.Live, "fresh version is born live (DEFAULT 1)")

	require.NoError(t, vs.SetLive(ctx, "note", "n1", v1.VersionID, false))

	got, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].Live, "after SetLive(false), Live=false on read-back")

	// Flip back.
	require.NoError(t, vs.SetLive(ctx, "note", "n1", v1.VersionID, true))
	got, err = vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	assert.True(t, got[0].Live)
}

// TestSQLiteSetLive_NonHead: SetLive on a version with children
// returns ErrNotAHead.
func TestSQLiteSetLive_NonHead(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	v2, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)

	// v1 has child v2 → not a head.
	err = vs.SetLive(ctx, "note", "n1", v1.VersionID, false)
	assert.True(t, errors.Is(err, ErrNotAHead), "got: %v", err)

	// v2 is a head → ok.
	require.NoError(t, vs.SetLive(ctx, "note", "n1", v2.VersionID, false))
}

// TestSQLiteSetLive_UnknownVersion: SetLive on an unknown version_id
// returns a non-nil error (does NOT silently no-op).
func TestSQLiteSetLive_UnknownVersion(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	err := vs.SetLive(ctx, "note", "n1", "v_does_not_exist", false)
	assert.Error(t, err)
}

// TestSQLiteDeleteVersions_HappyPath: removes a set of versions,
// cascades parent edges, and decrements blob refcounts.
func TestSQLiteDeleteVersions_HappyPath(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	v2, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)
	v3, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":3}`), []string{v2.VersionID})
	require.NoError(t, err)

	freed, err := vs.DeleteVersions(ctx, "note", "n1", []string{v1.VersionID, v2.VersionID})
	require.NoError(t, err)
	require.Len(t, freed, 2, "two unique-payload blobs freed")

	// v3 still exists.
	got, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, v3.VersionID, got[0].VersionID)
}

// TestSQLiteDeleteVersions_SharedBlob: when a doomed version shares
// a blob with a retained version, the blob survives at decremented
// refcount; only the unique blob is freed.
func TestSQLiteDeleteVersions_SharedBlob(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	// Two versions with the SAME payload → share a blob (refcount=2).
	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"shared":1}`), nil)
	require.NoError(t, err)
	v2, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"shared":1}`), []string{v1.VersionID})
	require.NoError(t, err)
	// Third version with unique payload.
	v3, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"unique":3}`), []string{v2.VersionID})
	require.NoError(t, err)

	// Delete v2 and v3. v2's blob is still referenced by v1; v3's
	// blob is freed.
	freed, err := vs.DeleteVersions(ctx, "note", "n1", []string{v2.VersionID, v3.VersionID})
	require.NoError(t, err)
	require.Len(t, freed, 1, "only the unique blob is freed")

	// v1 still exists and its data is unchanged.
	got, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.JSONEq(t, `{"shared":1}`, string(got[0].Data))
}

// TestSQLiteDeleteVersions_Empty: empty versionIDs is a no-op success.
func TestSQLiteDeleteVersions_Empty(t *testing.T) {
	vs, _ := newSQLiteVersionStore(t)
	ctx := context.Background()

	freed, err := vs.DeleteVersions(ctx, "note", "n1", nil)
	require.NoError(t, err)
	assert.Empty(t, freed)
}

// TestSQLitePrune_AbandonedForkTail_Fires_EndToEnd: full Prune path
// on the SQLite backend. Uses VersionedDocumentStore wired with a
// SQLite VersionStore so Abandon + Prune both run through the
// SQL-backed DeleteVersions and SetLive.
func TestSQLitePrune_AbandonedForkTail_Fires_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/prune.db"

	ds, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	defer ds.Close()
	vs, err := NewSQLiteVersionStore(ds.DB())
	require.NoError(t, err)
	vds := NewVersionedDocumentStore(ds, vs)
	ctx := context.Background()

	doc, err := vds.Create(ctx, "note", json.RawMessage(`{"v":1}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", doc.ID, json.RawMessage(`{"v":2}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", doc.ID, json.RawMessage(`{"v":3}`))
	require.NoError(t, err)
	_, err = vds.Fork(ctx, "note", doc.ID, 2)
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", doc.ID, json.RawMessage(`{"v":5,"branch":"fork"}`))
	require.NoError(t, err)

	// Abandon seq 5.
	require.NoError(t, vds.Abandon(ctx, "note", doc.ID, 5))

	// Prune.
	res, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: 1})
	require.NoError(t, err)
	assert.Len(t, res.VersionsRemoved, 2,
		"sqlite end-to-end: fork subtree (seqs 4, 5) prunes")

	hist, err := vds.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	assert.Len(t, hist, 3, "main line retained")
}

// TestSQLiteSetLive_PersistsAcrossReopen: SetLive's UPDATE persists
// to disk; closing + reopening the DB sees the same live bit.
func TestSQLiteSetLive_PersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/persist.db"

	// First open: append + flip live.
	ds, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	vs, err := NewSQLiteVersionStore(ds.DB())
	require.NoError(t, err)
	v, err := vs.AppendVersion(context.Background(), "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	require.NoError(t, vs.SetLive(context.Background(), "note", "n1", v.VersionID, false))
	require.NoError(t, ds.Close())

	// Second open: read back. Live must still be false.
	ds2, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	defer ds2.Close()
	vs2, err := NewSQLiteVersionStore(ds2.DB())
	require.NoError(t, err)
	got, err := vs2.ListVersions(context.Background(), "note", "n1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].Live, "live=false persists across reopen")
}
