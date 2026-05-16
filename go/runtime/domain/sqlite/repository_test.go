package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/domain/sqlite"
	"hop.top/kit/go/storage/sqlstore"
)

const itemTableSQL = `CREATE TABLE IF NOT EXISTS items (
	id   TEXT PRIMARY KEY,
	name TEXT NOT NULL
);`

type item struct {
	ID   string
	Name string
}

func (i item) GetID() string { return i.ID }

func scanItem(row *sql.Row) (item, error) {
	var it item
	err := row.Scan(&it.ID, &it.Name)
	return it, err
}

func scanItemRows(rows *sql.Rows) (item, error) {
	var it item
	err := rows.Scan(&it.ID, &it.Name)
	return it, err
}

func bindItem(it item) ([]string, []any) {
	return []string{"id", "name"}, []any{it.ID, it.Name}
}

func openTestStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := sqlstore.Open(filepath.Join(dir, "test.db"), sqlstore.Options{
		MigrateSQL: itemTableSQL,
	})
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func newItemRepo(store *sqlstore.Store) *sqlite.SQLiteRepository[item] {
	return sqlite.NewSQLiteRepository[item](store, "items", scanItem, scanItemRows, bindItem)
}

func TestSQLiteRepository_CreateAndGet(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &item{ID: "1", Name: "alpha"}))

	got, err := repo.Get(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, "alpha", got.Name)
}

func TestSQLiteRepository_CreateConflict(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &item{ID: "1", Name: "a"}))
	err := repo.Create(ctx, &item{ID: "1", Name: "b"})
	assert.ErrorIs(t, err, domain.ErrConflict)
}

func TestSQLiteRepository_GetNotFound(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)

	_, err := repo.Get(context.Background(), "nope")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestSQLiteRepository_Update(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &item{ID: "1", Name: "a"}))
	require.NoError(t, repo.Update(ctx, &item{ID: "1", Name: "b"}))

	got, err := repo.Get(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, "b", got.Name)
}

func TestSQLiteRepository_UpdateNotFound(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)

	err := repo.Update(context.Background(), &item{ID: "nope", Name: "x"})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestSQLiteRepository_Delete(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &item{ID: "1", Name: "a"}))
	require.NoError(t, repo.Delete(ctx, "1"))

	_, err := repo.Get(ctx, "1")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestSQLiteRepository_DeleteNotFound(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)

	err := repo.Delete(context.Background(), "nope")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestSQLiteRepository_List(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &item{ID: "1", Name: "alpha"}))
	require.NoError(t, repo.Create(ctx, &item{ID: "2", Name: "beta"}))
	require.NoError(t, repo.Create(ctx, &item{ID: "3", Name: "gamma"}))

	items, err := repo.List(ctx, domain.Query{})
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestSQLiteRepository_ListWithLimit(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)
	ctx := context.Background()

	for _, n := range []string{"a", "b", "c"} {
		require.NoError(t, repo.Create(ctx, &item{ID: n, Name: n}))
	}

	items, err := repo.List(ctx, domain.Query{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func TestSQLiteRepository_ListWithOffset(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)
	ctx := context.Background()

	for _, n := range []string{"a", "b", "c"} {
		require.NoError(t, repo.Create(ctx, &item{ID: n, Name: n}))
	}

	items, err := repo.List(ctx, domain.Query{Limit: 10, Offset: 2})
	require.NoError(t, err)
	assert.Len(t, items, 1)
}

func TestSQLiteRepository_ListWithSort(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &item{ID: "2", Name: "beta"}))
	require.NoError(t, repo.Create(ctx, &item{ID: "1", Name: "alpha"}))

	items, err := repo.List(ctx, domain.Query{Sort: "name ASC"})
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, "alpha", items[0].Name)
}

func TestSQLiteRepository_ListWithSearch(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &item{ID: "abc", Name: "x"}))
	require.NoError(t, repo.Create(ctx, &item{ID: "def", Name: "y"}))

	items, err := repo.List(ctx, domain.Query{Search: "ab"})
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, "abc", items[0].ID)
}

func TestSQLiteRepository_ListSortInjectionBlocked(t *testing.T) {
	store := openTestStore(t)
	repo := newItemRepo(store)
	ctx := context.Background()

	_, err := repo.List(ctx, domain.Query{Sort: "name; DROP TABLE items--"})
	assert.ErrorIs(t, err, domain.ErrValidation)

	_, err = repo.List(ctx, domain.Query{Sort: "1=1"})
	assert.ErrorIs(t, err, domain.ErrValidation)
}

func TestSQLiteRepository_UpdateMissingIdColumn(t *testing.T) {
	store := openTestStore(t)
	// Use a bind function that omits the id column.
	noIDBind := func(it item) ([]string, []any) {
		return []string{"name"}, []any{it.Name}
	}
	repo := sqlite.NewSQLiteRepository[item](store, "items", scanItem, scanItemRows, noIDBind)
	ctx := context.Background()

	err := repo.Update(ctx, &item{ID: "1", Name: "x"})
	assert.ErrorIs(t, err, domain.ErrValidation)
}

// Ensure temp dir is clean (sanity).
func TestSQLiteRepository_TempDir(t *testing.T) {
	dir := t.TempDir()
	entries, _ := os.ReadDir(dir)
	assert.Empty(t, entries)
}
