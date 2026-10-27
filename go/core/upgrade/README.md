# upgrade

Versioned state migrations and self-update for kit binaries.

`Checker` polls release feeds, downloads upgrade artifacts,
verifies them, and replaces the running binary. Lifecycle
events are emitted on the kit bus when `WithPublisher` is
wired.

## Default topics

| Topic                              | When |
|------------------------------------|------|
| `kit.core.upgrade.released`        | Check observes a new latest version |
| `kit.core.upgrade.downloaded`      | Upgrade fetched the asset successfully |
| `kit.core.upgrade.installed`       | Upgrade replaced the running binary |
| `kit.core.upgrade.snoozed`         | user deferred notification |

Without `WithPublisher` the checker is silent on the bus —
emission is opt-in to preserve the historical behavior.

## Adopter rebrand

```go
import "hop.top/kit/go/core/upgrade"

c := upgrade.New(
    upgrade.WithPublisher(pub),
    upgrade.WithTopicPrefix("myapp.core.upgrade"),
)
// emits: myapp.core.upgrade.{released,downloaded,installed,snoozed}
```

`WithTopics` overrides individual actions; empty fields keep
the default. Publishing is best-effort, fire-and-forget on a
goroutine — it never blocks or fails the upgrade flow.

## Migrations

`Migrator.Run(ctx)` brings each `SchemaDriver` from its stored version
to the target: backup, `Up` then `Verify` per pending migration in
semver order, the `WithVerify` final check, then the version file.
A failure (`ErrVerify` for checks, `context.Canceled` between steps)
rolls back: `Down`s newest first, else the backup.

| Option | Effect |
|--------|--------|
| `WithRegistry(NewRegistry())` | own migrations; default: `RegisterMigration`'s registry |
| `WithDataDir(dir)` | version files and backups under `dir`, not XDG |
| `WithVerify(fn)` | final-state check per schema |
| `WithEventPublisher(p)` | `kit.core.migration.*` events, in order |
| `WithManualRollback()`, `WithBackupRetention(n)` | no auto rollback; backups kept |

Parity:
[`contracts/upgrade-v1`](../../../contracts/upgrade-v1/README.md)
(`make test-parity-upgrade`); Rust: `hop_top_kit::upgrade`.

## Sub-packages

- [`driver/`](driver/README.md): `upgrade.SchemaDriver` backends, which store holds the schema version and how it is backed up.
- [`skill/`](skill/README.md): renders the agent skill text that keeps a tool's binary current.
