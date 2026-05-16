# hook

Thread-safe lifecycle hook system for kit extensions.

Extensions subscribe handlers to named hooks; the bus
dispatches them in priority order (lower runs first).
Internally delegates to `kit/runtime/bus` for transport while
preserving the priority + `DispatchAll` contract.

## Default topic shape

```
kit.ext.hook.<action>
```

Hook actions are open-ended and adopter-defined (e.g.
`before_run`, `after_close`). The 3-segment prefix
(`kit.ext.hook`) is configurable; the action segment is the
hook name itself.

Validation is best-effort at fire time — non-past-tense action
strings skip the publish (logged via the inner bus error path)
rather than fail construction.

## Adopter rebrand

```go
import "hop.top/kit/go/ai/ext/hook"

b := hook.NewBus(
    hook.WithHookTopicPrefix("myapp.hooks.lifecycle"),
)
// emits: myapp.hooks.lifecycle.<action>
```

The prefix MUST be exactly 3 lowercase segments. Trailing dot
is normalized — pass either `"myapp.hooks.lifecycle"` or
`"myapp.hooks.lifecycle."`.
