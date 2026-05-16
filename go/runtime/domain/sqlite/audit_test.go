package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/domain/sqlite"
	"hop.top/kit/go/storage/sqlstore"
)

func openAuditStore(t *testing.T) (*sqlstore.Store, *sqlite.SQLiteAuditRepository) {
	t.Helper()
	dir := t.TempDir()
	store, err := sqlstore.Open(filepath.Join(dir, "audit.db"), sqlstore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	ar := sqlite.NewSQLiteAuditRepository(store)
	require.NoError(t, ar.CreateTable(context.Background()))
	return store, ar
}

func TestSQLiteAuditRepository_CreateTable(t *testing.T) {
	_, ar := openAuditStore(t)
	// Idempotent.
	require.NoError(t, ar.CreateTable(context.Background()))
}

func TestSQLiteAuditRepository_AddAndList(t *testing.T) {
	_, ar := openAuditStore(t)
	ctx := context.Background()

	require.NoError(t, ar.AddEntry(ctx, &domain.AuditEntry{
		EntityID: "e1", Timestamp: "2025-01-01T00:00:00Z",
		By: "alice", Action: "created", Note: "initial",
	}))
	require.NoError(t, ar.AddEntry(ctx, &domain.AuditEntry{
		EntityID: "e1", Timestamp: "2025-01-02T00:00:00Z",
		By: "bob", Action: "updated", Note: "changed name",
	}))
	require.NoError(t, ar.AddEntry(ctx, &domain.AuditEntry{
		EntityID: "e2", Timestamp: "2025-01-01T00:00:00Z",
		By: "alice", Action: "created", Note: "",
	}))

	entries, err := ar.ListEntries(ctx, "e1")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "created", entries[0].Action)
	assert.Equal(t, "updated", entries[1].Action)
	assert.Equal(t, "alice", entries[0].By)
	assert.Equal(t, "bob", entries[1].By)
}

func TestSQLiteAuditRepository_ListEmpty(t *testing.T) {
	_, ar := openAuditStore(t)

	entries, err := ar.ListEntries(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.Empty(t, entries)
}
