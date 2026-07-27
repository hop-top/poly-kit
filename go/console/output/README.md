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
| `Error`                        | Structured error envelope (code, message, exit code) |
| `WrapError(err, code, exit)`   | Build an envelope that retains `err` for `errors.Is` |

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

## Errors

`Error` is the structured envelope rendered by `RenderError`. Build it
with a struct literal when there is no underlying error:

```go
return &output.Error{
    Code:     output.CodeNotFound,
    Message:  "workspace not found",
    ExitCode: 3,
}
```

Use `WrapError` when converting an existing error, so callers can still
classify it by sentinel:

```go
return output.WrapError(err, output.CodeGeneric, 1)
```

The retained error is unexported and never reaches the wire — JSON and
YAML output is byte-identical either way. `Cause` remains the
human-readable serialized form; the retained error is the
machine-matchable one.

`kit/console/cli` wraps handler failures this way, so sentinels survive
the RunE middleware boundary:

```go
if err := root.Execute(); err != nil {
    if errors.Is(err, myPkg.ErrNoConfig) { // matches through the envelope
        // ...
    }
}
```

`errors.As` still reaches `*output.Error` itself, so renderers can pull
the exit code and suggested fix off it.

One boundary: an adopter error implementing `AsCLIError() *output.Error`
takes a passthrough branch and is rendered as the envelope it returns.
That envelope retains no underlying error, so sentinel matching does not
survive it — the adopter owns the envelope there. Build it with
`WrapError` if you want `errors.Is` to keep working.
