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

// --- E2E tests (US-0009) ---

func TestE2E_Audit_CreateTableIdempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlstore.Open(filepath.Join(dir, "audit.db"), sqlstore.Options{})
	require.NoError(t, err)
	defer store.Close()

	ar := sqlite.NewSQLiteAuditRepository(store)
	ctx := context.Background()

	require.NoError(t, ar.CreateTable(ctx))
	require.NoError(t, ar.CreateTable(ctx)) // second call: no error
}

func TestE2E_Audit_AddAndListRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlstore.Open(filepath.Join(dir, "audit.db"), sqlstore.Options{})
	require.NoError(t, err)
	defer store.Close()

	ar := sqlite.NewSQLiteAuditRepository(store)
	ctx := context.Background()
	require.NoError(t, ar.CreateTable(ctx))

	entry := &domain.AuditEntry{
		EntityID:  "ent-1",
		Timestamp: "2025-06-01T10:00:00Z",
		By:        "tester",
		Action:    "created",
		Note:      "initial creation",
	}
	require.NoError(t, ar.AddEntry(ctx, entry))

	entries, err := ar.ListEntries(ctx, "ent-1")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "ent-1", entries[0].EntityID)
	assert.Equal(t, "tester", entries[0].By)
	assert.Equal(t, "created", entries[0].Action)
	assert.Equal(t, "initial creation", entries[0].Note)
}

func TestE2E_Audit_MultipleEntriesOrderedByTimestamp(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlstore.Open(filepath.Join(dir, "audit.db"), sqlstore.Options{})
	require.NoError(t, err)
	defer store.Close()

	ar := sqlite.NewSQLiteAuditRepository(store)
	ctx := context.Background()
	require.NoError(t, ar.CreateTable(ctx))

	// Insert out of chronological order to verify ORDER BY.
	require.NoError(t, ar.AddEntry(ctx, &domain.AuditEntry{
		EntityID: "ent-1", Timestamp: "2025-06-01T12:00:00Z",
		Action: "updated", By: "bob",
	}))
	require.NoError(t, ar.AddEntry(ctx, &domain.AuditEntry{
		EntityID: "ent-1", Timestamp: "2025-06-01T10:00:00Z",
		Action: "created", By: "alice",
	}))
	require.NoError(t, ar.AddEntry(ctx, &domain.AuditEntry{
		EntityID: "ent-1", Timestamp: "2025-06-01T14:00:00Z",
		Action: "deleted", By: "charlie",
	}))

	entries, err := ar.ListEntries(ctx, "ent-1")
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, "created", entries[0].Action)
	assert.Equal(t, "updated", entries[1].Action)
	assert.Equal(t, "deleted", entries[2].Action)
}

func TestE2E_Audit_ListEntriesFiltersByEntity(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlstore.Open(filepath.Join(dir, "audit.db"), sqlstore.Options{})
	require.NoError(t, err)
	defer store.Close()

	ar := sqlite.NewSQLiteAuditRepository(store)
	ctx := context.Background()
	require.NoError(t, ar.CreateTable(ctx))

	require.NoError(t, ar.AddEntry(ctx, &domain.AuditEntry{
		EntityID: "ent-A", Timestamp: "2025-01-01T00:00:00Z", Action: "created",
	}))
	require.NoError(t, ar.AddEntry(ctx, &domain.AuditEntry{
		EntityID: "ent-B", Timestamp: "2025-01-01T00:00:00Z", Action: "created",
	}))

	entriesA, err := ar.ListEntries(ctx, "ent-A")
	require.NoError(t, err)
	require.Len(t, entriesA, 1)
	assert.Equal(t, "ent-A", entriesA[0].EntityID)

	entriesB, err := ar.ListEntries(ctx, "ent-B")
	require.NoError(t, err)
	require.Len(t, entriesB, 1)
	assert.Equal(t, "ent-B", entriesB[0].EntityID)
}

func TestE2E_Audit_ListEntriesNonexistentReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlstore.Open(filepath.Join(dir, "audit.db"), sqlstore.Options{})
	require.NoError(t, err)
	defer store.Close()

	ar := sqlite.NewSQLiteAuditRepository(store)
	ctx := context.Background()
	require.NoError(t, ar.CreateTable(ctx))

	entries, err := ar.ListEntries(ctx, "does-not-exist")
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestE2E_Audit_PersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "audit.db")
	ctx := context.Background()

	// First session: write entries.
	store1, err := sqlstore.Open(dbPath, sqlstore.Options{})
	require.NoError(t, err)
	ar1 := sqlite.NewSQLiteAuditRepository(store1)
	require.NoError(t, ar1.CreateTable(ctx))
	require.NoError(t, ar1.AddEntry(ctx, &domain.AuditEntry{
		EntityID: "persist-1", Timestamp: "2025-01-01T00:00:00Z",
		Action: "created", By: "writer",
	}))
	require.NoError(t, store1.Close())

	// Second session: reopen and verify.
	store2, err := sqlstore.Open(dbPath, sqlstore.Options{})
	require.NoError(t, err)
	defer store2.Close()

	ar2 := sqlite.NewSQLiteAuditRepository(store2)
	entries, err := ar2.ListEntries(ctx, "persist-1")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "created", entries[0].Action)
	assert.Equal(t, "writer", entries[0].By)
}
