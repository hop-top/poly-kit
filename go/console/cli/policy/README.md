# policy

## What it answers

"May an agent-driven invocation run this command, and how many mutating
ops has it used?" Loads a delegation-safety policy YAML and enforces it
per invocation. Path guardrails belong to `hop.top/kit/go/core/scope`;
runtime breaker policy belongs to `hop.top/kit/go/runtime/policy`.

## Use it when

- wire enforcement into an adopter root → `cli.WithPolicy(...)` in `hop.top/kit/go/console/cli`; it constructs the Engine
- read a policy file by path → `policy.Load(path)`
- read `$XDG_CONFIG_HOME/<tool>/policies/<name>.yaml` → `policy.LoadNamed(tool, name)` or `policy.Resolve(tool, name)`
- gate a command before RunE → `engine.Authorize(cmd)`
- charge a successful write or destructive run against the budget → `engine.RecordOp(cmd)`

## Quick start

```go
root := &cobra.Command{Use: "kit"}
del := &cobra.Command{
    Use:         "delete",
    Annotations: map[string]string{"kit/side-effect": "destructive"},
}
drop := &cobra.Command{
    Use:         "drop",
    Annotations: map[string]string{"kit/side-effect": "destructive"},
}
root.AddCommand(del, drop)

p := policy.Policy{
    Name:           "ops",
    Allow:          map[policy.SideEffect][]string{policy.SideEffectDestructive: {"delete:*"}},
    RequireConfirm: []string{"delete:*"},
}
e := policy.NewEngine(p, 1)

allowed, confirm, _ := e.Authorize(del)
fmt.Println("delete:", allowed, confirm)
allowed, _, reason := e.Authorize(drop)
fmt.Println("drop:", allowed, reason)

fmt.Println(e.RecordOp(del))
fmt.Println(errors.Is(e.RecordOp(del), policy.ErrMaxOpsExceeded))
```

## Contract

- YAML shape: `name`, `allow` (map of side-effect class to verb globs),
  `max_ops`, `require_confirm` (command-path globs). A missing `name`
  defaults to the file stem.
- `allow` semantics: no map at all permits everything; a class listed
  with an empty list refuses that class categorically; a class absent
  from the map is permitted. Read-tagged and untagged commands always
  pass.
- Verb = command path minus the root name. Patterns match via
  `path.Match`; `*` matches all; `prefix:*` matches `prefix` or `prefix <sub...>`.
- `RecordOp` counts every call and returns `ErrMaxOpsExceeded` once the
  count exceeds `MaxOps`; 0 means unlimited. `NewEngine(p, n)` with
  n > 0 overrides `p.MaxOps`.
- cli maps `ErrMaxOpsExceeded` to `output.RateLimitedError`, exit 64.
- Engine is per-invocation, not concurrency-safe. A nil or zero Engine
  default-permits and counts without a cap.
- `SideEffect` values must equal `cli.SideEffect*`: `read`, `write`,
  `destructive`, `interactive`.

## Neighbours

- `hop.top/kit/go/console/cli`: `WithPolicy`, RunE middleware, `--max-ops`
  and `--confirm` flags, typed-token confirmation.
- `hop.top/kit/go/console/output`: `RateLimitedError`, `ExitRateLimited`.
- `hop.top/kit/go/core/scope`: path allow/deny, a separate policy.
- `hop.top/kit/go/runtime/policy`, `hop.top/kit/go/core/breaker/policy`:
  breaker policy, a separate engine.

## See also

- [docs/adopters/reference/go-primitives.md](../../../../docs/adopters/reference/go-primitives.md)
