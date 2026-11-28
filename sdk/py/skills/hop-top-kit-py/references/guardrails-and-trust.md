# Guardrails and trust

Open when a tool deletes or mutates data, restricts file or network access,
mints IDs, or migrates its own stores. References:
[guard destructive commands](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/guard-destructive-commands.md),
[upgrade and migrate](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/upgrade-and-migrate.md) and the
[Python SDK reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/py-sdk.md).

## Decisions

- Destructive commands: annotate the leaf `kit/side-effect` (tiers in
  `hop_top_kit.sideeffect`) and let the root gate it
  (`hop_top_kit.gate`, `hop_top_kit.policy`). `--dry-run` and `--policy`
  are root-only; `--confirm-token` appears on leaves annotated
  `kit/destructive-token: required`. `permissions:` and `callers:` policy
  rules are carried, never evaluated.
- Filesystem limits: `hop_top_kit.scope` (`default().enforce(path, Op.READ)`
  raises `ErrDenied`); its deny list is the shared contract.
- Offline: `--offline` is always on; `hop_top_kit.netpolicy` guards
  `urllib` (`is_offline`, `offline_scope`).
- IDs: `hop_top_kit.id` (`new_id`, `parse_id`, `TypeId`), pinned to the
  shared TypeID corpus.
- Migrations: `hop_top_kit.upgrade`. `Migrator(tool, version)`, `add_driver`,
  `run(cancel)` is synchronous with a `threading.Event`. Drivers in
  `hop_top_kit.upgrade.drivers`: `ConfigFileDriver`, `FsDirDriver`,
  `SqliteDriver`, `PostgresDriver`.
- SQLite then Postgres: `SqliteDriver(path)` needs no extra. For a pooled
  handle use `SqliteDriver(path, connect=...)` (backup only, no restore).
  `PostgresDriver(connect, table)` ships no client library and no kit extra
  covers one: install your own PEP 249 driver (psycopg 3 or psycopg2) and pass
  a `connect` callable that returns a new connection each call, at least two
  at once, no transaction-mode pooler. Without a backup a failed `up` ends
  not restored: make each Postgres `up` one transaction. Details:
  [drivers README](https://github.com/hop-top/poly-kit/blob/main/sdk/py/hop_top_kit/upgrade/drivers/README.md).
- A Go and a Python `Migrator` on one table and schema name exclude each
  other. Not ported: the `migrate` command tree and a TiDB driver.

## Check coverage first

Cell `unverified`: `hop_top_kit.netpolicy`, delegation safety
(`hop_top_kit.safety`), `hop_top_kit.provenance`. Cell `partial`: redaction,
consent grants, side-effect seams, upgrade and migrations.

The circuit breaker, event policy engine, operating mode, audit log,
identity and utilities have no module here: [other ports](other-ports.md).
