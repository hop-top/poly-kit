# upgrade

## What it answers

How a Python tool checks for a newer release, orders versions the way kit does, registers versioned migrations, migrates one JSON document by its own version field and runs migrations across schema drivers with a `Migrator`. Wrong module for self-update (download and replace): that half of `go/core/upgrade` is Go-only. Ships the driver protocol, no real drivers yet.

## Use it when

- startup notice of a newer release: `create_checker(CheckerOptions(...)).notify_if_available()`
- strict check for `upgrade --check`: `check()` raises `UpgradeCheckError`
- order or select migration versions: `compare_versions(a, b)`, `Registry.pending(schema, from_, to)`
- upgrade a stored JSON document: `migrate_document(bytes, steps, DocOptions(...))`
- migrate stores: `Migrator(tool, version, bus=...)`, `add_driver(d)`, `run(cancel)`, `status()`, `rollback_latest()`

## Quick start

```python
from hop_top_kit.upgrade import DocStep, migrate_document

steps = [DocStep(1, 2, lambda d: {**d, "title": d.pop("name")}, note="rename")]
out, report = migrate_document(b'{"version": 1, "name": "x"}', steps)
```

## Contract

- Version order is kit's lenient one (`v` and build metadata dropped, missing parts 0, pre-release identifier rules), not `packaging`.
- `Registry.register` raises `ValueError` when `up` is missing; `register_migration` feeds `default_registry()`.
- Document errors subclass `DocumentError`: newer, version, gap, step, invalid, and `DocVerifyError` (also a `VerifyError`). The input is never modified; a current document comes back byte-identical.
- A driver has `name`, `version()`, `set_version(v)`, `backup(dest)`, `restore(src)`; optional `Locker.lock(cancel) -> unlock` and `VersionStorer.stores_version()` are found with `isinstance`. `backup`/`restore` raise `BackupUnsupported` when they cannot.
- `run(cancel)` takes a `threading.Event`; set, it stops before the next `up` and wakes a waiting lock. Failures are `MigrateError`; match with `is_not_restored`, `is_backup_unsupported`, `is_verify`, `is_canceled`.
- Events go to a `hop_top_kit.bus.Bus`; warnings to the `hop_top_kit.upgrade` logger.
- Parity: [`contracts/upgrade-v1`](../../../../contracts/upgrade-v1/README.md), all three files, replayed by `tests/test_upgrade_conformance.py`.

## Neighbours

- Go reference: `go/core/upgrade`; Rust port: [`sdk/rs/src/upgrade`](../../../rs/src/upgrade/README.md)

## See also

- [`docs/adopters/guides/upgrade-and-migrate.md`](../../../../docs/adopters/guides/upgrade-and-migrate.md)
