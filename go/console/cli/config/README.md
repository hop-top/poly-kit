# config

## What it answers

"Which config file does this tool actually load, and what is the full
precedence chain?" This package ships the `config path` and `config paths`
cobra subcommands only. The chain itself is computed by
`hop.top/kit/go/core/config` (`Paths`, `PathsForTool`); loading and merging
config values is also not here.

## Use it when

- host already owns a `config` parent → `config.RegisterPathSubcommands(parent, "<tool>", opts...)`
- host has no `config` parent yet → `root.AddCommand(config.Command("<tool>", opts...))`
- attach one subcommand at a time → `config.PathCommand(...)` / `config.PathsCommand(...)`
- wire the real chain → `config.WithResolver(func(cwd string) []config.ResolvedPath { ... })`
- root error bridge must map "no config file" to exit 1 → `config.IsNoConfig(err)`

## Quick start

```go
resolver := func(cwd string) []config.ResolvedPath {
    return []config.ResolvedPath{
        {Path: filepath.Join(cwd, ".demo.yaml"), Source: "cwd", Exists: false},
        {Path: "/home/me/.config/demo/config.yaml", Source: "user", Exists: true},
        {Path: "<defaults>", Source: "default", Exists: true},
    }
}

cfg := &cobra.Command{Use: "config", Short: "Inspect demo configuration"}
config.RegisterPathSubcommands(cfg, "demo", config.WithResolver(resolver))
cfg.SetOut(os.Stdout)

cfg.SetArgs([]string{"path", "--from", "/work"})
_ = cfg.Execute()
cfg.SetArgs([]string{"paths", "--from", "/work"})
_ = cfg.Execute()
```

`path` prints `/home/me/.config/demo/config.yaml`; `paths` prints all three
paths, highest precedence first. Production hosts adapt
`core/config.Paths` instead of a literal slice.

## Contract

- Resolver returns the chain highest-precedence first, never nil entries.
  Default resolver returns nil: `path` exits 1 with
  `no config file found in resolution chain` on stderr, `paths` prints
  nothing.
- `path` prints the first entry with `Exists == true`. `paths` prints every
  entry, existing or not.
- `--format text|json|yaml`, default `text`. Text prints bare paths, one per
  line. JSON and YAML emit the full `ResolvedPath` shape
  (`path`, `source`, `scope`, `exists`); an empty chain encodes as `[]`, not
  `null`. Unknown format is an error.
- `--from <dir>` replaces `os.Getwd()` as the resolution start.
- `Command` registers a persistent `--format` flag. `RegisterPathSubcommands`
  does not; the host parent must already provide one or the subcommands fall
  back to text.
- `ResolvedPath` is field-for-field and tag-for-tag compatible with
  `core/config.ResolvedPath`, so a direct conversion works.
- Both leaves are annotated read-only and idempotent.

## Neighbours

- `hop.top/kit/go/core/config`: precedence chain computation and loading.
- `hop.top/kit/go/console/cli`: root command, side-effect and idempotency
  annotations.
- `hop.top/kit/go/console/cli/completion`: `ConfigKeys` completer for viper
  keys.

## See also

- [Inspect config paths](../../../../docs/adopters/guides/inspect-config-paths.md)
- [CLI API reference](../../../../docs/adopters/reference/cli-api-reference.md)
- [Go primitives reference](../../../../docs/adopters/reference/go-primitives.md)
