# cel

## What it answers

The CEL backend for `runtime/policy`: compiles boolean expressions once, evaluates them against the activation map the engine builds. It lives in its own package so adopters who wire Cedar, OPA, or another engine never pull `github.com/google/cel-go`. Wrong package if you only want a working engine (use `policy/withcel`).

## Use it when

- you need the evaluator alone, for instance to validate rules at load time → `cel.New()` then `ev.Compile(name, expr)`
- you build an engine by hand → `policy.NewEngine(cfg, policy.WithEvaluator(ev))`

## Quick start

```go
ev, err := cel.New()
if err != nil {
	panic(err)
}
if err := ev.Compile("admin-only", `principal.role == "admin"`); err != nil {
	panic(err)
}
for _, role := range []string{"admin", "engineer"} {
	ok, err := ev.Eval("admin-only", map[string]any{
		"principal": map[string]any{"role": role},
		"resource":  map[string]any{},
		"context":   map[string]any{},
		"payload":   map[string]any{},
	})
	fmt.Println(role, ok, err)
}
// admin true <nil>
// engineer false <nil>
```

Verified by `example_test.go` in this directory.

## Contract

- Activation shape: `principal`, `resource`, `context`, `payload`, `stage`, each a dyn map. `stage` is empty unless `policy.WithStageResolver` is wired.
- Expressions must yield `bool`; a non-bool result is an `Eval` error.
- `Eval` on a name that was never compiled is an error; compile every policy at engine init.
- Re-`Compile` under the same name replaces the program. One `Evaluator` per engine, goroutine-safe.

## Neighbours

- `hop.top/kit/go/runtime/policy`: the engine, YAML schema, and the `Evaluator` interface this package satisfies.
- `hop.top/kit/go/runtime/policy/withcel`: one-line constructor wiring this evaluator into an engine.
- External engines (Cedar, OPA) plug into the same `policy.Evaluator` seam; no kit package ships one today.

## See also

- [Configure bus enforcement](../../../../docs/adopters/guides/configure-bus-enforcement.md)
