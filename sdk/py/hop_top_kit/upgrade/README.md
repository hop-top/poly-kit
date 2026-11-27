# upgrade

## What it answers

How a Python tool checks for a newer release, orders versions the way kit does, registers versioned migrations and migrates one JSON document by its own version field. Wrong module for self-update (download and replace): that half of `go/core/upgrade` is Go-only. Wrong module for applying migrations to a database: the Migrator and drivers are not ported yet.

## Use it when

- startup notice of a newer release: `create_checker(CheckerOptions(...)).notify_if_available()`
- strict check for `upgrade --check`: `check()` raises `UpgradeCheckError`
- order or select migration versions: `compare_versions(a, b)`, `Registry.pending(schema, from_, to)`
- upgrade a stored JSON document: `migrate_document(bytes, steps, DocOptions(...))`

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
- Parity: [`contracts/upgrade-v1`](../../../../contracts/upgrade-v1/README.md), `versions.json` and `document.json`, replayed by `tests/test_upgrade_conformance.py`; `migrator.json` is listed there as pending.

## Neighbours

- Go reference: `go/core/upgrade`; Rust port: [`sdk/rs/src/upgrade`](../../../rs/src/upgrade/README.md)

## See also

- [`docs/adopters/guides/upgrade-and-migrate.md`](../../../../docs/adopters/guides/upgrade-and-migrate.md)
