# Generate avatars from a seed

> Audience: Go adopters who need a stable placeholder avatar for a
> user, project or peer without storing an image. Package contract:
> [`go/core/avatar` README](../../../go/core/avatar/README.md).

`avatar.Generate` turns a seed string into an avatar URL (or data
URI), through a provider registry. One provider ships, dicebear; you
can register your own. Generation does no I/O: you get a string back
and decide whether to render or fetch it.

## Prerequisites

- Go module that imports `hop.top/kit/go/core/avatar`.
- A stable seed per entity. The same seed with the same options gives
  the same URL.

## 1. Get a URL

```go
u, err := avatar.Generate(ctx, avatar.Options{
    Seed: "noor",
    Size: 256,
})
// https://api.dicebear.com/9.x/shapes/svg?seed=noor&size=256
```

`Seed` is required; an empty one returns `avatar: Seed is required`.
Every other `Options` field is optional and provider-defaulted:

| Field | Meaning |
|-------|---------|
| `Provider` | Registered provider name; empty uses the default (`dicebear`) |
| `Style` | Provider-specific look (dicebear: `shapes`, `bottts`, ...) |
| `Size` | Pixels; zero lets the provider decide, and the dicebear URL omits `size` |
| `Format` | `svg`, `png`, ...; dicebear default `svg` |
| `Extra` | Provider-specific query knobs |

## 2. Choose a dicebear look

```go
u, _ := avatar.Generate(ctx, avatar.Options{
    Seed:   "noor",
    Style:  "bottts",
    Format: "png",
    Size:   64,
    Extra:  map[string]string{"backgroundColor": "b6e3f4"},
})
// https://api.dicebear.com/9.x/bottts/png?backgroundColor=b6e3f4&seed=noor&size=64
```

URL shape: `https://api.dicebear.com/9.x/<style>/<format>?<query>`. The
defaults are style `shapes` (abstract, nothing figurative) and format
`svg`; the constants are `avatar.DicebearDefaultStyle`,
`DicebearDefaultFormat`, `DicebearAPIVersion` and `DicebearBaseURL`.
Style and format are path-escaped, the seed is query-escaped.

Styles are not validated. An unknown style yields a URL the endpoint
may reject. For strict checking, test against the published list:

```go
ok := slices.Contains(avatar.DicebearStyles, style)
```

`Extra` is applied last, so a key named `seed` or `size` overrides
the typed field.

## 3. Register your own provider

Only dicebear is built in. Names such as gravatar or libravatar appear
in the package docs as examples of what you can add; asking for an
unregistered name fails with `avatar: provider "gravatar" not
registered`.

```go
type localProvider struct{}

func (localProvider) Name() string     { return "local" }
func (localProvider) Styles() []string { return nil }
func (localProvider) Generate(_ context.Context, o avatar.Options) (string, error) {
    return "/avatars/" + o.Seed + ".svg", nil
}

func init() { avatar.RegisterProvider(localProvider{}) }
```

- `Generate` must be safe for concurrent use. The `ctx` is reserved
  for providers that call out; built-ins ignore it.
- Registering a name that exists replaces that provider. Register at
  program start.
- Return a URL or a data URI, whichever the provider produces.

Select it per call with `Options.Provider: "local"`, or make it the
fallback:

```go
err := avatar.SetDefaultProvider("local") // error if not registered
```

Introspection: `avatar.Providers()` (sorted names),
`avatar.LookupProvider(name)`, `avatar.DefaultProvider()`,
`Provider.Styles()`.

## 4. Fetching and privacy

- The returned dicebear URL points at a third-party host. Fetching it
  is an ordinary outbound request and is subject to `--offline` and
  network policy like any other (see
  [`go/core/netpolicy`](../../../go/core/netpolicy/README.md)).
- The seed travels in the URL. Do not seed with an email address or
  other personal data when the avatar will be fetched from a remote
  host; seed with an opaque ID instead, such as a TypeID from
  [use-typeid-entity-ids](use-typeid-entity-ids.md).
- To keep requests off the network, register a provider that renders
  locally (as above) and make it the default.

## See also

- [`go/core/avatar` README](../../../go/core/avatar/README.md)
- [use-typeid-entity-ids](use-typeid-entity-ids.md)
- [go-primitives](../reference/go-primitives.md)
