# breaker

## What it answers

"Which circuit breakers are registered in this process, what state are they
in, and how do I close one from the command line?" This is the `breaker`
subcommand tree only. To create, configure, or wrap operations with a breaker,
use `hop.top/kit/go/core/breaker`.

## Use it when

- host CLI should expose `breaker list|show|reset` → `root.AddCommand(breaker.Cmd())`
- root error bridge must map a missing name to exit 1 → `breaker.IsNotFound(err)`
- operator wants every breaker closed at once → `<tool> breaker reset --all --yes`
- machine-readable state for scripts → `<tool> breaker list --format json`

## Quick start

```go
b := bpkg.New("demo-http")
defer bpkg.Unregister("demo-http")
b.Trip("upstream 503")
fmt.Println("before:", b.State())

cmd := breaker.Cmd()
cmd.SetOut(os.Stdout)
cmd.SetArgs([]string{"reset", "demo-http"})
if err := cmd.Execute(); err != nil {
    fmt.Println("error:", err)
}
fmt.Println("after:", b.State())
```

Prints `before: open`, `reset breaker "demo-http"`, `after: closed`
(`bpkg` is `hop.top/kit/go/core/breaker`).

## Contract

- Scope: the registry of the current process only. Breakers in other
  processes are invisible; cross-process introspection is out of scope.
- Exit codes: 0 ok, 1 name not found, 2 usage error. Not-found is deliberately
  exit 1, not the kit-wide exit 3, and survives `RunE` envelope wrapping via
  `AsCLIError`.
- `reset --all` refuses to run without `--yes`; `reset` with neither a name
  nor `--all` is a usage error.
- `list` rows: `name`, `state` (`closed|open|half-open`), `trips`,
  `last_reason`. `show` adds `last_trip_at` and per-counter values
  (`bytes`, `ops`). Both honor `--format` through
  `hop.top/kit/go/console/output`.
- Every leaf is annotated: `list`/`show` are read-only and idempotent,
  `reset` is a write and idempotent.

## Neighbours

- `hop.top/kit/go/core/breaker`: breaker construction, policies, registry
  (`New`, `Lookup`, `List`, `ResetAll`, `Snapshot`).
- `hop.top/kit/go/console/cli`: root command, side-effect and idempotency
  annotations this package stamps on its leaves.
- `hop.top/kit/go/console/output`: `--format` dispatch and envelope rendering.

## See also

- [Go primitives reference](../../../../docs/adopters/reference/go-primitives.md)
