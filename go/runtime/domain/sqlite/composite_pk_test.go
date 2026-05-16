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

const taskTableSQL = `CREATE TABLE IF NOT EXISTS tasks (
	project_id TEXT NOT NULL,
	id         TEXT NOT NULL,
	title      TEXT NOT NULL,
	PRIMARY KEY (project_id, id)
);`

type task struct {
	ProjectID string
	ID        string
	Title     string
}

func (t task) GetID() string { return t.ID }

func scanTask(row *sql.Row) (task, error) {
	var t task
	err := row.Scan(&t.ProjectID, &t.ID, &t.Title)
	return t, err
}

func scanTaskRows(rows *sql.Rows) (task, error) {
	var t task
	err := rows.Scan(&t.ProjectID, &t.ID, &t.Title)
	return t, err
}

func bindTask(t task) ([]string, []any) {
	return []string{"project_id", "id", "title"},
		[]any{t.ProjectID, t.ID, t.Title}
}

func taskPK(t task) ([]string, []any) {
	return []string{"project_id", "id"}, []any{t.ProjectID, t.ID}
}

func openTaskStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := sqlstore.Open(filepath.Join(dir, "test.db"), sqlstore.Options{
		MigrateSQL: taskTableSQL,
	})
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func newTaskRepo(store *sqlstore.Store) *sqlite.SQLiteRepository[task] {
	return sqlite.NewSQLiteRepository[task](
		store, "tasks", scanTask, scanTaskRows, bindTask,
		sqlite.WithPK[task](taskPK),
	)
}

func TestCompositePK_CreateAndGetByPK(t *testing.T) {
	store := openTaskStore(t)
	repo := newTaskRepo(store)
	ctx := context.Background()

	err := repo.Create(ctx, &task{ProjectID: "p1", ID: "t1", Title: "alpha"})
	require.NoError(t, err)

	got, err := repo.GetByPK(ctx, task{ProjectID: "p1", ID: "t1"})
	require.NoError(t, err)
	assert.Equal(t, "alpha", got.Title)
}

func TestCompositePK_GetByPKNotFound(t *testing.T) {
	store := openTaskStore(t)
	repo := newTaskRepo(store)

	_, err := repo.GetByPK(context.Background(), task{ProjectID: "p1", ID: "nope"})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestCompositePK_SameIDDifferentProject(t *testing.T) {
	store := openTaskStore(t)
	repo := newTaskRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &task{ProjectID: "p1", ID: "t1", Title: "one"}))
	require.NoError(t, repo.Create(ctx, &task{ProjectID: "p2", ID: "t1", Title: "two"}))

	got1, err := repo.GetByPK(ctx, task{ProjectID: "p1", ID: "t1"})
	require.NoError(t, err)
	assert.Equal(t, "one", got1.Title)

	got2, err := repo.GetByPK(ctx, task{ProjectID: "p2", ID: "t1"})
	require.NoError(t, err)
	assert.Equal(t, "two", got2.Title)
}

func TestCompositePK_Update(t *testing.T) {
	store := openTaskStore(t)
	repo := newTaskRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &task{ProjectID: "p1", ID: "t1", Title: "old"}))
	require.NoError(t, repo.Update(ctx, &task{ProjectID: "p1", ID: "t1", Title: "new"}))

	got, err := repo.GetByPK(ctx, task{ProjectID: "p1", ID: "t1"})
	require.NoError(t, err)
	assert.Equal(t, "new", got.Title)
}

func TestCompositePK_UpdateNotFound(t *testing.T) {
	store := openTaskStore(t)
	repo := newTaskRepo(store)

	err := repo.Update(context.Background(), &task{ProjectID: "p1", ID: "nope", Title: "x"})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestCompositePK_UpdateOnlyTargetsCorrectRow(t *testing.T) {
	store := openTaskStore(t)
	repo := newTaskRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &task{ProjectID: "p1", ID: "t1", Title: "a"}))
	require.NoError(t, repo.Create(ctx, &task{ProjectID: "p2", ID: "t1", Title: "b"}))

	require.NoError(t, repo.Update(ctx, &task{ProjectID: "p1", ID: "t1", Title: "updated"}))

	got1, err := repo.GetByPK(ctx, task{ProjectID: "p1", ID: "t1"})
	require.NoError(t, err)
	assert.Equal(t, "updated", got1.Title)

	got2, err := repo.GetByPK(ctx, task{ProjectID: "p2", ID: "t1"})
	require.NoError(t, err)
	assert.Equal(t, "b", got2.Title, "other project's task must be unchanged")
}

func TestCompositePK_DeleteByPK(t *testing.T) {
	store := openTaskStore(t)
	repo := newTaskRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &task{ProjectID: "p1", ID: "t1", Title: "a"}))
	require.NoError(t, repo.DeleteByPK(ctx, task{ProjectID: "p1", ID: "t1"}))

	_, err := repo.GetByPK(ctx, task{ProjectID: "p1", ID: "t1"})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestCompositePK_DeleteByPKNotFound(t *testing.T) {
	store := openTaskStore(t)
	repo := newTaskRepo(store)

	err := repo.DeleteByPK(context.Background(), task{ProjectID: "p1", ID: "nope"})
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestCompositePK_DeleteByPKOnlyTargetsCorrectRow(t *testing.T) {
	store := openTaskStore(t)
	repo := newTaskRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &task{ProjectID: "p1", ID: "t1", Title: "a"}))
	require.NoError(t, repo.Create(ctx, &task{ProjectID: "p2", ID: "t1", Title: "b"}))

	require.NoError(t, repo.DeleteByPK(ctx, task{ProjectID: "p1", ID: "t1"}))

	_, err := repo.GetByPK(ctx, task{ProjectID: "p1", ID: "t1"})
	assert.ErrorIs(t, err, domain.ErrNotFound)

	got, err := repo.GetByPK(ctx, task{ProjectID: "p2", ID: "t1"})
	require.NoError(t, err)
	assert.Equal(t, "b", got.Title, "other project's task must survive")
}

func TestCompositePK_CreateConflict(t *testing.T) {
	store := openTaskStore(t)
	repo := newTaskRepo(store)
	ctx := context.Background()

	require.NoError(t, repo.Create(ctx, &task{ProjectID: "p1", ID: "t1", Title: "a"}))
	err := repo.Create(ctx, &task{ProjectID: "p1", ID: "t1", Title: "b"})
	assert.ErrorIs(t, err, domain.ErrConflict)
}
