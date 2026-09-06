# driver

## What it answers

Which store holds the schema version, and how it is backed up and
restored, for one `upgrade.Migration.Schema`. Every backend implements
`upgrade.SchemaDriver`; migration logic itself lives in `go/core/upgrade`.

## Use it when

| Backend | Import | Constructor | Version lives in | Backup / Restore |
|---------|--------|-------------|------------------|------------------|
| `configfile/` | `hop.top/kit/go/core/upgrade/driver/configfile` | `New(paths...)`, `NewWithOptions(opts, paths...)` | version file | copies each managed file to and from `dest` |
| `fsdir/` | `hop.top/kit/go/core/upgrade/driver/fsdir` | `New(name, dirPath)`, `NewWithOptions(name, dirPath, opts...)` | version file | tar.gz of the directory; `Restore` removes the directory first |
| `sqlite/` | `hop.top/kit/go/core/upgrade/driver/sqlite` | `New(dbPath, opts...)` | version file | copies the database file |
| [`tidb/`](tidb/README.md) | `hop.top/kit/go/core/upgrade/driver/tidb` | `New(dsn, table, opts...)` | table row | no-ops; managed outside kit |

- pick `configfile` for YAML/TOML config the migration rewrites in place
- pick `fsdir` for a data directory (caches, per-project state)
- pick `sqlite` for a local database file
- pick `tidb` when the schema lives in a shared MySQL-compatible server

## Quick start

```go
d := configfile.NewWithOptions(
    []configfile.Option{configfile.WithTool("mytool")},
    "/etc/mytool/config.yaml",
)
m := upgrade.NewMigrator("mytool", "1.2.0")
m.AddDriver(d)
fmt.Println(d.Name())
// Output: config
```

## Contract

- `Name()` must equal `Migration.Schema` for the migrations that target it. Defaults: `config`, the `name` argument for `fsdir`, `sqlite`, `tidb`. `WithName` overrides where offered.
- Version file backends read and write `<XDG data dir>/hop/<tool>/<name>/version` via `upgrade.ReadVersionFile`; set the tool with `WithTool`; without it the path collapses to `<XDG data dir>/hop/<name>/version`. A missing file means version `""`.
- `Backup(dest)` and `Restore(src)` take directories; the Migrator drives them around `Migration.Up` and on rollback.

## Neighbours

- `go/core/upgrade`: `Migrator`, `Migration`, `RegisterMigration`, `MigrateCommand`.
- `go/core/xdg`: resolves the data directory the version file sits in.
- `go/storage/kv/sqlite`, `go/storage/kv/tidb`: the stores these drivers version.

## See also

- [upgrade/README.md](../README.md)
