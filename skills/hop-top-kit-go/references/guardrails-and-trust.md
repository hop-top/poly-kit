# Guardrails and trust

Open when: guarding destructive or agent-driven commands, limiting what a
tool may touch, redacting, keeping an audit trail, or upgrading the binary
and migrating its stored state.

## Decisions

| Need | Use |
|------|-----|
| confirm, cap and policy-gate destructive commands | `cli.WithPolicy` and `go/console/cli/policy`: `--confirm`, `--max-ops`, policy YAML; stricter off a terminal |
| filesystem path rules with default denies | `go/core/scope`; inspect with `kit scope show\|check` |
| budgets, rate limits, circuit breaking | `go/core/breaker`; inspect with `kit breaker list` |
| `--offline` refusals under the HTTP client | `go/core/netpolicy` |
| redact secrets and PII before output or egress | `go/core/redact` |
| tamper-evident audit log | `go/security`: `OpenAuditLog`, `Append`, `VerifyAuditLog` |
| declarative rules over bus events | `go/runtime/policy` |
| lifecycle stage of a tool | `go/core/stage` |
| per-capability consent | `go/core/consent` |
| mutation boundary with recording fakes | `go/runtime/sideeffect` |
| keypairs, JWT claims, derived keys | `go/core/identity`; sortable ids: `go/core/id` |
| retry with backoff and small helpers | `go/core/util` |
| avatar URLs from a seed | `go/core/avatar` |

## Upgrade and state migrations (`go/core/upgrade`)

Two independent halves: `Checker` (poll releases, verify, replace the
running binary) and `Migrator` (versioned `Up`/`Down` steps per named
schema, backup first, rollback on failure).

Add a migration backed by Postgres:

1. Open your own `*sql.DB` with any Postgres driver; kit imports none.
2. `postgres.New(db, "schema_versions", postgres.WithName("app"))` from
   `hop.top/kit/go/core/upgrade/driver/postgres` creates the version table
   and returns a driver that stores the version itself and takes an
   advisory lock, so several replicas apply each step once.
3. `m := upgrade.NewMigrator("mytool", version)`, `m.AddDriver(d)`.
4. `upgrade.RegisterMigration(upgrade.Migration{Version, Schema, Up, Down,
   Verify})`; `Schema` equals the driver's name, `Down` may be nil.
5. `m.Run(ctx)` at startup, or behind a `migrate` command.

Limits: pool of at least two connections; no transaction-mode pooler;
Postgres cannot back up or restore, so a failed step with no working
`Down` is reported not restored and the version stays at the last applied
step. Keep every `Up` idempotent: a crash before the version record
re-applies it. SQLite, directory and config-file drivers sit beside it in
`go/core/upgrade/driver`. Other ports implement the same `Migrator`
contract; see `other-ports.md`.

## Traps

- A command named `delete`, `remove`, `rm`, `destroy`, `purge` or `drop`
  is gated as destructive even without annotation; declare
  `kit/side-effect` to override either way.
- A `--dry-run` on a write leaf must preview (`cli.RenderPlan`) or opt out
  with a rationale; the confirm gate skips only for leaves that preview.
- The audit chain records the redacted invocation, never the output.

## Pages

- [guard destructive commands](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/guard-destructive-commands.md),
  [choose enforcement mode](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/choose-enforcement-mode.md),
  [side effects](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/sideeffect.md)
- [audit log](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/keep-a-tamper-evident-audit-log.md),
  [redact](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/redact.md),
  [scope](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/scope.md),
  [breaker](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/breaker.md)
- [upgrade and migrate](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/upgrade-and-migrate.md)
- [stage](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/stage.md),
  [consent](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/consent-grants.md),
  [utilities](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/util-helpers.md)
