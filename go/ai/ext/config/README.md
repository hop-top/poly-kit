# config

## What it answers

Whether a named extension is enabled and what settings it was given, loaded
from YAML. Wrong package for discovering or registering extensions
(`hop.top/kit/go/ai/ext/discover`, `hop.top/kit/go/ai/ext/registry`).

## Use it when

- you load adopter config: `store := config.NewStore(); store.LoadFile(path)` or `store.Load(r)`
- you gate an extension: `store.IsEnabled(name)`
- you read its settings: `store.Settings(name)` (a copy; nil when none)
- you flip at runtime: `store.SetEnabled(name, false)`

## Quick start

```go
yaml := `
extensions:
  audit:
    enabled: false
  stats:
    enabled: true
    settings:
      interval: 30
`
store := config.NewStore()
if err := store.Load(strings.NewReader(yaml)); err != nil {
    panic(err)
}
fmt.Println("audit:", store.IsEnabled("audit"))
fmt.Println("stats:", store.IsEnabled("stats"), store.Settings("stats")["interval"])
fmt.Println("unknown:", store.IsEnabled("unknown"))
// Output:
// audit: false
// stats: true 30
// unknown: true
```

Verified by `example_test.go` in this directory.

## Contract

- YAML shape: top-level `extensions:` map of name to `{enabled: bool, settings: map}`.
- Opt-out model: unknown extensions are enabled.
- `Load` merges into the store; `All` returns a deep snapshot. The store is safe for concurrent use.

## Neighbours

- `hop.top/kit/go/ai/ext`: `Manager` routes `CapConfig` extensions to this mechanism.
- `hop.top/kit/go/core/config`: application-wide config loading; this store only covers the `extensions` block.

## See also

- [go/ai/ext](../README.md)
