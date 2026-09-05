# withcel

## What it answers

The one-line constructor for a CEL-backed `policy.Engine`: `withcel.New(cfg)` equals `policy.NewEngine(cfg, policy.WithEvaluator(cel.New()))`. Import it from `main` or the wiring layer only; libraries import `policy` and stay free of `cel-go`. Wrong package when you bring your own evaluator (call `policy.NewEngine` directly).

## Use it when

- an adopter wants guards from a YAML file with no ceremony → `withcel.New(cfg)` then `policy.Wire(b, eng)`
- resolvers must be attached → pass `policy.WithPrincipalResolver(...)`, `policy.WithStageResolver(...)` as extra options

## Quick start

```go
cfg, err := policy.ParseConfig([]byte(rules))
if err != nil {
	panic(err)
}
eng, err := withcel.New(cfg) // policy.NewEngine + policy.WithEvaluator(cel.New())
if err != nil {
	panic(err)
}
// In production: policy.Wire(b, eng) subscribes eng to the bus.
err = eng.Decide("kit.runtime.state.pre_transitioned", map[string]any{
	"principal": map[string]any{"role": "engineer"},
	"resource":  map[string]any{},
	"context":   map[string]any{},
	"payload":   map[string]any{"To": "CANCELED"},
})
fmt.Println(errors.Is(err, domain.ErrConflict), err)
// true policy "admin-only-cancel" denied: only admin may cancel
```

`rules` is a `policies:` YAML document with one `admin-only-cancel` rule on `kit.runtime.state.pre_transitioned`; see `example_test.go` in this directory, which verifies the snippet.

## Contract

- Every policy compiles at `New`; a bad expression fails construction, not the first event.
- Denials wrap `domain.ErrConflict` (HTTP 409, CLI exit 4); zero matching policies means allow.
- Options after the evaluator are passed through unchanged, so `withcel.New(cfg, opts...)` accepts every `policy.EngineOption`.

## Neighbours

- `hop.top/kit/go/runtime/policy`: engine, config schema, `Wire`.
- `hop.top/kit/go/runtime/policy/cel`: the evaluator this package installs.
- `hop.top/kit/go/runtime/policy/e2e`: runnable adopter stories, start with `story_use_cel_default`.
- A future external-engine wiring package (Cedar, OPA) would sit beside this one and use the same `policy.WithEvaluator` seam.

## See also

- [Configure bus enforcement](../../../../docs/adopters/guides/configure-bus-enforcement.md)
