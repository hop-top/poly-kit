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
