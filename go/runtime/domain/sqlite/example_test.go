package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"hop.top/kit/go/runtime/domain/sqlite"
	"hop.top/kit/go/storage/sqlstore"
)

type widget struct {
	ID   string
	Name string
}

func (w widget) GetID() string { return w.ID }

func Example() {
	dir, _ := os.MkdirTemp("", "sqlite")
	defer os.RemoveAll(dir)
	store, err := sqlstore.Open(filepath.Join(dir, "app.db"), sqlstore.Options{
		MigrateSQL: `CREATE TABLE IF NOT EXISTS widgets (id TEXT PRIMARY KEY, name TEXT NOT NULL);`,
	})
	if err != nil {
		panic(err)
	}
	defer store.Close()

	repo := sqlite.NewSQLiteRepository[widget](
		store, "widgets",
		func(r *sql.Row) (w widget, err error) { return w, r.Scan(&w.ID, &w.Name) },
		func(r *sql.Rows) (w widget, err error) { return w, r.Scan(&w.ID, &w.Name) },
		func(w widget) ([]string, []any) { return []string{"id", "name"}, []any{w.ID, w.Name} },
	)
	ctx := context.Background()
	if err := repo.Create(ctx, &widget{ID: "w1", Name: "gear"}); err != nil {
		panic(err)
	}
	got, _ := repo.Get(ctx, "w1")
	fmt.Println(got.Name)

	// Output:
	// gear
}
