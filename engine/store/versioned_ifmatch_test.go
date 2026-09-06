package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTxVersionedStore builds a versioned store over the SQLite version
// store, the transaction-capable path where the precondition and the
// write share one immediate transaction. The in-memory version store
// takes the non-transactional branch, so a test that used it would not
// exercise this code at all.
func newTxVersionedStore(t *testing.T) *VersionedDocumentStore {
	t.Helper()
	vs, ds := newSQLiteVersionStore(t)
	if _, ok := vs.(txCapable); !ok {
		t.Fatal("sqlite version store is not txCapable; this test would silently exercise the fallback path")
	}
	return NewVersionedDocumentStore(ds, vs)
}

func TestUpdateAndVersionIfMatch_TxPathRefusesAStaleVersion(t *testing.T) {
	vds := newTxVersionedStore(t)
	ctx := context.Background()

	_, first, err := vds.CreateAndVersion(ctx, "notes", json.RawMessage(`{"id":"n1","title":"first"}`))
	require.NoError(t, err)

	// A writer holding the creation version updates successfully.
	_, second, err := vds.UpdateAndVersionIfMatch(ctx, "notes", "n1", json.RawMessage(`{"title":"theirs"}`), first.VersionID)
	require.NoError(t, err)
	require.NotEqual(t, first.VersionID, second.VersionID)

	// A second writer still holding the original version is refused.
	_, _, err = vds.UpdateAndVersionIfMatch(ctx, "notes", "n1", json.RawMessage(`{"title":"mine"}`), first.VersionID)
	require.ErrorIs(t, err, ErrPreconditionFailed)

	// The refused write left no trace: the head is still the second
	// version, and no third version was appended.
	head, err := vds.CurrentVersionID(ctx, "notes", "n1")
	require.NoError(t, err)
	require.Equal(t, second.VersionID, head)

	history, err := vds.History(ctx, "notes", "n1")
	require.NoError(t, err)
	require.Len(t, history, 2, "a refused write must not append a version")
}

func TestUpdateAndVersionIfMatch_EmptyPreconditionStaysUnconditional(t *testing.T) {
	vds := newTxVersionedStore(t)
	ctx := context.Background()

	_, first, err := vds.CreateAndVersion(ctx, "notes", json.RawMessage(`{"id":"n1","title":"first"}`))
	require.NoError(t, err)

	// No precondition: the write applies even though the caller names
	// no version at all. This is the backward-compatible path.
	_, second, err := vds.UpdateAndVersionIfMatch(ctx, "notes", "n1", json.RawMessage(`{"title":"blind"}`), "")
	require.NoError(t, err)
	require.NotEqual(t, first.VersionID, second.VersionID)
}

func TestCurrentVersionID_IsEmptyBeforeAnyVersionExists(t *testing.T) {
	vds := newTxVersionedStore(t)
	ctx := context.Background()

	head, err := vds.CurrentVersionID(ctx, "notes", "absent")
	require.NoError(t, err)
	require.Empty(t, head, "a document with no history must not report a version to match against")
}

func TestUpdateAndVersionIfMatch_ConcurrentWritersOnlyOneWins(t *testing.T) {
	vds := newTxVersionedStore(t)
	ctx := context.Background()

	_, first, err := vds.CreateAndVersion(ctx, "notes", json.RawMessage(`{"id":"n1","title":"first"}`))
	require.NoError(t, err)

	// Both writers hold the same version and race. Exactly one must
	// win; the loser must see a precondition failure rather than
	// silently overwriting.
	const writers = 8
	results := make(chan error, writers)
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		go func() {
			<-start
			_, _, err := vds.UpdateAndVersionIfMatch(ctx, "notes", "n1",
				json.RawMessage(`{"title":"racer"}`), first.VersionID)
			results <- err
		}()
	}
	close(start)

	var won, refused int
	for i := 0; i < writers; i++ {
		switch err := <-results; {
		case err == nil:
			won++
		case errors.Is(err, ErrPreconditionFailed):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}

	require.Equal(t, 1, won, "exactly one racer may win")
	require.Equal(t, writers-1, refused, "every loser must be refused, not silently dropped")

	history, err := vds.History(ctx, "notes", "n1")
	require.NoError(t, err)
	require.Len(t, history, 2, "only the winning write may append a version")
}
