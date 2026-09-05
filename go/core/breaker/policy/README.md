# policy

## What it answers

How to cap cumulative bytes or operations as a `failsafe.Policy`, so the
cap composes with rate limiters, bulkheads and circuit breakers in one
executor. If you want the kit breaker with config, bus events and
`Record`, use `go/core/breaker`; it builds these policies for you.

## Use it when

- you wire failsafe-go directly and need a byte budget: `NewVolume[R]().WithMaxBytes(n).WithReader(r).Build()`
- same for an operation budget: `NewCount[R]().WithMaxOps(n).WithReader(r).Build()`
- you want a callback when the cap is crossed: `OnExceeded(func(n int64))`

## Quick start

```go
var ops atomic.Int64
p := bpolicy.NewCount[any]().
    WithMaxOps(2).
    WithReader(ops.Load).
    Build()
exec := failsafe.With[any](p)

for i := 0; i < 3; i++ {
    err := exec.Run(func() error { ops.Add(1); return nil })
    fmt.Println(i, errors.Is(err, bpolicy.ErrThresholdExceeded))
}
// Output:
// 0 false
// 1 false
// 2 true
```

## Contract

- The policy never counts; it reads the caller's counter through `Reader` on every pre-execute check. Back it with `atomic.Int64.Load` or something equally safe.
- Refusal happens when the reading is at or above the cap before the execution starts; the execution that crosses the cap still runs.
- `WithMaxBytes` / `WithMaxOps` and `WithReader` are required; `Build` panics without them.
- Refusals surface as `ErrThresholdExceeded` through the executor. `breaker.Allow` maps it to `breaker.ErrBrokenCircuit`.
- `OnExceeded` fires once per crossing with the observed value.

## Neighbours

- `go/core/breaker`: `MaxBytes(n)`, `MaxOps(n)`, `Record`, `WrapBytes`, bus topics.
- `github.com/failsafe-go/failsafe-go`: the executor and the stock policies these sit beside.

## See also

- [breaker/README.md](../README.md)
