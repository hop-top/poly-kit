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
| `(*Error).Retaining(err)`      | Copy of the envelope that also retains `err`    |

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

## Column ordering

This package is the reference implementation of the cross-runtime column
contract. Go needs no `ColumnSpec` type: a row is a struct, and the
`table:""` tags in declaration order *are* the spec. The four payload
SDKs (TS, Python, Rust, PHP) carry a `ColumnSpec` list because a map has
no declaration order to read.

Five rules bind every runtime.

**1. Default order.** Column order and header names come from the
`table:""` tags in struct field declaration order. `TableHeaders(t)`
returns exactly that list.

```go
type task struct {
    Name   string `json:"name"   yaml:"name"   table:"name,priority=9"`
    Count  int    `json:"count"  yaml:"count"  table:"count,priority=7"`
    Status string `json:"status" yaml:"status" table:"status,priority=5"`
}

output.Render(w, output.Table, []task{{"alpha", 3, "ready"}, {"beta", 8, "held"}})
```

```
name   count  status
alpha  3      ready
beta   8      held
```

Fields with no `table:""` tag, or `table:"-"`, never appear.

**2. `--cols` reorders as well as selects.** The user's sequence wins
over declaration order: `--cols status,name` renders `status` then
`name`, in every format. `filterColumns` walks the requested names and
emits in that order, and `json`/`yaml` project through an ordered
carrier (`orderedmap.go`) rather than a plain map, so key order survives
serialization too.

**3. `header == key`.** One name labels the column, matches `--cols`,
and locates the value. Go cannot express a split: a `table:""` tag
supplies the header while the value comes from the struct field it sits
on. That inexpressibility is the *reason* the rule is universal — no SDK
may carry a capability the reference runtime cannot mirror, so the
payload SDKs reject a `ColumnSpec` whose `key` differs from its
`header`.

**4. Zero rows emits nothing** — not even a bare header row. Emptiness
is decided by row count, never column count.

```go
output.Render(w, output.Table, []task{})  // writes nothing at all
```

**5. `priority` hides columns on overflow; it never reorders them.**
`table:"name,priority=9"` marks a column as worth keeping when the
terminal is too narrow. `selectVisibleColumns` (renderer.go:290) drops
the lowest-priority column repeatedly until the row fits, breaking ties
rightmost-first (`lowestPriorityIndex`, renderer.go:314); the tag itself
is parsed by `parseTableTag` (renderer.go:262), which defaults missing
values and clamps to `[0,9]`. Surviving columns keep their relative
order. This is a Go-only feature today.

### Go vs the payload SDKs

| Capability | Go (reference) | py / ts | rs / php |
|---|---|---|---|
| Column order source | `table:""` tags, declaration order | `ColumnSpec` list order | `ColumnSpec` list order |
| `priority` hide-on-overflow | implemented (renderer.go:262, :290, :314) | accepted, stored, ignored | accepted, stored, ignored |
| `header != key` | inexpressible via `table:""` | rejected at construction | rejected at construction |
| json/yaml key order | follows the resolved order (via `orderedmap.go`) | follows the resolved column order | follows the resolved column order |
| `--cols` reorders | yes, all five formats | yes | yes |
| Built-in formats | `table`, `json`, `yaml`, `csv`, `text` (+ `human`) | same five | `table`, `json`, `yaml` only |
| Ordered columns on the template path | `.Cols` (dispatch.go:299-306) | `cols` | `{*}` (php); none (rs) |

### Conformance status

All five rules hold in Go across all five formats. Two gaps remain open
in the payload SDKs, and matter when writing portable code:

- **`csv` and `text` do not exist in rs or php.** Only `table`, `json`
  and `yaml` are available in all five runtimes today. The fixtures
  record this as `rs-php-no-csv-text`.
- **rs has no ordered-column affordance on the `--template` path.**
  Go exposes `.Cols` and py and ts expose `cols`; php has a `{*}`
  placeholder yielding pre-joined values. The spelling for rs is an open
  decision.

The cross-runtime fixtures at `sdk/tests/cross-lang/` execute this
contract against all five runtimes. They compare the **column order
re-parsed from each runtime's own output**, never raw bytes: table
padding and YAML block style differ legitimately between runtimes, so
byte comparison was never viable. Byte-level formatting parity is
therefore pinned by each SDK's own unit tests, not by the cross-language
suite. `csv` output agrees byte-for-byte across go/py/ts in the default
LF mode; the `crlf` option exposes known quoting divergences.

## Errors

`Error` is the structured envelope rendered by `RenderError`. Every
standard code has a constructor that pins its exit code and transience
class, so adopters never hand-roll the numbers:

| Constructor | Code | Exit | Transience |
|---|---|---|---|
| `GenericError(msg)` | `GENERIC` | 1 (`ExitGeneric`) | permanent |
| `UsageError(msg)` | `USAGE` | 2 | permanent |
| `NotFoundError(msg)` | `NOT_FOUND` | 3 | permanent |
| `ConflictError(msg)` | `CONFLICT` | 4 | permanent |
| `UnauthorizedError(msg)` | `UNAUTHORIZED` | 5 | permanent |
| `TransientError(msg)` | `TRANSIENT` | 6 (`ExitTransient`) | transient |
| `RateLimitedError(msg)` | `RATE_LIMITED` | 64 (`ExitRateLimited`) | transient |
| `ProvenanceMissingError(detail)` | `PROVENANCE_MISSING` | 65 (`ExitProvenanceMissing`) | permanent |

Build it with a struct literal when there is no underlying error:

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

Adopter errors implementing `AsCLIError() *output.Error` are rendered as
the envelope they return, and the middleware reattaches the originating
error via `Retaining` so `errors.Is` survives that path too. `Retaining`
copies rather than mutates, so returning a shared package-level envelope
from `AsCLIError` stays safe.
