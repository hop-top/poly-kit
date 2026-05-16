package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/domain/sqlite"
	"hop.top/kit/go/storage/sqlstore"
)

// --- E2E helpers (repository) ---

const e2eItemTableSQL = `CREATE TABLE IF NOT EXISTS e2e_items (
	id   TEXT PRIMARY KEY,
	name TEXT NOT NULL
);`

type e2eItem struct {
	ID   string
	Name string
}

func (i e2eItem) GetID() string { return i.ID }

func scanE2EItem(row *sql.Row) (e2eItem, error) {
	var it e2eItem
	err := row.Scan(&it.ID, &it.Name)
	return it, err
}

func scanE2EItemRows(rows *sql.Rows) (e2eItem, error) {
	var it e2eItem
	err := rows.Scan(&it.ID, &it.Name)
	return it, err
}

func bindE2EItem(it e2eItem) ([]string, []any) {
	return []string{"id", "name"}, []any{it.ID, it.Name}
}

func openE2EStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	store, err := sqlstore.Open(
		filepath.Join(t.TempDir(), "test.db"),
		sqlstore.Options{MigrateSQL: e2eItemTableSQL},
	)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func newE2ERepo(store *sqlstore.Store) *sqlite.SQLiteRepository[e2eItem] {
	return sqlite.NewSQLiteRepository[e2eItem](
		store, "e2e_items", scanE2EItem, scanE2EItemRows, bindE2EItem,
	)
}

// --- E2E tests (US-0006) ---

func TestE2E_Repository_CreateGetRoundtrip(t *testing.T) {
	repo := newE2ERepo(openE2EStore(t))
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &e2eItem{ID: "r1", Name: "alpha"}))

	got, err := repo.Get(ctx, "r1")
	require.NoError(t, err)
	assert.Equal(t, "r1", got.ID)
	assert.Equal(t, "alpha", got.Name)
}

func TestE2E_Repository_DuplicateCreateConflict(t *testing.T) {
	repo := newE2ERepo(openE2EStore(t))
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &e2eItem{ID: "r1", Name: "a"}))
	err := repo.Create(ctx, &e2eItem{ID: "r1", Name: "b"})
	assert.ErrorIs(t, err, domain.ErrConflict)
}

func TestE2E_Repository_GetMissing(t *testing.T) {
	repo := newE2ERepo(openE2EStore(t))

	_, err := repo.Get(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestE2E_Repository_ListWithLimit(t *testing.T) {
	repo := newE2ERepo(openE2EStore(t))
	ctx := context.Background()

	for _, id := range []string{"a", "b", "c", "d"} {
		require.NoError(t, repo.Create(ctx, &e2eItem{ID: id, Name: "item-" + id}))
	}

	items, err := repo.List(ctx, domain.Query{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func TestE2E_Repository_ListWithSort(t *testing.T) {
	repo := newE2ERepo(openE2EStore(t))
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &e2eItem{ID: "2", Name: "banana"}))
	require.NoError(t, repo.Create(ctx, &e2eItem{ID: "1", Name: "apple"}))

	items, err := repo.List(ctx, domain.Query{Sort: "name ASC"})
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, "apple", items[0].Name)
	assert.Equal(t, "banana", items[1].Name)
}

func TestE2E_Repository_ListWithSearch(t *testing.T) {
	repo := newE2ERepo(openE2EStore(t))
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &e2eItem{ID: "abc-1", Name: "x"}))
	require.NoError(t, repo.Create(ctx, &e2eItem{ID: "def-2", Name: "y"}))

	items, err := repo.List(ctx, domain.Query{Search: "abc"})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "abc-1", items[0].ID)
}

func TestE2E_Repository_ListWithOffset(t *testing.T) {
	repo := newE2ERepo(openE2EStore(t))
	ctx := context.Background()

	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, repo.Create(ctx, &e2eItem{ID: id, Name: id}))
	}

	items, err := repo.List(ctx, domain.Query{
		Sort: "id ASC", Limit: 10, Offset: 2,
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "c", items[0].ID)
}

func TestE2E_Repository_UpdateAndVerify(t *testing.T) {
	repo := newE2ERepo(openE2EStore(t))
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &e2eItem{ID: "r1", Name: "old"}))
	require.NoError(t, repo.Update(ctx, &e2eItem{ID: "r1", Name: "new"}))

	got, err := repo.Get(ctx, "r1")
	require.NoError(t, err)
	assert.Equal(t, "new", got.Name)
}

func TestE2E_Repository_DeleteAndVerifyGone(t *testing.T) {
	repo := newE2ERepo(openE2EStore(t))
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &e2eItem{ID: "r1", Name: "a"}))
	require.NoError(t, repo.Delete(ctx, "r1"))

	_, err := repo.Get(ctx, "r1")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestE2E_Repository_UpdateMissing(t *testing.T) {
	repo := newE2ERepo(openE2EStore(t))

	err := repo.Update(context.Background(), &e2eItem{ID: "ghost", Name: "x"})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestE2E_Repository_DeleteMissing(t *testing.T) {
	repo := newE2ERepo(openE2EStore(t))

	err := repo.Delete(context.Background(), "ghost")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}
