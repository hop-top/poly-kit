# config

## What it answers

How a kit-built CLI loads one typed config struct from layered files, env
and `-c key=value` overrides, writes values back with the right YAML type,
and hot-reloads on a signal. Where the files resolve is `go/core/xdg`; the
`config path | paths` subcommands are `go/console/cli/config`; a typed
schema with wizard, completion and validation is [`pkl/`](pkl/README.md).

## Use it when

- load in `PersistentPreRunE` → `config.Load(&cfg, config.Options{...})` fed by `root.ConfigArgs()`
- write a value that has a Go type → `config.SetValue(key, value, scope, opts)`; a genuine string → `config.Set(...)`
- write a value typed from a raw CLI arg → `config.SetValue(key, config.ParseScalar(raw), scope, opts)`
- hot reload → `config.New(&cfg, opts, config.WithReloadPublisher(pub))`, read via `Snapshot()`, `go r.WatchSignal(ctx, syscall.SIGHUP)`

## Quick start

```go
paths, overrides, err := root.ConfigArgs()
if err != nil { return err }
err = config.Load(&cfg, config.Options{
    UserConfigPath:   userPath,
    ProjectConfigPath: projectPath,
    ExtraConfigPaths: paths,
    Overrides:        overrides,
})
```

## Contract

- Do not register your own `-c` / `--config` flag: the console root owns it as a repeatable flag (extra config file path or dotted `key=value` override). Pass `root.ConfigArgs()` through; never re-read `viper.GetString("config")`.
- `Set` writes every scalar as `!!str`, so `Set("k", "0.9", ...)` emits `k: "0.9"` and a consumer decoding into `float64` fails at the next load. `SetValue` infers the tag from the Go type.
- `ParseScalar` recognises floats, ints, bools and null only; oversized numbers (`9223372036854775808`, `1e400`) and YAML 1.1 lookalikes (`yes`, `on`, `off`, `no`) stay strings.
- Reload: fields are immutable unless tagged `reload:"true"`; a changed immutable field returns `*ErrImmutableChanged` and keeps the old snapshot. Treat every `*T` from `Snapshot()` as immutable.
- Reload events `kit.config.snapshot.reloaded` / `kit.config.snapshot.reload_failed` publish only when `WithReloadPublisher` is set; `SourcePaths` order is system, user, project, then `ExtraConfigPaths`.

## Neighbours

- [`pkl/`](pkl/README.md): PKL schema, onboarding wizard, completion, value validation.
- `go/core/xdg`: base directories the config paths resolve under.
- `go/console/cli`: owns the global `-c` / `--config` flag.
- `go/console/cli/config`: `config path` / `config paths` subcommands.

## See also

- [Config reference](../../../docs/adopters/reference/config.md): CLI integration, `Set` vs `SetValue`, type coercion, migration, hot reload, bus events
- [Inspect config paths](../../../docs/adopters/guides/inspect-config-paths.md): precedence chain and which file wins
- ADR-0016: signal-driven hot reload design context
