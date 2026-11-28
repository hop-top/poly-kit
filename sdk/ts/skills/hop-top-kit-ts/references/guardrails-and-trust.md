# Guardrails and trust

Open when adding delegation safety, path scope, offline enforcement, provenance, telemetry,
TypeIDs or upgrade migrations. Status per capability: [coverage](coverage.md). Depth:
[TypeScript API reference](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/ts-api-reference.md).

## Modules

| Import | Use for | Cue |
|--------|---------|-----|
| `@hop-top/kit/safety` | `--force` guard for destructive work | `safetyGuard(level, { force })`; levels `read`, `caution`, `dangerous` |
| `@hop-top/kit/scope` | filesystem path policy | `Default().check(path, Op.Read)`; deny list shared with Go |
| `@hop-top/kit/netpolicy` | `--offline` enforcement | `guardFetch`, `isOffline`, `isOfflineError` |
| `@hop-top/kit/provenance` | origin metadata on payloads | `withProvenance(data, { source, timestamp, method })` gives `{ data, _meta }` |
| `@hop-top/kit/telemetry` | consent-gated usage events | `Client`, `resolveMode`, `redact` |
| `@hop-top/kit/id` | TypeID primitive | mint and parse prefixed ids |
| `@hop-top/kit/upgrade` | release check, migrations | `createChecker`, `Migrator`, `migrateDocument` |

Matrix cell `unverified`: `@hop-top/kit/safety`, `@hop-top/kit/netpolicy`,
`@hop-top/kit/provenance`, `@hop-top/kit/telemetry`, and the event bus (no `exports` key for it
when this was written: not importable). Redaction, consent and side-effect seams
are `partial`; read their Evidence in coverage. Tell the user what is unpinned and test it.

## Delegation gates (in `cli`)

- Root `--confirm`, `--confirm-token`, `--max-ops`, `--policy`, `--dry-run` enforce a leaf's
  `kit/side-effect` annotation, set on `annotations` of the command.
- An unannotated leaf named `delete`, `remove`, `rm`, `destroy`, `purge` or `drop` is gated as
  destructive. A value outside the tier set is refused before the handler runs.
- Handlers under `--dry-run` call `isDryRun(cmd)` and `renderPlan`.
- Permission rules and per-caller budgets are loaded but never evaluated here.
- Guide: [guard destructive commands](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/guard-destructive-commands.md).

## Telemetry

- Default-denied: `record` does nothing without a granted consent decision and a mode other
  than `off` (`KIT_TELEMETRY_MODE`). This port reads consent; granting it is the `kit` binary.
- `record` never throws and returns at once; end with `await client.shutdown(ms)`.
- Redaction and consent here cover telemetry only, and the Go parity targets named for them are
  Go and Rust. The envelope names events freely (`event` plus `attrs`), unlike Go's command
  path. Read the Telemetry cell in coverage before saying it matches Go.

## Offline

`createCLI` installs `guardFetch` on `globalThis.fetch` for `--offline`; loopback stays open.
It does not cover `node:net`, `http.request`, database drivers or a client built with its own
`fetch`: those call `isOffline()` themselves or wrap their transport with `guardFetch`.

## Upgrade migrations

- `Migrator` from `@hop-top/kit/upgrade`; one driver per backend, each its own import:
  `@hop-top/kit/upgrade/drivers/configfile`, `.../fsdir`, `.../sqlite`, `.../postgres`.
- Postgres: `await openPostgresDriver({ db: pool, connect: () => pool.connect(), table })`;
  you own the `pg` Pool (at least two connections); no backup or restore.
- Same Postgres table and lock key as Go, so Go and TypeScript Migrators exclude each other.
  `SqliteDriver` locks with `BEGIN EXCLUSIVE` on `.lock.db`, which does not exclude other ports.
- Drivers take your `dataDir`; the Migrator keeps versions under it.
- In the kit repo, `make test-ts-upgrade-integration` runs driver tests that need native
  `better-sqlite3` or Docker; `make test-ts-sdk` does not.
- Not here: self-update (download and replace) and a migrate command tree: Go or the sidecar.
