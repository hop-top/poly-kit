# upgrade-v1

## What it answers

What versioned state migrations in `kit/core/upgrade` must do, independent of language: how migration
versions order, which migrations are pending between a stored and a target version, and what the runner
does around them: the backup it takes, the order it applies and rolls back in, the version it records,
the backups it prunes and the exact error texts. Wrong place for self-update (check, download, replace);
that half of the package is Go-only and has no vectors.

## Use it when

- you port or change `Migration`, `Migrator`, `SchemaDriver` or the version order in any SDK: run both runners
- you change migration behaviour in Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-upgrade
# or separately:
go test ./go/core/upgrade/ -run '^TestConformanceUpgrade'
cargo test --features upgrade --test upgrade_conformance   # from sdk/experimental/rs
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted,
not that the vector should be edited. Each file carries `version: "v1"` and a `description` stating the
rules it pins.

| File, section | Pins |
|---------------|------|
| `versions.json` `compare[]` `{a, b, cmp}` | lenient semver order: `v` and build metadata dropped, missing or non-numeric parts 0, pre-release identifier rules; checked both ways |
| `versions.json` `pending[]` | `from < version <= to` for one schema, ascending, versions as registered |
| `migrator.json` `scenarios[]` | `run`, `rollback_latest` and `status` steps against a recording driver: calls in order, error text, history, version files, backup directories |

What the scenarios pin, beyond order: a re-run applies nothing; the recorded version is the last applied
migration's, not the target; a failure runs applied `down`s newest first and restores the backup the moment
one is missing or fails; a failing first migration rolls nothing back; history keeps rolled-back
migrations; the version file is written before the driver hears of it.

Equal versions have no defined order (Go sorts unstably), so no vector holds a tie. Runners are strict:
unknown fields, a `version` other than `v1` and a corpus file no runner consumes all fail the run.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/core/upgrade/upgrade_conformance_test.go` (`TestConformanceUpgrade*`) |
| Rust | `sdk/experimental/rs/tests/upgrade_conformance.rs` |

## See also

- [`go/core/upgrade`](../../go/core/upgrade/README.md)
- [`sdk/experimental/rs/src/upgrade`](../../sdk/experimental/rs/src/upgrade/README.md)
