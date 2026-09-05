# registry

## What it answers

In-process registration for extensions compiled into the binary that declare
`ext.CapRegistry`. Wrong package for external plugin binaries
(`hop.top/kit/go/ai/ext/discover`) or lifecycle hooks (`hop.top/kit/go/ai/ext/hook`).

## Use it when

- an extension self-registers at `init()`: `registry.Register(e)` on the package-level default
- you look one up: `registry.Get(name)`, `registry.MustGet(name)`, `registry.List()`
- you need an isolated registry (tests, embedders): `registry.New()` with the same methods

## Quick start

```go
r := registry.New()
r.Register(audit{})
for _, e := range r.List() {
    fmt.Println(e.Meta().Name, e.Meta().Version, e.Capabilities())
}
// Output:
// audit 0.1.0 registry
```

`audit` is any `ext.Extension` whose `Capabilities()` includes `ext.CapRegistry`.
Verified by `example_test.go` in this directory.

## Contract

- `Register` panics when the extension lacks `CapRegistry` or when the name is already taken; registration is a build-time invariant, not a runtime error.
- `List` preserves registration order.

## Neighbours

- `hop.top/kit/go/ai/ext`: `Extension`, `Capability`, and `Manager`, which routes `CapRegistry` extensions here.
- `hop.top/kit/go/ai/ext/config`: enable or disable a registered extension by name.

## See also

- [go/ai/ext](../README.md)
