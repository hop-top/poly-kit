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
- keep a committed project file from setting secrets or widening a policy → `Options.Project: &config.ProjectRules{...}` (forbidden keys, narrow-only lists)
- a key holds a filesystem path → `Options.PathKeys: []string{"data.dir"}`; relative values resolve against the supplying file, not the cwd
- show where a value came from → `config.LoadWithOrigins(&cfg, opts, keys...)`
- load into a `map[string]any` with nested maps merging across layers → `Options.DeepMergeMaps: true`
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
- Layers merge as yaml.v3 decodes into an existing value: struct fields merge per field at any depth; scalars, slices, each map entry and `any` values are replaced whole; a null clears pointer, slice, map and `any` fields only; a wrong-kind value for a typed field is an error. Pinned by `contracts/config-v1/load.json`.
- `Set` writes every scalar as `!!str`, so `Set("k", "0.9", ...)` emits `k: "0.9"` and a consumer decoding into `float64` fails at the next load. `SetValue` infers the tag from the Go type.
- `ParseScalar` recognises floats, ints, bools and null only; oversized numbers (`9223372036854775808`, `1e400`) and YAML 1.1 lookalikes (`yes`, `on`, `off`, `no`) stay strings.
- `Options.Project` restricts `ProjectConfigPath` only; system, user, `-c` files, env and overrides merge as usual, and precedence is unchanged. A forbidden key (exact, prefix or secret-name segment) returns `*ErrProjectForbidden` naming file, key and the user config path. A `Narrow` list key merges against what the layers below project produced: `Union` adds, `Intersect` keeps entries both sides admit (`Match` defaults to equality). No lower list: the project list stands. Empty `Intersect` returns `*ErrNarrowEmpty`, never "no limit". A null project list cannot clear a lower one.
- Rules match the case-folded key path (`strings.ToLower` per segment, rule and key alike): `Owner_Profile` hits a `owner_profile` rule and `AI: {MODELS: {ALLOW: ...}}` is narrowed by `ai.models.allow`, because viper-based tools lower-case keys on merge. Every case variant present in the project file is narrowed. `ProjectRules.CaseSensitive` turns folding off. Kit's own struct decode is case-sensitive (yaml.v3), so folding only refuses or narrows more, never less.
- A mapping key containing `.` is read as its dotted path. `Forbidden` and `SecretKey` apply to that path. A dotted key overlapping a `Narrow` key is refused (`ErrProjectForbidden.Dotted`): write it nested. Other dotted keys (host names as map keys) pass.
- `Options.PathKeys` (dotted keys, case-folded per segment, `*` matches one segment; value a string or list of strings): a relative value becomes absolute. A file layer resolves against the directory of the file that supplied it, except a project `.tool/config.yaml` (named `config.yaml` inside a dot-directory), which resolves against the project root, the parent of `.tool`; `.tool.yaml` and `tool.yaml` resolve against their own directory. `EnvOverride` changes and `Overrides` (`-c key=value`) resolve against the cwd. Absolute and `~`-prefixed values are left as written; empty stays empty.
- `LoadWithOrigins` returns `map[string]KeyOrigin{Key, Layer, File}`; `Load` is unchanged. Layers: `default`, `system`, `user`, `project`, `extra`, `env`, `override`; `File` is set for file layers. No keys: every leaf (scalars, lists) that reached dst. With keys: those only; a mapping key reports the highest layer beneath it; unset keys are absent. Env is found by diffing dst around `EnvOverride`. Keys dst drops (unknown field, `omitempty`) are not reported.
- `Options.DeepMergeMaps` applies only when dst is `*map[string]any`: nested mappings merge key by key at any depth across defaults, files and overrides (project rules still apply); scalars, lists and nulls replace. Off by default, so the whole-entry rule above and `contracts/config-v1/load.json` (struct dst) are untouched. A struct dst ignores it. Path keys, origin and deep merge are Go-only; the Rust runner has no counterpart and no vector pins them.
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
