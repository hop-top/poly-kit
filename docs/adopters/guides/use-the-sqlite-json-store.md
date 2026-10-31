# Persist JSON values in SQLite with sqlstore

> Audience: Go tool authors who want a small durable keyed store for
> JSON-serialisable values, with backup-before-migrate and optional
> at-rest encryption. For raw bytes by key see `go/storage/kv`; for
> the layer comparison see
> [storage abstractions](../concepts/storage-abstractions.md).

Package: `hop.top/kit/go/storage/sqlstore`. A `Store` keeps one `kv`
table (`key`, `stored_at`, `payload`) in a SQLite file. Values are
JSON-marshalled on `Put` and unmarshalled on `Get`.

## Open, put, get

```go
import (
    "context"
    "time"

    "hop.top/kit/go/storage/sqlstore"
)

type Profile struct {
    Name string `json:"name"`
}

func demo(path string) error {
    s, err := sqlstore.Open(path, sqlstore.Options{TTL: time.Hour})
    if err != nil {
        return err
    }
    defer s.Close()

    ctx := context.Background()
    if err := s.Put(ctx, "user:1", Profile{Name: "ada"}); err != nil {
        return err
    }
    var p Profile
    found, err := s.Get(ctx, "user:1", &p) // (false, nil) when absent or expired
    _ = found
    return err
}
```

- `Open` creates the file and its parent directory (mode `0750`) and the
  `kv` table.
- `Put` upserts and refreshes `stored_at`.
- `Get` returns `(false, nil)` for a missing key; a real failure is
  `(false, err)`. `dst` must be a non-nil pointer.
- Keys are plain strings with no namespacing; prefix them yourself.
- The API has `Put`, `Get`, `DB`, `Close` only. There is no `Delete` or
  `List`; use `DB()` for those (below).

## TTL

`Options.TTL > 0` makes `Get` report `(false, nil)` for entries whose
`stored_at` is older than the TTL. Zero or negative disables expiry.
Expired rows are not deleted. Prune them yourself:

```go
cutoff := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
_, err := s.DB().ExecContext(ctx, "delete from kv where stored_at < ?", cutoff)
```

`stored_at` is RFC 3339 UTC text, so string comparison orders correctly.

## Extra tables, raw SQL

`Options.MigrateSQL` runs once during `Open`, after the `kv` table
exists, as a single `Exec` (separate statements with `;`). Use it for
your own tables and indexes. `DB()` returns the shared `*sql.DB`; do not
close it directly, call `Close` on the store.

```go
s, err := sqlstore.Open(path, sqlstore.Options{
    MigrateSQL: `create table if not exists audit (at text, msg text);`,
})
```

## Back up before a migration

`BackupBeforeMigrate(dbPath, version, opts...)` writes a consistent
copy (SQLite `VACUUM INTO`) before you change schema. Call it before
`Open` / your migration.

```go
path, err := sqlstore.BackupBeforeMigrate(dbPath, 3,
    sqlstore.WithKeep(5),
)
if err != nil && !errors.Is(err, sqlstore.ErrRetention) {
    return err // no backup: do not migrate
}
// path == "" and err == nil: dbPath did not exist yet (first run)
```

| Option | Effect |
|--------|--------|
| `WithBackupDir(dir)` | write here instead of `<dbDir>/.dbs/` (created `0700`) |
| `WithKeep(n)` | keep newest `n` backups the naming recognises; `n <= 0` keeps all |
| `WithNaming(n)` | replace the `Naming` interface (`Name`, `Parse`) |
| `WithDB(db)` | snapshot through a handle you already hold on `dbPath` |

Default name: `<basename>.pre-v<version>.<UTC 20060102-150405>.bak`,
for example `app.db.pre-v3.20261031-120000.bak`. An existing name is
never overwritten; the attempt number is appended. The backup is
`0600`, fsynced and renamed into place, so a crash leaves no partial
file under a backup name.

`ErrRetention` means the new backup is complete but pruning old ones
failed; the returned path is valid. Also, `BackupBeforeMigrateContext`
takes a `context.Context`.

If the app opened the file with a different SQLite library (such as
mattn/go-sqlite3), pass `WithDB`: two SQLite copies in one process
do not share lock state.

## Back up to, and restore from, a blob store

`Backup` and `Restore` move the database through a
[`blob.Store`](store-files-with-blob.md), so the same call works for a
local directory or S3.

```go
if err := sqlstore.Backup(ctx, dbPath, dest, "backups/app.db"); err != nil {
    return err
}
// later, with every handle on dbPath closed:
if err := sqlstore.Restore(ctx, dbPath, dest, "backups/app.db"); err != nil {
    return err
}
```

- `Backup` with `dest == nil` falls back to `BackupBeforeMigrateContext`
  with version `0`. It errors if `dbPath` does not exist.
- `Restore` with `src == nil` is a no-op.
- `Restore` requires every connection on `dbPath` closed first, in this
  process and others. It downloads to a temp file, fsyncs, renames over
  `dbPath`, removes stale `-wal`, `-shm`, `-journal`, then fsyncs the
  directory. Failure before the rename leaves `dbPath` untouched. An
  error after it (message says "replaced") means the backup is already in
  place; remove any sidecar named in the error before opening.
- The restored file keeps the old file's permission bits, or `0600` if
  none existed.

## Encrypt values at rest

`NewEncryptedStore(inner, kp)` wraps a `*Store`. Values are
JSON-marshalled, encrypted with a key derived from the keypair, and
stored through the inner store. Keys stay plaintext. It has `Put`,
`Get`, `Close`.

```go
import "hop.top/kit/go/core/identity"

kp, err := identity.Generate() // or identity store LoadOrGenerate
if err != nil { return err }
es := sqlstore.NewEncryptedStore(inner, kp)
defer es.Close()
err = es.Put(ctx, "token", map[string]string{"v": "x"})
```

The key is bound to the keypair: losing the private key loses the data.
Persist the keypair with `identity.NewStore(dir).LoadOrGenerate()`. For
the engine flow and threat model see
[Encrypt engine data at rest](encrypt-engine-data.md) and
[engine security](../reference/engine-security.md).

Reading the inner store directly returns ciphertext, not your value;
do not mix `Store` and `EncryptedStore` writes under the same keys.

## See also

- [Storage abstractions](../concepts/storage-abstractions.md)
- [Inspect with Datasette](inspect-with-datasette.md): browse the `kv`
  table read-only.
