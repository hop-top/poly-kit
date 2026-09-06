# serve

## What it answers

Which services `<tool> serve [service]` runs, in what order, and what exit
code the run ends on: Registry, Resolve, StartOrder, ExitCodeFor, Supervisor.
Mounting the cobra command and reading the `services.*` config block is the
job of `hop.top/kit/go/console/cli`; building a transport is the job of
`hop.top/kit/go/transport/...`.

## Use it when

- a tool has a long-running thing to serve → implement `Service` (Name,
  Start, Ready, Stop); register via `cli.WithService`
- start order, config validation, policy class, or bound address matter →
  also implement `Dependent`, `Validator`, `Classified`, or `Addressed`
- you need the runnable set without starting anything → `Resolve(reg, Request)`
- you need the exit code for an outcome → `ExitCodeFor`, `CodeFor`, `WorstOutcome`
- you are the supervisor → `NewSupervisor(reg, cfg, opts...).Run(ctx,
  outcome.Selected, configs)`; `SignalContext` for SIGINT/SIGTERM escalation
- you replace a kit-shipped service (the built-in `api`) → `Registry.Override`

## Quick start

`noopService` is any `serve.Service` implementation.

```go
reg := serve.NewRegistry()
reg.Register(noopService{name: "api"})
reg.Register(noopService{name: "mcp"})

configs := map[string]serve.Config{
    "api": {Enabled: true},
    "mcp": {Enabled: false},
}

// Supervisor form: every configured AND enabled service.
all := serve.Resolve(reg, serve.Request{Configs: configs})
fmt.Println(all.Selected, all.Skipped, all.Err == nil)

// Selector form: the named service, even when disabled.
one := serve.Resolve(reg, serve.Request{Args: []string{"mcp"}, Configs: configs})
fmt.Println(one.Selected, one.Explicit)

// Unknown name: the refusal already carries its exit code.
bad := serve.Resolve(reg, serve.Request{Args: []string{"nope"}, Configs: configs})
fmt.Println(bad.Err.ExitCode, serve.ExitCodeFor(serve.OutcomeUnknownService))
```

Prints `[api] [mcp] true`, `[mcp] true`, `3 3`.

## Contract

Behavior is specified in [serve-lifecycle.md](../../../docs/contracts/serve-lifecycle.md);
this package is the authority for signatures only. Points a caller gets wrong:

- `Name()` matches `^[a-z][a-z0-9-]*$` and is not `all`, `none`, `list`;
  `Register` panics on an invalid or duplicate name.
- `Start` calls `ready()` exactly once, after every acquisition that can fail
  deterministically. Nil after cancellation is a clean stop; an error is a failure.
- `Config.Enabled` defaults to false. The selector form ignores enablement;
  the supervisor form resolving to zero services exits 2, not 0.
- Exit codes: clean stop 0; invalid selection, config invalid, no services 2;
  unknown service 3; policy denied 5; start failed, runtime crash, shutdown
  timeout 1. An outcome missing from the table exits 1.
- Ports reproduce [contracts/parity/serve.json](../../../contracts/parity/serve.json).

## Neighbours

- `hop.top/kit/go/console/cli`: `serve` command, `WithService`,
  `WithServiceOverride`, `WithServicePolicy`, `services.*` resolution
- `hop.top/kit/go/transport/transportsvc`: transport-backed `Service` implementations
- `hop.top/kit/go/runtime/bus`: `Event`, `TopicMap`, `ValidateTopic`

## See also

- [Build a transport service](../../../docs/adopters/guides/build-a-transport-service.md)
- [Migrate to served commands](../../../docs/adopters/guides/migrate-to-served-commands.md)
- [Parity harness](../../../contracts/parity/README.md)
