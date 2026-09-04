# uri

Kit-shipped custom URI commands for kit-built CLIs. Mounts a `uri`
command tree that parses scheme values, resolves action routes, emits
completion candidates, and generates OS handler metadata.

## Relationship to cite

This package delegates to `hop.top/cite` (`v0.1.0`); it does not
reimplement the URI contract. Go is the reference consumer and uses the
widest surface of the four runtimes:

| Import | Used by |
|---|---|
| `hop.top/cite/scheme` | `parse.go`, `policy.go`, `complete.go`, `types.go` |
| `hop.top/cite/handle/generate` | `handler.go`, `types.go` |
| `hop.top/cite/handle` | `console/output/linkify.go` (OSC 8 hyperlinks) |

The payload SDKs (TS, Python, Rust, PHP) each expose a thinner facade
over their own `cite` binding; see their READMEs for the version each
one pins.

## Public API

| Symbol             | Purpose                                        |
|--------------------|------------------------------------------------|
| `Command(cfg)`     | Build the top-level URI command                |
| `Register(p, cfg)` | Attach the command tree to a parent command    |
| `Config`           | Command name, policy, types, handler defaults  |
| `HandlerConfig`    | Handler artifact defaults (vendor, app, …)     |

## Sub-commands

`parse`, `resolve`, `complete`, `handler id`, `handler generate`,
`completion`. Each leaf is suppressible via `Config.DisabledCommands`
using those keys.

## Adopter quickstart

```go
uri.Register(root, uri.Config{
    Policy: scheme.Policy{
        SchemeNamespaceSegments: map[string]int{"tlc": 2},
    },
    Handler: uri.HandlerConfig{
        Vendor: "example", App: "spaced", Scheme: "tlc",
    },
})
```

Rename the mount point or drop leaves:

```go
uri.Register(root, uri.Config{
    CommandName:      "link",
    DisabledCommands: []string{"handler.generate"},
})
```

`examples/spaced/go/main.go` is the worked reference wiring.

<!-- release: track hop.top/cite v0.1.0 -->
