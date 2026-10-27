# upgrade

## What it answers

How does stored state (a database, config, saved documents, caches) move forward when a new build
ships? The migration half of Go `go/core/upgrade`: versioned `Migration`s per schema, a `Migrator` that
backs up, applies the pending ones in semver order, records the version reached and rolls back on
failure. Self-update (release check, download, binary replace) is not ported.

## Use it when

- a release changes a stored format → `Migration::new("1.2.0", "layout", up)`, `with_down` when it can be undone
- startup must bring old data to the current build → `Migrator::run` before loading it; a re-run is a no-op
- the store is a set of files rewritten in place → `configfile::ConfigFileDriver`; anything else → `impl SchemaDriver`
- a failure must be inspected before undoing it → `manual_rollback()`, then `rollback_latest()`
- a `migrate status` view → `Migrator::status` (stored, target, pending per schema)

## Quick start

```rust
use hop_top_kit::upgrade::configfile::ConfigFileDriver;
use hop_top_kit::upgrade::{read_version_file_in, Migration, Migrator};

let tmp = tempfile::tempdir().unwrap();
let (data, doc) = (tmp.path().join("data"), tmp.path().join("layout.json"));
std::fs::write(&doc, r#"{"v":1}"#).unwrap();

let mut m = Migrator::new("mytool", env!("CARGO_PKG_VERSION")).data_dir(&data);
let path = doc.clone();
m.register(Migration::new("0.1.0", "layout", move || {
    let s = std::fs::read_to_string(&path)?;
    Ok(std::fs::write(&path, s.replace(r#""v":1"#, r#""v":2"#))?)
}));
m.add_driver(ConfigFileDriver::new([&doc]).with_name("layout").with_data_dir(&data));

m.run().unwrap();
assert_eq!(read_version_file_in(&data, "layout").unwrap(), "0.1.0");
m.run().unwrap(); // nothing pending
```

## Contract

- Feature `upgrade` pulls in `thiserror` only. Authority: the crate [feature table](../../README.md#features).
- `compare_versions`, `pending`, `Migration` and `SchemaDriver` build on wasm32; `Migrator`,
  `configfile` and the version-file helpers are compiled off wasm32 only.
- Pending is `stored < version <= target` for the driver's schema, in semver order (pre-releases
  included, a leading `v` ignored). The version recorded is the last applied migration's.
- Layout under the data dir (default `<XDG data home>/hop/<tool>`, as Go): `<schema>/version`,
  `backups/<schema>/pre-<first pending version>/`; the 5 newest backups are kept unless
  `backup_retention` says otherwise.
- On failure, applied `down`s run newest first; a missing or failing one restores the backup instead.
  A failing first migration rolls nothing back. Error texts match Go's.
- Differences from Go: migrations are registered on the `Migrator`, not a process global; steps are
  synchronous `Fn() -> Result`; `data_dir` overrides the XDG location; a prune failure is dropped,
  not logged. Only the `configfile` driver is ported.
- Parity: [`contracts/upgrade-v1`](../../../../../contracts/upgrade-v1/README.md), run by
  `tests/upgrade_conformance.rs` (`make test-parity-upgrade`).

## Neighbours

- `hop_top_kit::config` (src/config/): in-document `schema_version` migrations for YAML config, applied on load
- `hop_top_kit::sqldb` (src/sqldb.rs): numbered SQL migrations inside one SQLite database

## See also

- [`go/core/upgrade`](../../../../../go/core/upgrade/README.md), the Go reference
