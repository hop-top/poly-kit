# Upgrade the binary and migrate its state

> Audience: Go adopters shipping a CLI who want it to check for new
> releases, replace itself, and move its on-disk state (databases,
> config, data dirs, JSON documents) forward across versions. Package
> reference: [`go/core/upgrade` README](../../../go/core/upgrade/README.md).

`hop.top/kit/go/core/upgrade` has two independent halves:

- `Checker`: poll a release feed, download, verify, replace the
  running binary.
- `Migrator`: run versioned `Up`/`Down` steps against named schemas
  with a backup first and automatic rollback on failure.

Use either alone.

## Part 1: self-update

### Prerequisites

- Releases published to GitHub (assets named with the OS and arch,
  e.g. `mytool_linux_amd64.tar.gz`), or a JSON endpoint of your own.
- A checksums asset next to the binaries (see "Verification").
- Your build stamps the running version (for example via `-ldflags`).

### 1. Build a Checker

```go
c := upgrade.New(
    upgrade.WithBinary("mytool", version),
    upgrade.WithGitHub("acme/mytool"),
)
```

Options (all in `config.go`): `WithGitHub(owner/repo)`,
`WithReleaseURL(url)` for a custom endpoint, `WithCacheTTL` (default
4h), `WithSnoozeDuration` (default 24h), `WithTimeout` (default 10s),
`WithStateDir` (default `$XDG_STATE_HOME/<binary>/upgrade`),
`WithChecksumURL`, `WithSkipVerify`, `WithTagPrefix("xray/v")` (this
tool's releases in a multi-component repo; other tags are ignored) and
`WithIncludePrereleases(true)`; either lists releases (3 pages of 100
at most) and takes the highest semver, where the default reads
`releases/latest`. With neither a repo nor a URL,
`Check` returns an error ("no release source configured").

A custom endpoint answers a GET with
`{"version":"1.3.0","url":"https://.../mytool_linux_amd64.tar.gz","notes":"..."}`.
For GitHub the asset is picked by matching the running OS and arch
(`amd64`/`x86_64`, `arm64`/`aarch64`) in the asset name; a leading `v`
on the tag is stripped. Release notes are cut at 1000 characters.

### 2. Expose an `upgrade` command

kit does not mount one for you. Wire the helper:

```go
cmd := &cobra.Command{
    Use:   "upgrade",
    Short: "Check for and install a newer release",
    RunE: func(cmd *cobra.Command, _ []string) error {
        return upgrade.RunCLI(cmd.Context(), c, upgrade.CLIOptions{
            AutoUpgrade: auto, // true: install without asking
            Quiet:       quiet, // true: silent when already current
            Out:         cmd.ErrOrStderr(),
        })
    },
}
```

`RunCLI` prints the update and notes, then asks `Upgrade now?
[y/N/snooze]` unless `AutoUpgrade` is set. The prompt reads os.Stdin;
for non-interactive callers (agents, CI) offer the `--auto` flag.
Answering `snooze` suppresses banners for the snooze duration.

Lower-level calls, if you build your own UI: `Check(ctx)` returns a
`*Result` (`Current`, `Latest`, `URL`, `Notes`, `UpdateAvail`, `Err`;
never nil, errors are in `Err`), `ShouldNotify(ctx)`, `Upgrade(ctx)`,
`Snooze()`, `WhatsNew(ctx)`.

### 3. Nudge at startup

```go
upgrade.NotifyIfAvailable(ctx, c, os.Stderr)
```

Prints one line (`[mytool] update available: 1.2.0 → 1.3.0`, then
the `mytool upgrade` hint) unless snoozed. Results are cached for
the TTL, so most runs make no network call; a failed check is silent.

### Verification and replacement

`Upgrade` downloads the asset beside the running binary, verifies its
SHA-256 against the checksums file, extracts the binary named
`BinaryName` from a `.tar.gz` (or takes a bare binary), then renames
it over the running executable.

- A checksums asset (name ending `checksums.txt`, `sha256sums` or
  `sha256sums.txt`) is found automatically on GitHub. With a custom
  endpoint, set `WithChecksumURL`. With neither, `Upgrade` refuses
  unless `WithSkipVerify(true)` (dev and testing only).
- `.zip` assets are not supported; publish `.tar.gz`.
- Verification is a checksum from the same release feed, not a
  signature check.

### Observe it on the bus

`upgrade.WithPublisher(pub)` emits `kit.core.upgrade.released`,
`.downloaded`, `.installed` and `.snoozed`; rename them with
`WithTopicPrefix("myapp.core.upgrade")` or per action with
`WithTopics`. Publishing is best effort and never fails an upgrade.
Without a publisher the Checker never touches the bus.

To tell a coding agent to keep the tool current, render the text with
[`go/core/upgrade/skill`](../../../go/core/upgrade/skill/README.md).

## Part 2: versioned state migrations

### 1. Pick a driver per store

A `SchemaDriver` names a store, reports its version and backs it up.
Shipped drivers (details in the
[driver README](../../../go/core/upgrade/driver/README.md)):

| Store | Package | Constructor |
|-------|---------|-------------|
| Config files | `driver/configfile` | `New(paths...)` |
| Data directory | `driver/fsdir` | `New(name, dirPath)` |
| SQLite file | `driver/sqlite` | `New(dbPath, opts...)` |
| Shared TiDB/MySQL | `driver/tidb` | `New(dsn, table, opts...)` (no backup: `ErrBackupUnsupported`) |
| Shared PostgreSQL | `driver/postgres` | `New(db, table, opts...)` over your `*sql.DB`; advisory lock (needs two or more connections, no transaction-mode pooler); no backup: `ErrBackupUnsupported` |

The driver's `Name()` is the key that ties migrations to it
(`Migration.Schema`). The file-backed drivers keep the version in a
file under the data dir; pass `WithTool("mytool")` so the path is
per-tool, and if you give the Migrator `WithDataDir`, give the driver
the same dir with its own `WithDataDir`.

### 2. Register migrations

```go
func init() {
    upgrade.RegisterMigration(upgrade.Migration{
        Version: "1.2.0",     // semver, pre-release allowed
        Schema:  "cache",     // the driver's Name()
        Up:      addIndexes,
        Down:    dropIndexes, // nil: no step-level rollback
        Verify:  checkIndexes, // optional; runs after Up
    })
}
```

`Register` panics when `Up` is nil. Pending steps for a schema are
those with `current < version <= target`, run in semver order.
`NewRegistry()` plus `WithRegistry` gives an isolated set (useful in
tests); `ResetRegistryForTest` clears the default.

### 3. Run them

```go
m := upgrade.NewMigrator("mytool", version,
    upgrade.WithVerify(checkFinalState),
    upgrade.WithBackupRetention(3), // default 5
)
m.AddDriver(fsdir.NewWithOptions("cache", cacheDir, fsdir.WithTool("mytool")))
if err := m.Run(ctx); err != nil {
    return err
}
```

Per schema, `Run` reads the stored version, backs the store up (under
`<data dir>/backups/<schema>/pre-<version>/`), applies each `Up` then
its `Verify`, records that migration's version, then runs the
`WithVerify` check. A crash between an `Up` and its record re-applies
that one migration on the next run, so keep migrations idempotent. On
failure it runs the applied `Down`s newest first, recording the
version before each, else restores the backup and records the version
the run started at. A failed verification satisfies
`errors.Is(err, upgrade.ErrVerify)`; a cancelled context is checked
between steps and wraps `context.Canceled`. `WithManualRollback()`
turns automatic rollback off. Old backups beyond the retention are
pruned after success.

A driver that cannot back its store up (a server database: `driver/tidb`)
returns `upgrade.ErrBackupUnsupported` from `Backup`: the run still goes
ahead, with no backup and no pruning for that schema. If a migration then
fails and a `Down` is missing or fails, nothing can restore the schema:
`Run` returns an error matching `upgrade.ErrNotRestored` (and
`ErrBackupUnsupported`) and publishes no `reverted` event, and the stored
version stays at the last step still applied. Give such a schema a `Down`
for every migration, or take a backup outside kit first.

`Status()` returns per-schema `Current`, `Target`, `Pending`;
`History()` the migrations applied this session.

#### Fresh and existing stores

A store that predates the migrator reports no version, so every
migration looks pending. The usual app creates its tables at the
current shape on first start, then runs the migrator. On a database it
just created, an additive migration (`ALTER TABLE ... ADD COLUMN`)
would apply on top of a table that already has the column and fail.
`WithBaseline` is what prevents that: when a driver reports `""`, the
Migrator calls it before it selects pending migrations, and records the
version it returns.

```go
func ensureTables(db *sql.DB) error { /* CREATE TABLE IF NOT EXISTS at the current shape */ }

m := upgrade.NewMigrator("mytool", version,
    upgrade.WithBaseline(func(ctx context.Context, schema string) (string, error) {
        // Inspect the live store: which shape is it at?
        has, err := columnExists(ctx, db, "versions", "live")
        if err != nil {
            return "", err // fails this schema's run, nothing applied
        }
        if has {
            return "1.2.0", nil // already at the shape of 1.2.0: stamp it
        }
        return "", nil // unversioned and older: every migration applies
    }),
)
if err := ensureTables(db); err != nil {
    return err
}
if err := m.Run(ctx); err != nil {
    return err
}
```

The function runs only for a store with no recorded version, once per
schema per run, never when a version exists. A non-empty answer is
recorded through the same path as a migration's version and publishes
`kit.core.migration.baselined`; migrations at or below it are not
applied, later ones run as usual. An empty answer changes nothing. An
error fails that schema (`baseline: ...`) before any backup or `Up`.

The introspection to copy is the one the document store already uses
(`engine/store/document.go`): its `live` column migration reads
`PRAGMA table_info(versions)` and adds the column only when it is
missing, because a fresh database got it from `CREATE TABLE`. Your
baseline function asks the same question of the live store and turns
the answer into a version. A fresh database and an upgraded one then
end at the same version with the same shape.

#### Several processes starting at once

A driver that also implements `upgrade.Locker`
(`Lock(ctx) (unlock func() error, err error)`) is locked before the
Migrator reads its version and unlocked when that schema's run ends:
the version read, the baseline, the backup, every `Up`, every version
record, a rollback and the backup prune all happen under the lock. A
second process blocks in `Lock` until the first unlocks (or its
context is done, which fails that schema with `lock: ...`), then reads
the version the first one recorded and finds nothing pending. An
`unlock` error is logged, not returned. `RollbackLatest` (the `migrate
rollback` command) takes the same lock around the restore and the version
record, so a manual rollback waits for a migrating process; a lock error
fails that schema's rollback (`rollback <schema>: lock: ...`). A driver
without `Locker` runs unlocked. In Rust the driver returns a `Locker` from
`SchemaDriver::as_locker`.

### 4. Give users a `migrate` command

```go
root.AddCommand(upgrade.MigrateCommand(m, v)) // v is your *viper.Viper
```

Subcommands: `migrate status` (read), `migrate run` (write-local,
honors `--dry-run` with a plan), `migrate rollback` (destructive-local;
only with `WithManualRollback()`, restores the latest backup),
`migrate history` (read). The tree is reserved, so it is never exposed
over a served surface. `rollback` is gated like any destructive
command; see [guard-destructive-commands](guard-destructive-commands.md).
Migration progress events (`kit.core.migration.*`) go to
`WithEventPublisher`.

## Migrating one JSON document in memory

For a settings or manifest document with an integer version field, no
I/O needed:

```go
steps := []upgrade.DocStep{{
    From: 1, To: 2, Note: "rename title",
    Apply: func(d map[string]any) (map[string]any, error) {
        d["name"] = d["title"]
        delete(d, "title")
        return d, nil
    },
}}
out, report, err := upgrade.MigrateDocument(doc, steps, upgrade.DocOptions{})
```

`{"version":1,"title":"x"}` becomes `{"name":"x","version":2}`; keys
come back sorted, numbers stay exact. `report.Applied` lists the steps.
Options: `VersionField` (dotted path, default `version`), `Latest`
(default: highest `To`), `MissingIsZero`. Errors satisfy `errors.Is`
with `ErrDocNewer` (document newer than known), `ErrDocVersion`,
`ErrDocGap`, `ErrDocStep`, `ErrDocInvalid`, or `ErrVerify`; on error
the input is untouched. `MigrateDocumentMap` is the same on a decoded
map and never modifies yours. Cross-language behavior is pinned by
[`contracts/upgrade-v1`](../../../contracts/upgrade-v1/README.md).

## See also

- [`go/core/upgrade` README](../../../go/core/upgrade/README.md)
- [Upgrade migrations on SQL backends](../../contributors/architecture/upgrade-sql-backends.md):
  version authority, locking, baseline, backup limits, and when to use
  `storage/sqldb.Migrate` instead
- [guard-destructive-commands](guard-destructive-commands.md)
- [hook-cli-into-bus](hook-cli-into-bus.md)
