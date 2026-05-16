# hop-top-kit (Python SDK)

Python implementation of the hop-top kit library.

## URI facade

`hop_top_kit.uri` exposes Kit's URI integration surface as a thin adapter over
the `hop-top-uri` package. The SDK does not duplicate URI parsing or handler
generation logic; it delegates to the URI package for contract-backed behavior.

```python
from hop_top_kit import uri

policy = uri.default_policy()
parsed = uri.parse("tlc://org/repo/T-0001?cmd=task&verb=claim", policy)
plan = uri.resolve(parsed, policy)  # command plan only; never executes
```

Supported helpers:

| Helper | Purpose |
|--------|---------|
| `parse(input, policy=None, options=None)` | Parse a URI with `hop-top-uri`. |
| `resolve(parsed_uri, policy)` / `resolve_action(...)` | Resolve an action to a command plan without executing it. |
| `complete(registry, input=...)` | Return vanity completions from a URI registry. |
| `complete(registry, type_name=..., prefix=...)` | Return typed completions from a URI registry. |
| `complete(registry, type_name=..., to_complete=...)` | Delegate to scheme-aware completion. |
| `handler_id(spec)` | Return the handler ID for a `HandlerSpec`. |
| `handler_snippet(platform, spec)` | Render Linux/macOS/Windows handler snippets. |

URI types such as `Policy`, `ParseOptions`, `Registry`, `HandlerSpec`, and
`VanityAlias` are exposed lazily from `hop-top-uri` so callers can use the
backend's canonical model directly.

## Output formatting

`hop_top_kit.output` brings the same formatter surface to Python that
`hop.top/kit/go/console/output` ships for Go: an extensible
`Formatter` Protocol, a `Registry`, built-in formatters
(`json`, `yaml`, `table`, `csv`, `text`), and the matching Typer flag
suite — `--format`, `--format-opt`, `--format-help`, `--cols` /
`--columns`, `--template`, `--output` / `-o`.

### Quickstart

```python
import typer
from hop_top_kit.output.cli import register_output_flags
from hop_top_kit.output.dispatch import dispatch
from hop_top_kit.output.formatter import ColumnSpec

app = typer.Typer()
register_output_flags(app)        # wire the full flag suite

@app.command("list")
def list_items(ctx: typer.Context) -> None:
    rows = [
        {"name": "alpha", "count": 1, "status": "ok"},
        {"name": "beta",  "count": 2, "status": "warn"},
    ]
    cols = [
        ColumnSpec(header="name",   key="name",   priority=9),
        ColumnSpec(header="count",  key="count",  priority=7),
        ColumnSpec(header="status", key="status", priority=5),
    ]
    dispatch(ctx, rows, columns=cols)
```

```bash
$ mycli list
name   count  status
alpha  1      ok
beta   2      warn

$ mycli list --format json
[
  {"name": "alpha", "count": 1, "status": "ok"},
  ...
]

$ mycli list --format csv --format-opt delimiter=';'
name;count;status
alpha;1;ok
beta;2;warn

$ mycli list --cols name,status
name   status
alpha  ok
beta   warn

$ mycli list -o /tmp/out.json    # extension infers json

$ mycli list --format-help        # catalog of registered formats
```

### Built-in formatters

| Key     | Extensions     | Options                                                            |
|---------|----------------|--------------------------------------------------------------------|
| `json`  | `.json`        | `indent` (int, default 2; `0` -> compact)                          |
| `yaml`  | `.yaml`,`.yml` | `default-flow-style` (bool, default false)                         |
| `table` | (none)         | none                                                               |
| `csv`   | `.csv`         | `delimiter`, `no-header`, `quote-all`, `crlf`                      |
| `text`  | `.txt`         | `style` (`kv` / `lines` / `paragraph`), `separator` (kv only)      |

Discover at runtime:

```bash
mycli list --format-help                # list all
mycli list --format csv --format-help   # csv-only options
```

### `--output|-o` and extension inference

| `--output` | `--format` | Result                                      |
|------------|------------|---------------------------------------------|
| (omitted)  | (omitted)  | stdout, default format `table`              |
| `-`        | any        | stdout (sentinel)                           |
| `path.csv` | (omitted)  | writes CSV to file (ext-inferred)           |
| `path.csv` | `csv`      | writes CSV to file                          |
| `path.csv` | `json`     | error: format/extension mismatch            |

Files are opened with `O_WRONLY|O_CREATE|O_TRUNC` (overwrites).

Suppress `-o` for commands that must write to stdout only:

```python
register_output_flags(app, disable={"output": True})
```

### `--template` (Jinja2)

Mutually exclusive with `--cols`. Auto-escape is **off** so output is
raw text (not HTML).

Template context:

| Variable | Type                  | Description                          |
|----------|-----------------------|--------------------------------------|
| `items`  | `list[dict[str, Any]]`| Each row coerced to a dict           |
| `cols`   | `list[str]`           | Column headers (from `ColumnSpec`)   |
| `data`   | original payload      | The raw `data` arg passed to dispatch|

Syntax delta from Go's `text/template`:
`{{range}}` -> `{% for x in items %}` / `{% endfor %}`. See
[Jinja2 syntax docs](https://jinja.palletsprojects.com/templates/).

```bash
mycli list --template '{% for it in items %}{{ it.name }}={{ it.count }}\n{% endfor %}'
```

### Custom formatters

Implement the `Formatter` Protocol (no inheritance required) and
register against the default registry, or `override` a built-in:

```python
from hop_top_kit.output import default_registry
from hop_top_kit.output.formatter import OptionSpec

class MarkdownFormatter:
    key = "md"
    extensions = (".md",)

    def options(self):
        return [
            OptionSpec(name="header-level", type="int", default=2,
                       usage="leading '#' count for record headers"),
        ]

    def render(self, out, data, opts, cols):
        prefix = "#" * opts["header-level"]
        for it in data:
            out.write(f"{prefix} {it['name']}\n")

default_registry.register(MarkdownFormatter())
```

Use `default_registry.override(MyJSONFormatter())` to intentionally
replace a built-in.

### Backward compatibility

The legacy `render(w, format, v)` entry point still works:

```python
from hop_top_kit.output import render

render(sys.stdout, "json", {"a": 1})
```

It now delegates to `default_registry.lookup(format).render(...)`
internally. The `Format` Literal extends to include the new built-ins
(`csv`, `text`) - non-breaking for adopters that typed against the
narrower set.
