# avatar

## What it answers

Give me a stable avatar URL (or data URI) for this seed, from whichever
provider is registered. Keys and signing identity are `go/core/identity`;
entity identifiers are `go/core/id`.

## Use it when

- you need an avatar for a user, project or peer: `Generate(ctx, Options{Seed: ...})`
- you want a specific provider: set `Options.Provider`, or change the fallback with `SetDefaultProvider`
- you ship your own provider (gravatar, libravatar, ...): implement `Provider`, call `RegisterProvider` at program start
- you need to list what is available: `Providers()`, `Provider.Styles()`, `DicebearStyles`

## Quick start

```go
u, err := avatar.Generate(context.Background(), avatar.Options{
    Seed: "noor",
    Size: 256,
})
if err != nil {
    fmt.Println("err:", err)
    return
}
fmt.Println(u)
// Output: https://api.dicebear.com/9.x/shapes/svg?seed=noor&size=256
```

## Contract

- `Seed` is required; every other field is optional and provider-defaulted.
- Built-in providers do no I/O: `Generate` returns a URL, never fetches it. Fetching is the caller's job and falls under `--offline` like any other request.
- Dicebear defaults: style `shapes`, format `svg`, API `9.x`, host `https://api.dicebear.com`. URL shape: `<host>/<api>/<style>/<format>?seed=...&size=...`, plus `Options.Extra` as extra query keys.
- Styles are not validated; an unknown style yields a URL the endpoint may reject. Check against `DicebearStyles` yourself if you need strictness.
- `RegisterProvider` replaces a provider registered under the same name. `SetDefaultProvider` errors on an unknown name.

## Neighbours

- `go/core/identity`: local-first cryptographic identity.
- `go/core/id`: TypeID entity identifiers, a natural seed.
- `go/core/netpolicy`: governs the fetch if you download the image.

## See also

- [go-primitives.md](../../../docs/adopters/reference/go-primitives.md)
