# hop-top-kit (Python SDK)

Python implementation of the hop-top kit library.

## Modules

- [`hop_top_kit.id`](hop_top_kit/id/) — TypeID primitive (cross-language;
  see [ADR 0001](../../docs/adr/0001-typeid-primitive.md))
- [`hop_top_kit.mcp`](hop_top_kit/mcp/) — dual-spec MCP surface
  (extra `mcp`; see [MCP surface](#mcp-surface))

## MCP surface

Serves the Model Context Protocol over a bridged command tree, one MCP
tool per runnable leaf. A single mount answers **both** revisions —
`2024-11-05` (handshake) and `2026-07-28` (stateless per-request
envelope) — choosing per request, because the newer revision has no
handshake to negotiate with.

Wire behaviour is pinned by the shared cross-language fixtures in
`sdk/tests/cross-lang/fixtures/mcp-wire.json`, compared as raw bytes.

### Install

The MCP dependencies are optional, so adopters who do not serve MCP do
not carry them:

```bash
pip install 'hop-top-kit[mcp]'
```

The extra pulls `mcp>=2.0,<3` and `mcp-types>=2.0,<3`. The 2.0 line is
the first carrying the `2026-07-28` era; 1.x tops out at `2025-11-25`
and cannot serve the modern revision.

`mcp-types` already models the era split this surface needs —
`HANDSHAKE_PROTOCOL_VERSIONS` versus `MODERN_PROTOCOL_VERSIONS` — so the
protocol constants come from the SDK rather than a parallel vocabulary.

### Hosting: ASGI

```python
from hop_top_kit.mcp import Bridge, Command, Result, mount_mcp

root = Command(name="app", children=[
    Command(
        name="ping",
        short="Ping the server",
        run=lambda flags: Result(stdout="pong\n"),
        annotations={"kit/side-effect": "read"},
    ),
])

app = mount_mcp(Bridge(root))   # an ASGI callable
```

`mount_mcp` returns an `McpSurface`, which *is* the ASGI application —
mount it under uvicorn, hypercorn, or a Starlette route directly. ASGI
rather than WSGI is deliberate: the modern era's streaming affordances
are not expressible in WSGI, and the official SDK's modern transport is
async.

For a framework-free binding, `McpSurface.handle(request) -> Response`
is the whole contract — a pure function from a normalised request, no
socket involved, which is what lets the conformance suite drive every
fixture case.

### Options

Keyword-only on `mount_mcp`: `path` (`/mcp`), `server_name`
(`cmdsurface`), `server_version` (`0.0.0`), `spec_versions` (`None` =
both eras), `cache_ttl_ms` (`0`), `cache_scope` (`private`),
`origin_allowlist` (empty, no check), `confirmation_key`, and
`extensions`. The option *set* is normative across every kit SDK; only
the spelling is idiomatic.

An explicitly empty `spec_versions`, a negative ttl, an unrecognized
cache scope, or an empty confirmation key raise `MountError` rather than
starting a server that quietly serves nothing.

Policy is passed to the `Bridge`, not to `mount_mcp`:
`Bridge(root, policy=...)`.

### Safety

Exposure is gated by `Policy.allowed(cls, surface)`. The default is
deliberately closed: **no remote surface may invoke a destructive
leaf** — `default_policy()` leaves `allow_destructive_on` empty, and
empty means block-all. A blocked call returns an `isError` result at
HTTP 200, not a transport error: the call was understood and declined,
not malformed.

```python
from hop_top_kit.mcp import Policy, Surface

Bridge(root, policy=Policy(allow_destructive_on=(Surface.MCP,)))
```

Leaves are classified from `kit/*` annotations: `kit/side-effect`
(`destructive`, `destructive-local`, `destructive-shared`),
`kit/auth-required`, `kit/requires-confirmation`.

This gate is unrelated to the Factor 10 `safety.py` `--force` helper,
which is a CLI-time TTY check for delegation safety.

### Confirmation

A `kit/requires-confirmation` leaf requires an `X-Confirm-Token` header
by default. Supplying `confirmation_key` swaps in the
`ElicitationConfirmationGate`: the first call answers `resultType:
"input_required"` with an elicitation form and a signed `requestState`,
and the client retries with the user's decision. Clients that do not
advertise form elicitation keep the header gate — the spec forbids
sending input requests to a client that cannot answer them.

Multi-instance deployments must share one key, so a retry landing on any
instance can verify state minted by another.

### Lazy help flags change `tools/list` bytes

`Command.attach_help_flag()` models cobra's lazy `--help` registration,
so a leaf's `inputSchema` gains a `help` property after its first
`tools/call`. Two identical `tools/list` requests either side of an
invocation therefore return **different bytes**, legitimately. This is
pinned by the `sequences` section of the wire fixtures. Opt out per
command with `lazy_help_flag=False`.

### Scope

Deprecated upstream features (Roots, Sampling, Logging, HTTP+SSE) are
unimplemented, matching the Go reference. The `tasks/*` extension is
opt-in — `mount_mcp(bridge, extensions=(TasksExtension(),))`; left
unmounted it answers `-32601` with no `extensions` map advertised in
`server/discover`, which is the conformant way to not support it.

### Cross-references

- [Serve MCP from any SDK](../../docs/adopters/guides/serve-mcp-from-any-sdk.md)
  — the polyglot adopter guide
- [Expose your CLI over MCP](../../docs/adopters/guides/expose-cli-over-mcp.md)
  — the Go reference surface, in depth

## URI facade

`hop_top_kit.uri` exposes Kit's URI integration surface as a thin adapter over
the `hop-top-cite` package. The SDK does not duplicate URI parsing or handler
generation logic; it delegates to the cite package for contract-backed behavior.

```python
from hop_top_kit import uri

policy = uri.default_policy()
parsed = uri.parse("tlc://org/repo/T-0001?cmd=task&verb=claim", policy)
plan = uri.resolve(parsed, policy)  # command plan only; never executes
```

Supported helpers:

| Helper | Purpose |
|--------|---------|
| `parse(input, policy=None, options=None)` | Parse a URI with `hop-top-cite`. |
| `resolve(parsed_uri, policy)` / `resolve_action(...)` | Resolve an action to a command plan without executing it. |
| `complete(registry, input=...)` | Return vanity completions from a URI registry. |
| `complete(registry, type_name=..., prefix=...)` | Return typed completions from a URI registry. |
| `complete(registry, type_name=..., to_complete=...)` | Delegate to scheme-aware completion. |
| `handler_id(spec)` | Return the handler ID for a `HandlerSpec`. |
| `handler_snippet(platform, spec)` | Render Linux/macOS/Windows handler snippets. |

URI types such as `Policy`, `ParseOptions`, `Registry`, `HandlerSpec`, and
`VanityAlias` are exposed lazily from `hop-top-cite` so callers can use the
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

$ mycli list --cols status,name   # --cols reorders as well as selects
status  name
ok      alpha
warn    beta

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

### Column ordering

1. **Default order.** When `dispatch` receives a `columns=` list, that
   list's order and header names drive every formatter — table, csv, text,
   json, yaml and `--template` alike. The payload's own key order is the
   fallback used *only* when no `ColumnSpec` list is supplied.
2. **`--cols` reorders as well as selects.** The user's sequence wins over
   the `ColumnSpec` order: `--cols status,name` renders `status` then
   `name`. Repeated `--cols` flags accumulate and de-duplicate, and the
   surviving first-seen order is the render order. The same rule applies on
   the no-schema fallback path.
3. **`header == key`.** They are one name: validation and value lookup are
   the same operation, so a name accepted by `--cols` validation can never
   fail again mid-render. Constructing a `ColumnSpec` whose `key` differs
   from its `header` raises `ValueError` — Go cannot express the split via
   its `table:""` tags, so no SDK carries it.
4. **Zero rows emits nothing** — not even a bare header row. Emptiness is
   decided by row count, never header count, so a `ColumnSpec` list does not
   resurrect a header for an empty payload.
5. **`priority` is accepted, stored and ignored.** The hide-on-overflow
   behavior it drives is implemented in Go only; the payload SDKs keep the
   field so specs stay portable until that feature is ported.

A `ColumnSpec` naming a column the payload does not carry is a hole, not an
error: it renders as an empty cell.

Worked example — the same rows under each rule:

```python
rows = [
    {"count": 3, "status": "ready", "name": "alpha"},
    {"count": 8, "status": "held",  "name": "beta"},
]
cols = [
    ColumnSpec("name", "name", 9),
    ColumnSpec("count", "count", 7),
    ColumnSpec("status", "status", 5),
]
```

`columns=cols`, no `--cols` — the spec drives order, not the payload's
own `count, status, name` key order (rule 1):

```
name   count  status
alpha  3      ready
beta   8      held
```

`--cols status,name` — the user's sequence wins and also selects
(rule 2), in `table` and `json` alike:

```
status  name
ready   alpha
held    beta
```

```json
[
  {
    "status": "ready",
    "name": "alpha"
  },
  {
    "status": "held",
    "name": "beta"
  }
]
```

No `columns=` at all — payload key order is the fallback:

```
count  status  name
3      ready   alpha
8      held    beta
```

#### Go-vs-payload capability gaps

| Capability | Go (reference) | py / ts | rs / php |
|---|---|---|---|
| Column order source | `table:""` tags, declaration order | `ColumnSpec` list order | `ColumnSpec` list order |
| `priority` hide-on-overflow | implemented | accepted, stored, ignored | accepted, stored, ignored |
| `header != key` | inexpressible via `table:""` | `ValueError` at construction | rejected at construction |
| json/yaml key order | follows the resolved order | follows the resolved order | follows the resolved order |
| `--cols` reorders | yes | yes | yes |
| Built-in formats | `table`, `json`, `yaml`, `csv`, `text` (+ `human`) | same five | `table`, `json`, `yaml` only |
| Ordered columns on the template path | `.Cols` | `cols` (py), `cols` (ts) | `{*}` (php); none (rs) |

`header != key` being inexpressible in Go is not an oversight — it is the
*reason* rule 3 is universal. No SDK may carry a capability the reference
runtime cannot mirror.

#### Conformance status

Python satisfies all five rules, as does Go across all five formats. The
cross-runtime fixtures under `sdk/tests/cross-lang/` execute the contract
against every runtime. Two gaps remain open and matter when writing
portable code:

- **`--format csv` and `--format text` do not exist in rs or php.** Only
  `table`, `json` and `yaml` are portable across all five runtimes today.
  The fixtures record this as `rs-php-no-csv-text`.
- **rs has no ordered-column affordance on the `--template` path.** Go
  exposes `.Cols` and py and ts expose `cols`; php has a `{*}`
  placeholder yielding pre-joined values. The spelling for rs is an open
  decision.

The fixtures compare the **column order re-parsed from each runtime's own
output**, never raw bytes — table padding and YAML block style differ
legitimately between runtimes. Byte-level formatting parity is pinned by
each SDK's own unit tests instead. `csv` output agrees byte-for-byte
across go/py/ts in the default LF mode; the `crlf` option exposes known
quoting divergences.

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

    def render(self, out, data, opts, cols, columns=None):
        prefix = "#" * opts["header-level"]
        for it in data:
            out.write(f"{prefix} {it['name']}\n")

default_registry.register(MarkdownFormatter())
```

`columns` is optional: dispatch forwards the caller's `ColumnSpec` list
only to formatters whose signature accepts it, so the four-argument
`render(out, data, opts, cols)` form keeps working unchanged. Accept it to
honor the column-ordering rules above — `hop_top_kit.output.projection`
exposes `to_rows(data, columns)`, `filter_columns` and `project_payload`
so a custom formatter gets them for free.

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

## Telemetry

`hop_top_kit.telemetry` implements the SDK-side cross-language event
schema in
[`hops/main/sdk/docs/telemetry-event-schema.md`](../docs/telemetry-event-schema.md).
Default-denied: nothing is emitted unless the user has both opted in
(consent file) AND set a non-off `KIT_TELEMETRY_MODE`.

### What's collected

In the default `anon` mode the envelope carries:

- `schema_version`, `sdk_lang` (`py`), `sdk_version`, `mode`
- `installation_id` (anonymised, rotatable — see `kit telemetry inspect`)
- `occurred_at` (RFC 3339 UTC)
- `event` name + caller-supplied `attrs` dict (best-effort redacted)

Never collected by default: command arguments, filesystem paths, env, or
caller identity. The opinionated redactor scrubs emails, IPv4/IPv6,
common token prefixes (`sk-`, `gh{p,o,u,s,r}_`, `xoxb-`), and rewrites
`$HOME` paths.

### Opt in

```python
from hop_top_kit.telemetry import Client

c = Client()              # JSONL sink at ~/.kit-telemetry.jsonl
c.record("my_event", {"k": 1})
c.shutdown()
```

For a remote NDJSON collector, install the optional dep and set
`KIT_TELEMETRY_ENDPOINT`:

```bash
pip install 'hop-top-kit[telemetry-https]'
export KIT_TELEMETRY_ENDPOINT=https://collector.example.com/v1
export KIT_TELEMETRY_SINK=https
```

### Opt out

- Env: `KIT_TELEMETRY_MODE=off` (highest precedence)
- CLI: `kit telemetry disable` (Go-side; writes the consent file)
- Inspect: `kit telemetry inspect` shows resolved mode + consent + path

### Cross-language note

The Go-side `core/telemetry/event.go` struct carries `CommandPath` because
its consumers are all Cobra commands. The SDK envelope swaps that for a
free-form `event` name + `attrs` dict so non-CLI adopters can use it too.
This is a documented divergence; consumers that need command-path
semantics should pass `"command_path"` inside `attrs`.

