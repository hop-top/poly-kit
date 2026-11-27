# upgrade

## What it answers

How does stored state (a database, config, saved documents, caches) move forward when a new build ships?
The migration half of Go `go/core/upgrade`: versioned `Migration`s per schema, a `Migrator` that backs up,
applies the pending ones in semver order, records the version after each and rolls back on failure. The
release check (`check`) says a newer build is out. Self-update (download, binary replace) is not ported.

## Use it when

- a release changes a stored format → `Migration::new("1.2.0", "layout", up)`, `with_down` when it can be undone
- startup must bring old data to the current build → `Migrator::run` before loading it; a re-run is a no-op
- the store is files rewritten in place → `configfile::ConfigFileDriver`; a SQLite file → `sqlite::SqliteDriver`; a directory → `fsdir::FsDirDriver`; anything else → `impl SchemaDriver`
- one JSON document carries its own integer version (a saved layout) → `migrate_value` / `migrate_document` with `DocStep`s, pure, wasm-safe
- a step must be checked before the version is recorded → `with_verify` per migration, `Migrator::verify` for the final state
- other parts of the app react to migrations → `publisher(bus)`: `kit.core.migration.*` events, in order
- a store predating the migrator is already at some version → `baseline(|ctx, schema| ..)`; several processes may migrate one store → `SchemaDriver::as_locker` returns a `Locker`, held for the schema's whole run
- a failure must be inspected before undoing it → `manual_rollback()`, then `rollback_latest()`; a `migrate status` view → `Migrator::status`
- the tool should say a newer release exists → `check::Checker::check` with the request in your transport (route it through `GuardedClient`), `notify` to print the notice; `CheckConfig::offline(globals.offline)` makes `--offline` skip it before any request or cache read

## Quick start

```rust
use hop_top_kit::bus::Context;
use hop_top_kit::upgrade::configfile::ConfigFileDriver;
use hop_top_kit::upgrade::{read_version_file_in, Migration, Migrator};

let tmp = tempfile::tempdir().unwrap();
let (data, doc) = (tmp.path().join("data"), tmp.path().join("layout.json"));
std::fs::write(&doc, r#"{"v":1}"#).unwrap();

let mut m = Migrator::new("mytool", env!("CARGO_PKG_VERSION")).data_dir(&data);
let path = doc.clone();
m.register(Migration::new("0.1.0", "layout", move |_ctx: &Context| {
    let s = std::fs::read_to_string(&path)?;
    Ok(std::fs::write(&path, s.replace(r#""v":1"#, r#""v":2"#))?)
}));
m.add_driver(ConfigFileDriver::new([&doc]).with_name("layout").with_data_dir(&data));

m.run(&Context::background()).unwrap();
assert_eq!(read_version_file_in(&data, "layout").unwrap(), "0.1.0");
m.run(&Context::background()).unwrap(); // nothing pending
```

## Contract

- Feature `upgrade` builds on `bus` (context, events) plus `thiserror`; `upgrade-sqlite` adds `sqldb`, `upgrade-fsdir` adds `tar` and `flate2`. Authority: the crate [feature table](../../README.md#features).
- `migrate_value` refuses newer documents and leaves the value untouched on any error; a current document
  returns from `migrate_document` borrowed, byte for byte. Keys stay ordered only with serde_json
  `preserve_order` (the `output` feature has it), else sorted.
- `compare_versions`, `pending`, `Migration`, `SchemaDriver`, `document` and the unsupported-backup errors
  build on wasm32; `Migrator`, `configfile` and the version-file helpers do not.
- Pending is `stored < version <= target` per schema, in semver order; each version is recorded after
  its `up` and `verify` under the data dir (default `<XDG data home>/hop/<tool>`, or `data_dir`) unless
  `stores_version` (no file). A crash re-applies at most the unrecorded migration: keep them idempotent.
- A failed `up`, `verify` or record (`StepKind::Record`), or a context done before the next `up`
  (`is_verify`, `is_canceled`), stops the run; in auto mode applied `down`s run newest first on a fresh
  context, each followed by a record of the version before it, else the backup is restored and the starting
  version recorded. A failed verify undoes its own step too. Manual mode keeps the last applied version;
  `rollback_latest` records the version saved beside the backup, under the driver's lock. Texts match Go's.
- A `backup` returning `BackupUnsupported` (boxed) runs without a backup: no directory, no prune. A rollback
  needing a restore it cannot run (no backup, or `restore` unsupported) restores nothing:
  `MigrateError::is_not_restored()` (the step's `rollback` is a `NotRestored`), no reverted event, the
  version stays at the last step still applied; `is_backup_unsupported()` finds the cause; `rollback_latest`
  with no backup fails alike. Free fns `is_not_restored`, `is_backup_unsupported` take any error.
- `check` never fails: errors sit in `CheckResult::error`, a fresh cache (default 4h) answers without a request, an offline check returns `Skipped::Offline` touching neither transport, cache nor state dir. `CheckConfig::tag_prefix("xray/v")` keeps only one component's releases, `include_prereleases(true)` offers prereleases; either lists releases (3 pages of 100 at most, highest semver, drafts skipped), one transport call per page; `CheckResult::tag` carries the tag the notice links.
- A prune failure goes to `on_warning` (default stderr, Go's `log.Printf` text); never fatal. Registration
  is per `Migrator` (no `init()` for a default registry).
- `sqlite::SqliteDriver::new(path)` backs up with `VACUUM INTO ?` (WAL rows included) on its own connection and restores by swapping the file (no open connection allowed, stale `-wal`/`-shm`/`-journal` removed); `.with_connection(Arc<Mutex<Connection>>)` backs up on yours, never closes it, and `restore` is `BackupUnsupported`.
- `fsdir::FsDirDriver::new(name, dir)` writes `<name>.tar.gz` (Go's layout, restorable both ways), restores via a temp directory then a swap, and refuses names or links leaving the directory. Both lock with `File::lock` (flock(2), as Go) on `<data dir>/<name>/.lock`, polling for `ctx`; native only.
- Parity: [`contracts/upgrade-v1`](../../../../contracts/upgrade-v1/README.md), run by `tests/upgrade_conformance.rs`.

## Neighbours

- `hop_top_kit::config` (src/config/): in-document `schema_version` migrations for YAML config, applied on load; `hop_top_kit::sqldb` (src/sqldb.rs): numbered SQL migrations inside one SQLite database

## See also

- [`go/core/upgrade`](../../../../go/core/upgrade/README.md), the Go reference
