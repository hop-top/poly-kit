# upgrade-v1

## What it answers

What versioned state migrations in `kit/core/upgrade` must do in any language: version order, pending
selection, and what the runner does around them (backup, apply and rollback order, versions recorded,
pruning, exact error texts); and how one JSON document migrates by its own version field. Not self-update
(check, download, replace): that half is Go-only, with no vectors.

## Use it when

- you port or change `Migration`, `Migrator`, `SchemaDriver` or version order: run every runner; change Go behaviour: add a vector first

## Quick start

```sh
make test-parity-upgrade   # every runner; one alone: see Runners
```

## Contract

Go is canonical; the vectors are the source of truth: a runner failure means the implementation drifted, not
the vector. Each file carries `version: "v1"` and a `description` of its rules.

| File, section | Pins |
|---------------|------|
| `versions.json` `compare[]` `{a, b, cmp}` | lenient semver order: `v` and build metadata dropped, missing or non-numeric parts 0, pre-release identifier rules; checked both ways |
| `versions.json` `pending[]` | `from < version <= to` for one schema, ascending, versions as registered; no vector has equal versions (Go sorts them unstably) |
| `document.json` `scenarios[]` | the in-memory JSON document migrator: chain by version field, refusing newer documents, missing or non-integer versions, gaps, invalid steps, step and verify failures (input untouched), byte-identical when current |
| `migrator.json` `scenarios[]` | `run`, `rollback_latest` and `status` steps against a recording driver (optionally version-storing, lockable or with unsupported backup or restore): calls in order, error text, history, version files, stored versions, backup directories and their saved starting versions, events, warnings |

What the scenarios pin, beyond order: a re-run applies nothing; the version is recorded after each
migration's `up` and `verify`, before the next `up` (a stopped run leaves the last applied version, a re-run
applies only what is left); a failure runs applied `down`s newest first and restores the backup the moment
one is missing or fails; a failed `verify` (per migration or final) rolls back including the verified step; a
context canceled between steps stops before the next `up` (rollbacks still run); an explicit data dir
replaces XDG; an own registry ignores the default; a prune failure is logged, never fatal; events come in
order under `kit.core.migration.*`; history keeps migrations a later failure rolled back; a failing first
`up` rolls nothing back.

The recorded version describes the data after any rollback. Each record writes the version file, then calls
`set_version`; a `stores_version` driver gets only `set_version`, no file. In auto mode each successful `down`
records the version before that migration, a restore records the starting version (`""` too, logged as
`set_version ` with nothing after it), a final-verify failure rolls back every step to the starting version,
and a failed record stops a rollback (no restore after a `down`), the error carrying both failures. Manual
mode undoes and records nothing after a failure or a cancel. The starting version is saved beside each backup
in `<backup dir>.version`; `rollback_latest` restores, then records it (without the file: restored, version
left as it is, the error says so); pruning removes the file too. A failed `set_version` is that
step's failure (`record <version> failed: <error>`, stage `record`): the rollback includes the step, not in
`history`; the file, written first, ends at the starting version (auto) or ahead of the driver (manual).
A scenario's `baseline` map gives the baseline function, run for a store reporting `""` after the read, before
pending and backup; a non-empty answer is recorded and publishes `baselined`, a failure is `baseline: <error>`.
A `lock` driver is locked before its version is read, unlocked after its run (rollback included) on every path
once held; `lock: <error>` means no unlock, `unlock_fail` only logs `warning: unlock <schema>: <error>`.
`rollback_latest` locks it too, per schema: `lock`, restore, `set_version`, `unlock`, no `version` read; a lock
error is `rollback <schema>: lock: <error>`. A driver's `backup_unsupported` or `restore_unsupported` makes that call (still logged) report the sentinel
(Go `ErrBackupUnsupported`, Rust `BackupUnsupported`). An unsupported backup is no failure: the run goes on,
leaves no backup directory or `.version` file and prunes nothing for that schema. A rollback needing a
restore it cannot run (`down` missing or failing, and no backup or `restore` unsupported) restores nothing:
no `reverted` event, the error ends `(rollback also failed: not restored: no backup was taken)` or `(...: not
restored: restore unsupported)` (Go `ErrNotRestored`, Rust `is_not_restored`), the version stays at the last
step still applied. `rollback_latest` fails `no backups found for <schema>` (no backup dir) or `not restored:
restore unsupported`; no record. Rust has no default registry (scenarios run as `registry: own`). Runners are strict: unknown fields, a `version`
other than `v1` and an unconsumed corpus file fail the run; an unported file is listed pending by name (none today).

## Runners

| Port | Runner | Run alone |
|------|--------|-----------|
| Go | `go/core/upgrade/upgrade_conformance_test.go` | `go test ./go/core/upgrade/ -run '^TestConformanceUpgrade'` |
| Rust | `sdk/rs/tests/upgrade_conformance.rs` | `cargo test --features upgrade --test upgrade_conformance`, from `sdk/rs` |
| Python | `sdk/py/tests/test_upgrade_conformance.py` | `pytest tests/test_upgrade_conformance.py`, from `sdk/py` |
| TypeScript | `sdk/ts/src/upgrade/conformance.test.ts` | `pnpm vitest run src/upgrade/conformance.test.ts`, from `sdk/ts` |
| PHP | `sdk/experimental/php/tests/Upgrade/Upgrade{,Migrator}ConformanceTest.php` | `vendor/bin/phpunit tests/Upgrade --filter Conformance`, from `sdk/experimental/php` |

## See also

- [`go/core/upgrade`](../../go/core/upgrade/README.md)
- [`sdk/rs/src/upgrade`](../../sdk/rs/src/upgrade/README.md)
