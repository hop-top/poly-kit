# upgrade-v1

## What it answers

What versioned state migrations in `kit/core/upgrade` must do, independent of language: how migration
versions order, which migrations are pending between a stored and a target version, and what the runner
does around them: the backup it takes, the order it applies and rolls back in, the version it records after each migration,
the backups it prunes and the exact error texts; and how one JSON document migrates by its own version field. Wrong place for self-update (check, download, replace);
that half of the package is Go-only and has no vectors.

## Use it when

- you port or change `Migration`, `Migrator`, `SchemaDriver` or the version order in any SDK: run both runners
- you change migration behaviour in Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-upgrade   # every runner; one alone: see Runners
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted,
not that the vector should be edited. Each file carries `version: "v1"` and a `description` stating the
rules it pins.

| File, section | Pins |
|---------------|------|
| `versions.json` `compare[]` `{a, b, cmp}` | lenient semver order: `v` and build metadata dropped, missing or non-numeric parts 0, pre-release identifier rules; checked both ways |
| `versions.json` `pending[]` | `from < version <= to` for one schema, ascending, versions as registered |
| `document.json` `scenarios[]` | the in-memory JSON document migrator: chain by version field, refusing newer documents, missing or non-integer versions, gaps, invalid steps, step and verify failures (input untouched), byte-identical when current |
| `migrator.json` `scenarios[]` | `run`, `rollback_latest` and `status` steps against a recording driver (optionally version-storing or lockable): calls in order, error text, history, version files, stored versions, backup directories and their saved starting versions, published events, logged warnings |

What the scenarios pin, beyond order: a re-run applies nothing; the version is recorded after each
migration's `up` and `verify`, before the next `up` (a stopped run leaves the last applied version, and a
re-run applies only what is left); a failure runs applied `down`s newest first and restores the backup the
moment one is missing or fails; a failed `verify` (per migration or final) rolls back including the verified
step; a context canceled between steps stops before the next `up` (rollbacks still run); an explicit data
dir replaces XDG; an own registry ignores the default one; a prune failure is logged, never fatal; events
come in order under `kit.core.migration.*`.

The recorded version describes the data after any rollback. Each record writes the version file, then calls
the driver's `set_version`; a driver with `stores_version` gets only `set_version`, no version file. In auto
mode each successful `down` records the version before that migration (the starting version for the first),
a restore records the starting version (`""` too: logged as `set_version ` with nothing after it), and a
final-verify failure rolls back every step to the starting version. A failed record stops a rollback (no
restore after a `down`) and the error carries both failures. Manual mode undoes and records nothing after a
failure or a cancel: the version stays the last applied migration's. The Migrator saves the starting version
beside each backup in `<backup dir>.version`; `rollback_latest` restores, then records it. A backup without
that file is restored, the version left as it is, and the error says so. Pruning removes the file too.
A scenario's `baseline` map gives the baseline function: for a store reporting `""` it runs after the read,
before pending, backup and any `up`. A non-empty answer is recorded like any version and publishes
`baselined`; `""` changes nothing; a failing function or record is `baseline: <error>`, with no backup.
A failed `set_version` is that step's failure (`record <version> failed: <error>`, event stage `record`):
`up` completed, so the rollback includes the step, which is not in `history`. The file is written first, so
it ends at the starting version in auto mode and at the failed step's version, ahead of the driver, in manual mode.
A driver with `lock` is locked before its version is read and unlocked after its run ends, rollback included, on every
path once held (`lock` and `unlock` are logged, as is each `version` read). A `lock` error is `lock: <error>`, no
unlock; `lock_blocked` fails with the context's error; `unlock_fail` only logs `warning: unlock <schema>: <error>`.
Kept from Go: history keeps migrations a later failure rolled back; a failing first `up` rolls nothing back.

Rust has no default registry: its runner treats every scenario as `registry: own`. No vector holds equal
versions (Go sorts them unstably). Runners are strict: unknown fields, a `version` other than `v1` and a corpus file no runner consumes fail the run. A port that has not
ported a file yet lists it in its runner as pending by name, with a reason, until it consumes it: Python, TypeScript and PHP list `migrator.json` today.

## Runners

| Port | Runner | Run alone |
|------|--------|-----------|
| Go | `go/core/upgrade/upgrade_conformance_test.go` | `go test ./go/core/upgrade/ -run '^TestConformanceUpgrade'` |
| Rust | `sdk/rs/tests/upgrade_conformance.rs` | `cargo test --features upgrade --test upgrade_conformance`, from `sdk/rs` |
| Python | `sdk/py/tests/test_upgrade_conformance.py` | `pytest tests/test_upgrade_conformance.py`, from `sdk/py` |
| TypeScript | `sdk/ts/src/upgrade/conformance.test.ts` | `pnpm vitest run src/upgrade/conformance.test.ts`, from `sdk/ts` |
| PHP | `sdk/experimental/php/tests/Upgrade/UpgradeConformanceTest.php` | `vendor/bin/phpunit tests/Upgrade/UpgradeConformanceTest.php`, from `sdk/experimental/php` |

## See also

- [`go/core/upgrade`](../../go/core/upgrade/README.md)
- [`sdk/rs/src/upgrade`](../../sdk/rs/src/upgrade/README.md)
