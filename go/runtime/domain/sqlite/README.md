# sqlite

## What it answers

`domain.Repository[T]` and `domain.AuditRepository` on a `sqlstore.Store`: you supply the table name, scan and bind functions, and get generic CRUD with `ErrConflict` and `ErrNotFound` semantics. Wrong package for a key-value cache (use `hop.top/kit/go/storage/sqlstore` directly) or for version history (wrap with `domain/version`).

## Use it when

- an entity needs persistence behind `domain.Service[T]` → `sqlite.NewSQLiteRepository[T](store, table, scan, scanRows, bind)`
- the table has a composite key → add `sqlite.WithPK(pk)` and use `GetByPK` / `DeleteByPK`
- mutations must leave an audit trail → `sqlite.NewSQLiteAuditRepository(store)` then `CreateTable`

## Quick start

```go
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
// gear
```

`widget` implements `domain.Entity` (`GetID() string`). Verified by `example_test.go` in this directory.

## Contract

- The caller owns the schema: create tables through `sqlstore.Options.MigrateSQL`; the repository only issues DML.
- `Get` and `Delete` address the first bind column as `id` unless `WithPK` is set.
- `Create` maps a primary-key collision to `domain.ErrConflict`; `Get`, `Update`, `Delete` map absence to `domain.ErrNotFound`.
- The audit table is created by `CreateTable`, not on construction.

## Neighbours

- `hop.top/kit/go/runtime/domain`: the interfaces, `Service[T]`, and the events it publishes.
- `hop.top/kit/go/runtime/domain/version`: append-only history over any repository.
- `hop.top/kit/go/storage/sqlstore`: the store this package borrows its `*sql.DB` from.
