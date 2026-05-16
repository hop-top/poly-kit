# output

Pluggable structured output for kit CLIs. Built-in formatters:
`table`, `json`, `yaml`, `csv`, `text`.

## Public API

| Symbol                         | Purpose                                         |
|--------------------------------|-------------------------------------------------|
| `Formatter` (interface)        | Encode `data` to an `io.Writer`                 |
| `OptionSpec`                   | Describes one `--format-opt` key                |
| `Options` / `ParseOptions`     | Validated option map + parser                   |
| `Registry` / `NewRegistry`     | Formatter container                             |
| `Default`                      | Package-level `Registry`; built-ins register here |
| `RegistryOption`               | Functional option for `RegisterFlagsWith`       |
| `WithRegistry(r)`              | Bind flags to a custom `Registry`               |
| `DisableOutputFlag()`          | Suppress `--output` / `-o`                      |
| `RegisterFlags(cmd, v)`        | Add the standard output flag set                |
| `RegisterFlagsWith(cmd, v, …)` | Same, with options                              |
| `Dispatch(cmd, v, data)`       | Resolve flags and render                        |
| `Render(w, format, data)`      | Backward-compat shim (no flag awareness)        |
| `TableHeaders(t)`              | Reflect-based header list for a struct type     |
| `WithTableStyle(s)`            | Render-call opt-in to lipgloss-backed table (TTY only) |
| `TableStyle`                   | Theme-data envelope used by `WithTableStyle`    |
| `RowEmphasis(i, kind)`         | Mark row `i` for primary/secondary/muted color  |
| `EmphasisKind`                 | Enum: `EmphasisNone/Primary/Secondary/Muted`    |

Format keys are also exposed as constants: `Table`, `JSON`, `YAML`,
`CSV`, `Text`.

## Adopter quickstart

Plain wiring (uses `Default`, all built-ins registered):

```
output.RegisterFlags(cmd, v)
// ...
return output.Dispatch(cmd, v, data)
```

Replace a built-in:

```
output.Default.Override(myJSONFormatter{})
```

Isolated registry (e.g. multi-CLI binary):

```
r := output.NewRegistry()
r.Register(output.JSONFormatter{}) // pseudo: register what you need
r.Register(myFancyFormatter{})
output.RegisterFlagsWith(cmd, v, output.WithRegistry(r))
return output.Dispatch(cmd, v, data)
```

Stream-only command (no `-o`):

```
output.RegisterFlagsWith(cmd, v, output.DisableOutputFlag())
```

Themed tables (lipgloss-backed; TTY-only — non-TTY writers stay
on the plain tabwriter renderer):

```
output.Render(w, output.Table, rows,
    output.WithTableStyle(root.TableStyle()),
    output.RowEmphasis(0, output.EmphasisPrimary),
)
```

`TableStyle` takes `color.Color` values directly so the package
stays a leaf. Adopters using `kit/console/cli` should call
`Root.TableStyle()` to lift colors from the active theme.
