# svcconfig

## What it answers

"Which value does this service's middleware key resolve to, and is the
services block valid?" It holds the middleware block registry and the one
resolver every block reads through: `services.<svc>.<block>.<key>`, then
`services.all.<block>.<key>`, then the caller's code option and default.
Loading files, the environment and `-c` into the Viper is not here; the
console root does that before `serve` runs (`go/console/cli`).

## Use it when

- a service reads a middleware key → `svcconfig.New(v).Lookup(svc, block, key)`
- a service validates its block → `svcconfig.New(v).ValidateBlock(block, svc, svcconfig.Shared)`
- the supervisor gates the whole `services` tree → `svcconfig.New(v).Validate()`
- a new middleware block ships → add a `Block` row with its keys to the registry;
  a key holding a list of entries (`audit.sinks`) also names its entry keys in `Lists`
- a block only some services apply → name them in `Services` (`Validate` refuses it
  elsewhere); a key or value only an HTTP listener applies → `HTTPKeys`, `HTTPValues`
  (`ValidateNoHTTP` refuses them under a service with none)
- map a `<TOOL>_SERVICES_*` variable to its key → `svcconfig.EnvKey(name, tool, services)`

## Quick start

```go
v := viper.New()
v.Set("services.all.body_limit.max_bytes", 2048)
v.Set("services.api.body_limit.enabled", true)

r := svcconfig.New(v)
val, from, _ := r.Lookup("api", "body_limit", "max_bytes")
fmt.Println(val, from) // 2048 services.all.body_limit.max_bytes

v.Set("services.all.addr", "0.0.0.0:8080")
fmt.Println(r.Validate()) // services.all.addr: not a middleware key; ...
```

## Contract

- Per key, specificity before source: the service's key from any viper
  source beats the `services.all` key from a higher one. A list is one
  value: the more specific list replaces, never merges.
- Keys are enumerated from every viper layer, so a block split across a
  file, the environment and `-c` is validated whole.
- An unknown key inside a registered block, and anything outside a
  registered block under `services.all`, are errors; `serve` reports them
  at exit 2. So is a list-of-entries key of the wrong shape: an entry that
  is neither a type nor a map, or a map with a key the entry does not take.
- Environment names split longest match first: service, then block, then
  key. A remainder that names no block is a service's own flat key.
- Rules: [serve lifecycle, Middleware configuration](../../../../docs/contracts/serve-lifecycle.md#middleware-configuration).

## Neighbours

- `go/console/cli`: layers the sources into the root's Viper and reads the
  api and socket blocks.
- `go/transport/observability`: reads the `tracing` and `metrics` blocks.
- `go/core/config`: file paths, `-c` parsing, typed config loading.
