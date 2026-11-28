# Config, storage and secrets

Open when: loading configuration, choosing where state lives, storing
values or secrets, or resolving XDG directories.

## Layered config with a project marker

Package `hop.top/kit/go/core/config`; paths come from `go/core/xdg`.

| Need | Use |
|------|-----|
| the standard cascade (project, user, system, defaults) | `config.OptionsForTool("mytool")`, then `config.Load(&cfg, opts)` in `PersistentPreRunE` |
| a project marker of your own (`.mytool/config.yaml`) | `config.Nearest(cwd, ".mytool/config.yaml")` for the file, set it as `Options.ProjectConfigPath`; `config.PathsForToolWithMarkers(cwd, tool, markers)` lists the chain for display |
| `-c file` and `-c key=value` | pass `root.ConfigArgs()` as `ExtraConfigPaths` and `Overrides`; never declare the flag |
| a committed project file must not set secrets or widen policy | `Options.Project: &config.ProjectRules{...}` |
| keys holding paths | `Options.PathKeys`: relative values resolve against the supplying file |
| where a value came from | `config.LoadWithOrigins` |
| write a value back | `config.SetValue` (keeps the Go type); `config.Set` writes strings |
| reload on a signal | `config.New` with `WithReloadPublisher`; fields tagged `reload:"true"` only |

Layers merge per struct field; scalars, lists and map entries replace
whole. The project layer is the one that can be restricted; system, user,
`-c` and env are not. The project-marker walk-up stops at the home
directory.

## Storage

| Need | Use |
|------|-----|
| a local cache or session table | `go/storage/kv` with the `sqlite` driver (one file, readable from other languages) |
| in-process cache | `go/storage/kv/memory` |
| shared state across hosts | `go/storage/kv/etcd` or `go/storage/kv/tidb` |
| opaque bytes | `go/storage/blob` |
| SQLite connections with WAL and busy timeout | `go/storage/sqldb` |
| JSON documents with backup and at-rest encryption | `go/storage/sqlstore` |
| cache HTTP responses | `go/storage/httpcache` |
| the shared project registry | `go/core/projects` |

## Secrets

`go/storage/secret` is the interface (store, mutable store, keeper) with
backends under it: `env` for CI, `keyring` for laptops, `agefile`,
`file`, `onepassword`, `openbao`, `infisical`, `ghsecrets` (write-only),
`memory` for tests. `secret/ref` resolves `op://` and `secret:NAME`
references inside config values. In tests build secret-looking values at
runtime; never commit a literal key.

## Pages

- [config](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/config.md), [XDG](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/xdg.md),
  [inspect config paths](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/inspect-config-paths.md)
- [storage abstractions](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/concepts/storage-abstractions.md),
  [blob](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/store-files-with-blob.md),
  [SQLite JSON store](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/use-the-sqlite-json-store.md),
  [HTTP cache](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/cache-http-responses.md),
  [project registry](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/use-the-project-registry.md)
- [secret management](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/secret-management-guide.md)
