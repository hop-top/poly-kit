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
- gate a command before RunE → `engine.Authorize(cmd)`; for a served caller → `engine.AuthorizeFor(cmd, caller)`
- read a served caller's budget, or whether anyone may run cmd → `engine.BudgetFor(caller)`, `engine.RefusedForEveryone(cmd)`
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
fmt.Println(errors.Is(e.RecordOp(del), policy.ErrMaxOpsExceeded))
```

## Contract

- YAML: `name` (default: file stem), `allow` (class → verb globs),
  `max_ops`, `require_confirm` (path globs), `callers`, `permissions`
  (served-call rules, `celpermission` compiles them). Unknown top-level
  keys are ignored; `Load` refuses a malformed `callers`/`permissions`.
- `allow`: no map permits everything; a class with an empty list is
  refused; an absent class is permitted; read and untagged always pass.
  An expanded tier (`write-shared`, …) is answered by its own entry,
  else its legacy class (`write`, `destructive`).
- `callers`: rules matching `principal`, `tenant` (globs; `*` matches
  anything) and `scope` (held by the credential; the owner holds all),
  with `allow`, `max_ops`, `window` (default `1h`). The first match
  answers the classes it declares, the policy's `allow` the rest; a nil
  `Caller` (CLI, unestablished call) gets the policy's own rules.
  `BudgetFor` keys a budget by policy, rule, principal and tenant; cli
  counts it on a `cmdsurface.UsageLedger`.
- Verb = command path minus root; `path.Match` globs; `prefix:*` matches `prefix` and below.
- `RecordOp` returns `ErrMaxOpsExceeded` once the count exceeds
  `MaxOps` (0 = unlimited; `NewEngine(p, n>0)` overrides); cli maps it
  to `output.RateLimitedError`, exit 64.
- Engine is per-invocation, not concurrency-safe; a nil or zero Engine
  default-permits. `SideEffect` values equal `cli.SideEffect*`.

## Neighbours

- `hop.top/kit/go/console/cli`: `WithPolicy`, RunE middleware, `--max-ops`,
  `--confirm`, typed tokens; `output`: `RateLimitedError`.
- `hop.top/kit/go/core/scope`, `go/runtime/policy`, `go/core/breaker/policy`:
  path allow/deny and breaker policy, separate engines.

## See also

- [docs/adopters/reference/go-primitives.md](../../../../docs/adopters/reference/go-primitives.md)
