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
- a step must be checked before the version is recorded → `with_verify` per migration, `Migrator::verify` for the final state
- other parts of the app react to migrations → `publisher(bus)`: `kit.core.migration.*` events, in order
- a failure must be inspected before undoing it → `manual_rollback()`, then `rollback_latest()`
- a `migrate status` view → `Migrator::status` (stored, target, pending per schema)

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

- Feature `upgrade` builds on `bus` (context, events) plus `thiserror`. Authority: the crate [feature table](../../README.md#features).
- `compare_versions`, `pending`, `Migration` and `SchemaDriver` build on wasm32; `Migrator`,
  `configfile` and the version-file helpers are compiled off wasm32 only.
- Pending is `stored < version <= target` per schema, in semver order; the version recorded is the
  last applied migration's, under the data dir (default `<XDG data home>/hop/<tool>`, or `data_dir`).
- A failed `up` or `verify`, or a context done before the next `up` (`is_verify`, `is_canceled`),
  stops the run; in auto mode applied `down`s run newest first on a fresh context, else the backup
  is restored. A failed verify undoes its own step too. Error texts match Go's.
- A prune failure goes to `on_warning` (default stderr, Go's `log.Printf` text); never fatal.
- Registration is per `Migrator`; Go's process-wide default registry has no Rust counterpart, since
  Rust has no `init()` to fill it from. Only the `configfile` driver is ported.
- Parity: [`contracts/upgrade-v1`](../../../../../contracts/upgrade-v1/README.md), run by
  `tests/upgrade_conformance.rs` (`make test-parity-upgrade`).

## Neighbours

- `hop_top_kit::config` (src/config/): in-document `schema_version` migrations for YAML config, applied on load
- `hop_top_kit::sqldb` (src/sqldb.rs): numbered SQL migrations inside one SQLite database

## See also

- [`go/core/upgrade`](../../../../../go/core/upgrade/README.md), the Go reference
