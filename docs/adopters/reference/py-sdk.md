# Python SDK Reference

> Every long-form surface of `hop-top-kit` (`sdk/py`): the MCP mount, the
> URI facade, the delegation gate, output formatting rules and worked examples, offline
> enforcement, the telemetry envelope, and LLM messages (content parts,
> tool-call round trips). The package front page is
> [`sdk/py/README.md`](../../../sdk/py/README.md).

## Who this is for

Python authors who already have `hop-top-kit` installed and need the
detail the package front page links out to. For the CLI factory itself
see [`py-api-reference.md`](py-api-reference.md).

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

### Hosting over stdio

For a host that spawns the tool (a desktop client, an editor), serve the
same surface over standard input and output:

```python
from hop_top_kit.mcp import Bridge, serve_stdio

server = serve_stdio(
    Bridge(root),
    mount={"server_name": "my-cli", "server_version": "1.4.0"},
)
server.wait()  # the host closed stdin
```

`serve_stdio(target, *, input, output, log, mount)` takes a `Bridge`
(mounted with `mount`, the `mount_mcp` keyword options as a dict) or an
`McpSurface` from `mount_mcp`, which keeps its own options; passing
`mount` with a surface raises `TypeError`. `input` / `output` / `log`
default to `sys.stdin` / `sys.stdout` (their byte buffers, so the wire
is UTF-8 whatever the locale) / `sys.stderr`; text and binary streams
both work. It returns at once with a `StdioServer`: `wait(timeout=None)`
returns `True` once input ends and every in-flight request is answered,
`done` reports the same without blocking, and `close(timeout=None)`
stops reading and then waits the same way.

The surface is synchronous, so requests run on a thread pool
(`concurrent.futures.ThreadPoolExecutor`, default size) while one reader
thread frames input; output lines are written whole, one at a time.

| Input line | Output |
|---|---|
| a request | one reply line carrying its `id`; requests run concurrently, so replies may arrive out of order |
| a notification (no `id`) | nothing, ever |
| `ping` (2024-11-05) | `{"jsonrpc":"2.0","id":…,"result":{}}` |
| not JSON | `-32700`, `"id": null` |
| a JSON array (batch) | `-32600`, `"id": null`: neither revision allows batches over stdio |
| a non-object, or an object with an `id` but no `method` | `-32600` |
| a response (`result` / `error`, no `method`) | nothing; noted on `log` |
| a blank line | skipped |

Output carries protocol messages only; diagnostics go to `log`. A
handler that raises answers `-32603` and logs the cause.

stdio has no header layer. Era detection uses the body markers alone (the
reserved `_meta` protocolVersion key, `server/discover`), and the
`2026-07-28` header checks (`MCP-Protocol-Version`, `Mcp-Method`,
`Mcp-Name`: V4, V6, V7) do not apply; every other check runs unchanged.
The seam is `McpSurface.dispatch(request, rpc, transport="stdio")`, the
same routing `handle` uses after parsing. Gates that read headers see
none: the mount's `verifier` is called with a header-less request, so a
`kit/auth-required` leaf is refused unless your verifier admits it, and
the `X-Confirm-Token` header gate cannot be satisfied. A mount with a
`confirmation_key` can still confirm over stdio through the MRTR
round-trip, for a `2026-07-28` client that declares form elicitation.

Any other callable is treated as a foreign handler and called with an
HTTP-shaped POST `Request` per message: that serves `2024-11-05`, but
such a handler cannot know the message came over stdio, so `2026-07-28`
requests fail its header checks.

### Positional arguments

A leaf declares its positionals, in order, as `args=[Arg(...)]`;
`Arg(name, required=False)` is Go's `name?`. There is no variadic
marker, as in Go: no `maxItems` is published, so a trailing argument may
repeat and the leaf decides what count it accepts.

```python
from hop_top_kit.mcp import Arg, Command, Flag, Result

Command(
    name="tag",
    short="Tag an item",
    args=[Arg("name"), Arg("note", required=False)],
    flags=[Flag("force", "skip checks", "bool")],
    run=lambda flags, args: Result(stdout=f"tagged {args[0]}\n"),
)
```

The tool's `inputSchema` gains one `args` property beside the flags, the
contract the Go surface publishes (see
[Positional arguments](../guides/expose-cli-over-mcp.md#positional-arguments)):
an array of strings in declared order, described as
`Positional arguments in order: name, note?`, with `minItems` the number
of required arguments and `args` listed in `required` when any is. On
`tools/call` the surface takes `args` out of the arguments, checks it
after the auth gate and before the confirmation gate
(`missing required argument: name`, `"args" must be an array of strings`,
both `isError` at HTTP 200), and calls `run(flags, args)`; every other
key stays in `flags`. A custom `Bridge(runner=...)` reads them from
`Invocation.args`.

A leaf with a flag named `args` keeps that flag, publishes no `args`
array, and its tool description says its positionals cannot be passed.
A leaf that declares no `args` is unchanged: `run(flags)`, the same
schema bytes, and an `args` key is just another flag.

### Options

Keyword-only on `mount_mcp`: `path` (`/mcp`), `server_name`
(`cmdsurface`), `server_version` (`0.0.0`), `spec_versions` (`None` =
both eras), `cache_ttl_ms` (`0`), `cache_scope` (`private`),
`origin_allowlist` (empty, no check), `confirmation_key`,
`extensions`, `tool_name_separator` (`.`), and `audit` (`None`; see
[Audit](#audit)). The option *set* is normative across every kit SDK;
only the spelling is idiomatic.

An explicitly empty `spec_versions`, a negative ttl, an unrecognized
cache scope, or an empty confirmation key raise `MountError` rather than
starting a server that quietly serves nothing.

`tool_name_separator` joins a leaf path into its tool name and splits a
called name back: `"_"` names the leaf `widget add` `widget_add`, for
hosts that admit only `^[A-Za-z0-9_-]{1,64}$`. An empty separator
raises `MountError`, and so does any separator other than `.` that a
command name contains (`data_sync` under `_` would not map back to one
leaf). `tool_name(path, sep)` and `tool_path(name, sep)` are the same
functions, exported.

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

A `kit/auth-required` leaf runs only for a caller the mount's verifier
established: `mount_mcp(bridge, verifier=fn)`, where `fn(request)`
returns an `Identity` or `None`. An `Authorization` header alone is
refused with 401. The verified `caller`, `tenant` and `scopes` reach the
bridge on `Invocation.meta`. See [Admit authenticated callers](../guides/serve-mcp-from-any-sdk.md#admit-authenticated-callers).

An unannotated leaf named `delete`, `remove`, `rm`, `destroy`, `purge`
or `drop` classifies as `destructive-shared` (inferred), so a remote
surface blocks it by default. The table and the rule (annotation absent or
empty; an explicit value, even a malformed one, is never inferred over) live
in `hop_top_kit.sideeffect` and are shared with the CLI delegation gate.
`classify(annotations, leaf_name=...)` takes the leaf name; `Bridge` passes
it. The toolspec module only loads and validates a spec file; it emits
nothing from annotations, so it needs no alignment (Go's `cmdreflect`
reports the inferred tier as `tier_inferred`).

This gate is unrelated to the Factor 10 `safety.py` `--force` helper,
which is a CLI-time TTY check for delegation safety. See
[Delegation gate](#delegation-gate) for how the two relate.

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

### Audit

Go audits every refusal its MCP surface decides before a call runs
through the bridge's sinks. This port has no sink fan-out; the mount
takes one callable instead and calls it with an `AuditRecord` per
refusal, on both eras and over stdio:

```python
import logging

from hop_top_kit.mcp import AuditRecord, mount_mcp

def audit(record: AuditRecord) -> None:
    logging.warning("%s refused: %s %s", record.path, record.error, record.extra)

app = mount_mcp(Bridge(root), audit=audit)
```

`AuditRecord` is a frozen dataclass: `path` (the resolved leaf's path
segments), `surface` (`"mcp"`), `requested_at` (timezone-aware UTC, when
the refusal was decided), `spec_version` (`2024-11-05` or
`2026-07-28`), `error` and `extra`. No caller, arguments or flags, so
there is nothing to redact. `error` is the message of the Go sentinel
the same event is audited with, verbatim, so one sink can match records
from every port; `extra` always carries `mcp_spec_version`.

| Event | `error` | Era |
|---|---|---|
| `kit/auth-required` leaf, caller not established | `cmdsurface: authentication refused` | both |
| confirmation gate unsatisfied: no `X-Confirm-Token`, and no elicitation the client can answer | `mcpsdk: confirmation required` | both |
| the user declined or dismissed the elicitation | `mcpsdk: confirmation declined` | `2026-07-28` |
| a `requestState` failed verification (`extra["mcp_confirm_rejection"]` = `request_state_verification_failed`) | `mcpsdk: confirmation requestState failed verification` | `2026-07-28` |
| the destructive ceiling refused the leaf | `cmdsurface: destructive command blocked on this surface` | both |

An expired but authentic `requestState` is a routine re-prompt and is
not audited, as in Go. Unknown tools, invalid arguments, the origin
allowlist and run outcomes are not audited.

The hook is best-effort: it runs synchronously after the refusal is
decided and before the response is returned, and it can never change
the response. An exception it raises is swallowed. The elicitation
gate's in-memory `rejections` list stays as it was; the hook is additive.

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

- [Serve MCP from any SDK](../guides/serve-mcp-from-any-sdk.md)
  — the polyglot adopter guide
- [Expose your CLI over MCP](../guides/expose-cli-over-mcp.md)
  — the Go reference surface, in depth

## URI facade

`hop_top_kit.uri` exposes Kit's URI integration surface as a thin adapter over
the `hop-top-cite` package (`>=0.1.0`). The SDK does not duplicate URI parsing
or handler generation logic; it delegates to the cite package for
contract-backed behavior.

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
| `csv`   | `.csv`         | `delimiter`, `no-header`, `quote-all`, `crlf`                      |
| `json`  | `.json`        | `indent` (int, default 2; `0` -> compact)                          |
| `table` | (none)         | none                                                               |
| `text`  | `.txt`         | `style` (`kv` / `lines` / `paragraph`), `separator` (kv only)      |
| `yaml`  | `.yaml`,`.yml` | `default-flow-style` (bool, default false)                         |

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

## Delegation gate

`create_app` enforces, before a leaf's handler runs, what the Go and Rust
kit roots enforce (canonical: `go/console/cli`, pinned by the
[`cli-root-v1`](../../../contracts/cli-root-v1/README.md) vectors).
Annotate leaves with `kit/*` tags:

```python
from hop_top_kit.cli import annotate, create_app, is_dry_run
from hop_top_kit.policy import default_policy_loader

app, _ = create_app(
    name="mytool", version="1.0.0", help="...",
    policy_loader=default_policy_loader("mytool"),   # --policy <name>
)

@app.command("rm")
@annotate(side_effect="destructive")                  # needs --confirm
def rm(): ...

@app.command("purge")
@annotate(side_effect="destructive", destructive_token=True)
def purge(): ...                                      # needs --confirm-token

@app.command("add")
@annotate(side_effect="write")
def add():
    if is_dry_run():                                  # kit applied --dry-run
        return preview()
```

Root flags (hidden, like Go's; give them before the command):

| Flag | Behavior |
|------|----------|
| `--confirm yes\|no\|prompt\|auto` | `yes`/`auto` proceed; `no` refuses; `prompt` asks on the controlling terminal (`/dev/tty`, never stdin). Unset or unknown: `prompt` when a terminal exists, else `no`. Applies to destructive tiers and to commands a policy lists under `require_confirm`. |
| `--confirm-token <sha>` | Leaf flag on `kit/destructive-token: required` leaves. The token is the first 6 bytes (12 hex) of `sha256` of the command path (`mytool purge`); `--confirm=yes` alone never suffices. Under `--format json\|yaml` the envelope's `suggested_fix` carries the re-run instruction; otherwise the token is printed. |
| `--max-ops N` | Budget of mutating operations (write and destructive tiers) per engine; over budget is `RATE_LIMITED`, exit 64. One CLI invocation runs one command, so the refusal needs a host sharing a `Gate.engine()` across calls. |
| `--policy <name>` | Loads `$XDG_CONFIG_HOME/<tool>/policies/<name>.yaml` through `policy_loader`. The name must match `^[A-Za-z0-9_-]{1,64}$`, checked before any filesystem access. `kit-default` is reserved and needs no loader. An unreadable or invalid policy is `USAGE` (2), a refusal is `UNAUTHORIZED` (5) and not lifted by `--confirm=yes`. |
| `--dry-run` | Honored by write and destructive tiers (the handler runs with `is_dry_run()` true, confirm skipped, no budget spent); a no-op on `read`; refused (`USAGE`, 2) on `interactive`, `kit/dry-run: opted-out`, untagged or malformed leaves. `Disable(dry_run=True)` drops the flag. |

Only the root's values drive the gate. A leaf that declares its own
`--confirm`, `--max-ops` or `--policy` option keeps it as the leaf's own;
it neither approves the run nor names a policy. (Go and Rust let a
leaf-local `--confirm` or `--max-ops` shadow the root's; Python does not.)
`GlobalFlag` names `confirm`, `max-ops`, `policy` and `dry-run` are
reserved (`ValueError`).

### Side-effect resolution

The gate acts on the declared `kit/side-effect`, else `destructive-shared`
for an unannotated leaf whose name is in the destructive table (above). The
tiers are Go's: legacy `read|write|destructive|interactive` and the
expanded `write-local|write-shared|destructive-local|destructive-shared`.

- **Malformed tags fail closed.** Go refuses a tree with an invalid tier at
  `Root.Validate`; Python has no root validator, so the gate refuses the
  leaf (`USAGE`, exit 2) before policy, confirm or the handler, and the
  policy engine refuses an unknown non-empty tier.
- **`--dry-run` on an inferred leaf never invokes the handler.** Such a
  leaf never promised to honor dry-run, so its handler may really mutate.
  Kit renders a plan (command, one irreversible effect, a warning) in the
  active `--format` and exits 0. Policy still applies; no confirmation is
  asked because nothing can mutate. A declared destructive leaf keeps the
  handler-previews contract above.

### Envelopes

Refusals use the SDK's `CLIError`: `UNAUTHORIZED` (5) for confirm, token and
policy refusals (transient for the confirm and prompt refusals, permanent
for policy and token), `USAGE` (2), `RATE_LIMITED` (64). Python's
`consent_refused_error` (exit 7) is not used, because Go's gate does not.

### Policies

`hop_top_kit.policy` ports Go's `console/cli/policy`: `allow` per class
(an empty list refuses the class; a tier falls back to its legacy class),
`max_ops`, `require_confirm` (globs, `verb:*` shorthand), `unannotated`,
`name`, plus the shipped `kit-default`. `permissions:` rules and `callers:`
rules are validated but not evaluated; the CLI has no transport-established
caller, so only the policy's own rules answer.

### Relation to `safety_guard`

`hop_top_kit.safety.safety_guard` is the Factor 10 `--force` check a
command author calls; it keeps working unchanged. The gate needs no call:
it reads annotations. A destructive leaf can pass both; they answer
different questions (may a human proceed here, vs. has the invocation been
confirmed, policed and budgeted).

### Not ported

`-C/--chdir`, `-c/--config` layering, `Root.Validate`, signature
validators, flag validators, flags accepted after the command (Typer root
options precede the subcommand), the alias store commands and
`config path|paths`. The conformance runner
(`tests/test_cli_root_conformance.py`) replays the delegation and
inference vectors and lists each skip with its reason.

## Offline enforcement

The family-wide `--offline` global disables network access. It is the
highest-precedence override: per-command network opt-ins behave as if
their opt-out flag had been passed. It only forces opt-outs on — it never
un-sets an explicitly passed `--no-*` flag.

Loopback is exempt. `--offline` means "do not talk to the network", not
"do not talk to myself", so `127.0.0.0/8`, `localhost` and `[::1]` stay
reachable. Other hostnames are remote even when they would resolve to
loopback, because performing that resolution is itself network access.
`file:` and `data:` URLs are never refused: they resolve locally.

### With `create_app`: nothing to do

`create_app` registers `--offline` on every app; unlike `--quiet` it has
no `Disable` field, because the flag name is reserved family-wide. On
every dispatch the root callback stamps the marker from the flag, and
when the flag is set it guards `urllib.request.urlopen`. A leaf that
fetches with `urlopen` is refused without consulting anything.

To skip work instead of letting it fail at the transport, read the
marker:

```python
from hop_top_kit.cli import is_offline

@app.command()
def sync():
    if is_offline():
        typer.echo("offline: skipping remote sync", err=True)
        return
    ...
```

The marker is stamped on every dispatch, not only when the flag is
present, so a process that invokes the app twice (a REPL, a test
harness) never inherits the first invocation's `--offline`.

### Without `create_app`

```python
from hop_top_kit import netpolicy

netpolicy.set_offline(True)   # mark the process offline
netpolicy.install()           # once, at start-up
netpolicy.is_offline()        # consult
```

`install()` guards the opener behind module-level
`urllib.request.urlopen`, the chokepoint beneath every caller that does
not build its own. It is idempotent, but it mutates process state: call
it during start-up, never concurrently with in-flight requests.

### Enforcement

The marker alone is advisory — a caller who forgets to consult it still
reaches the wire. Enforcement therefore sits in urllib's opener chain,
ahead of every protocol handler, so a refused request never resolves DNS
or opens a socket. A blocked request raises `OfflineError` rather than
skipping silently:

```text
GET https://api.example.com:8443/v1/models: network disabled by --offline
```

The message names the method and the destination as scheme, host, port
and path only; query, fragment and userinfo never appear, as they may
carry credentials. See
[Offline refusals](../guides/cli-parity-guide.md#offline-refusals) for
the shape every port shares.

Match the refusal by type. `OfflineError` subclasses `OSError`, so an
`except OSError` written for transport failures still catches it — but
it is **not** a `urllib.error.URLError`, so an `except URLError` alone
lets it through:

```python
import urllib.request
from hop_top_kit.netpolicy import OfflineError

try:
    with urllib.request.urlopen(url, timeout=10) as resp:
        body = resp.read()
except OfflineError:
    body = None          # refused by --offline: degrade, don't retry
except OSError as e:     # URLError included: attempted and failed
    raise SystemExit(f"fetch failed: {e}")
```

### Openers `install` cannot reach

`install()` guards the module-level opener and wraps
`urllib.request.build_opener`, so `urlopen(..., context=ssl_ctx)` (which
builds a fresh opener per call) and any opener built through
`urllib.request.build_opener` after `install()` are refused too. Two
openers stay out of reach until you guard them:

- **An `OpenerDirector` assembled by hand.**
- **A `build_opener` imported by name before `install()` ran**
  (`from urllib.request import build_opener` at module import).

Wrap those with `guard`, which is idempotent; `guard(None)` builds a
default opener first:

```python
opener = netpolicy.guard(my_opener)
opener.open(url)
```

### Marker scope

`set_offline` (and so `--offline`) marks the whole process: every
thread, `ThreadPoolExecutor` worker, `loop.run_in_executor` job and
asyncio task is refused once it is set.

To mark a single unit of work offline without touching the rest of the
process, use `offline_scope`:

```python
with netpolicy.offline_scope():
    fetch(url)           # refused
```

A scope reaches asyncio tasks and `asyncio.to_thread` started inside it,
but not bare threads; it can't lift a process-wide mark. Two app
dispatches running at once in different threads share the process flag,
so keep `--offline` dispatches serial.

### Scope

Enforcement covers HTTP(S) through `urllib.request`, which is every
network client in this port today (`aim`, `upgrade`, `llm.URLSource`).
It does **not** cover code that opens a socket by other means: raw
`socket`, `http.client` used without urllib, `httpx`, `requests`,
`grpc` (`routellm_grpc`) or DB-API drivers. For those `--offline` stays
advisory and the call site must consult `is_offline()` itself.

The telemetry HTTPS sink is deliberately **not** guarded. It posts
through `httpx`, outside urllib's opener chain. Telemetry is
logging-class egress: `--offline` stops traffic the user asked for, it
is not a second consent gate on diagnostics. Consent and
`KIT_TELEMETRY_MODE` already govern whether anything is emitted.

## Telemetry

`hop_top_kit.telemetry` implements the SDK-side cross-language event
schema in
[`sdk/docs/telemetry-event-schema.md`](../../../sdk/docs/telemetry-event-schema.md).
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

The HTTPS sink is not refused by `--offline`; see
[Offline enforcement](#offline-enforcement).

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

## LLM messages

`hop_top_kit.llm` mirrors the Go `llm` package's data types; the
[LLM client reference](llm.md) is the source of truth for the contract.
`llm.Message` carries `role` and `content`, plus three optional fields:

| Field | Role | Meaning |
|-------|------|---------|
| `parts` | `user` | `ContentPart` list; when non-empty it replaces `content` |
| `tool_calls` | `assistant` only | `ToolCall(id, name, arguments)` list the model made; `content` may be empty |
| `tool_call_id` | `tool` only | the `ToolCall.id` this result answers; result text in `content` |

`Message("user", "hi")` still works; the new fields default to empty.

### Returning tool results

Replay the model's calls, then send one result per call, linked by ID:

```python
from hop_top_kit import llm

msgs.append(llm.Message(role="assistant", content=resp.content, tool_calls=resp.tool_calls))
for call in resp.tool_calls:
    msgs.append(llm.Message(role="tool", tool_call_id=call.id, content=run(call)))
resp = client.call_with_tools(llm.Request(messages=msgs), tools)
```

`ToolCall.arguments` takes a JSON string, JSON bytes, or a
JSON-serializable value such as a `dict`; empty (`None`, `""`, `b""`)
means `{}`.

### Content parts

```python
part = llm.ContentPart(type=llm.PartType.IMAGE, source=llm.FileSource("cat.png"))
msg = llm.Message(role="user", parts=[llm.ContentPart(type=llm.PartType.TEXT, text="What is this?"), part])
```

`PartType` is `text`, `image`, `audio` or `video`. A part's media comes
from a `MediaSource`: `InlineSource(data, mime_type)`, `URLSource(url)`
(downloaded on `read()`) or `FileSource(path)` (MIME from the
extension). `ContentPart.mime_type` overrides the source's.

`URLSource.read()` treats the URL as untrusted: `http` or `https` only,
no userinfo, every address the host resolves to public (loopback,
private, link-local such as `169.254.169.254`, shared, reserved and
multicast refused, v4-mapped and NAT64 IPv6 judged by their IPv4; the
connection goes to the vetted address, so DNS cannot rebind it), no
redirects, a 30s timeout, at most `max_bytes` (32 MiB;
`llm.MediaTooLargeError` past it), and `--offline` applies. There is no
switch for private hosts: read local media yourself and pass
`InlineSource` or `FileSource`. Adapters that pass a URL to the provider
never read it: `anthropic` (images and PDFs, as `url` sources),
`openai` and `routellm` (images).

### Provider adapters

`create_llm()` / `llm.resolve()` load the built-in adapters on first
use; both are stdlib-only (`urllib`, no provider SDK):

| Scheme | Module | Default endpoint |
|--------|--------|------------------|
| `anthropic` | `hop_top_kit.llm_anthropic` | `https://api.anthropic.com` |
| `openai`, `openrouter`, `xai`, `groq`, `together`, `fireworks`, `deepseek`, `mistral` | `hop_top_kit.llm_openai` | each provider's API |
| `lmstudio` | `hop_top_kit.llm_openai` | `http://localhost:1234/v1` (no key; `/v1` appended) |

Each implements `complete`, `stream`, `call_with_tools` and
`stream_with_tools`. A stream yields `llm.Token`s, then one with
`done=True`; `stream.outcome()` then returns the `llm.StreamOutcome`
(content, thinking blocks, tool calls, finish reason, usage).

```python
client = llm.create_llm("anthropic://claude-opus-5-5")
req = llm.Request(messages=msgs, max_tokens=4096, thinking=llm.ThinkingConfig(2048))
stream = client.stream_with_tools(req, tools)
for tok in stream:
    print(tok.thinking or tok.content, end="")
turn = stream.outcome()
msgs.append(llm.Message(role="assistant", content=turn.content,
                        tool_calls=turn.tool_calls, thinking=turn.thinking))
```

Anthropic only: `Request.thinking` (budget at least 1024, below
`max_tokens`) streams thinking deltas as `Token.thinking`; replay
`thinking` blocks unchanged on the assistant message. `cache_control=
llm.CacheControl(ttl="5m" | "1h" | "")` on a message or `ToolDef` marks
a prompt-cache breakpoint. `Usage` carries `cache_read_tokens` and
`cache_creation_tokens`; `usage.cost(llm.Price(input=, output=,
cache_read=, cache_write=))` prices it per million tokens.

`Request.tool_choice=llm.ToolChoice(mode, name="")` constrains tool use
on `call_with_tools` and `stream_with_tools`; `None` leaves the provider
default. Modes (`llm.ToolChoiceMode`): `auto`, `any` (at least one
call; `"required"` on the OpenAI wire), `none`, and `tool` with `name`,
the tool as defined (sent under its wire name). `anthropic`, `openai`
(and OpenAI-compatible) and `routellm` raise `llm.LLMError` before
sending for an unknown mode, a name missing with `tool` or set with
another mode, a name no tool has, or any choice on `complete` / `stream`
(no tools); `llm.check_tool_choice(choice, tools)` returns the reason.

`Request.stream_events=True` (anthropic, the OpenAI-compatible adapter
and `routellm`) adds two kinds of token to a stream; unset, a stream
yields exactly what it always has. Each token carries one thing:

| Field | When | Holds |
|-------|------|-------|
| `tool_call` | a `tool_use` block starts, gets argument JSON, stops (openai: a `tool_calls[i]` fragment completes its id and name, adds arguments; every open call ends, in index order, on the `finish_reason` chunk) | `llm.ToolCallEvent`: `kind` `start` (`index`, `id`, `name`), `delta` (`arguments_delta`, a raw fragment), `end` (`call`, exactly `outcome().tool_calls[index]`) |
| `usage` | the provider reports usage (`message_start`, `message_delta`; openai: the chunk after the last choice, asked for with `stream_options.include_usage` only when opted in) | the turn's usage so far: cumulative, never a delta; the last equals `outcome().usage` |

`name` is the tool's name as defined. Parallel calls interleave, told
apart by `index`. A call open when the stream fails gets no `end`.

```python
req = llm.Request(messages=msgs, stream_events=True)
for tok in client.stream_with_tools(req, tools):
    if tok.usage and tok.usage.total_tokens > budget:
        break  # enforce a budget mid-stream
```

`llm.resolve(uri, hermetic=True)` (also `llm.load_config` and
`llm.create_llm`) reads nothing ambient: the config is the URI alone
(`api_key`, `base_url`, host form; no fallbacks unless passed), and the
adapter, told by `ResolvedConfig.hermetic`, ignores `ANTHROPIC_BASE_URL`,
`ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_WORKSPACE_ID`, `LLM_BASE_URL` and
llm.yaml. `llm.parse_router_config(extras, hermetic=True)` ignores the
`ROUTELLM_*` variables, and so does the `routellm` adapter in both
modes. In in-process mode the `routellm` Controller library may still
read its own environment.

The Python `Client` publishes no bus events: `on_request` and
`on_response` run in process with the full request and response.
`hop_top_kit.llm_events` builds the metadata-only payloads Go publishes
as `kit.ai.request.started` and `kit.ai.response.received`
(`request_start_payload(op, req, tools=n)`,
`request_end_payload(op, resp, tool_calls, duration=s)`, each with
`to_dict()`): counts, sizes, settings and usage, never content. Python
has no secret store; keys come from the URI, the environment or
llm.yaml.

Transport rules, every adapter:

- **Keys**: `api_key` URI param, `LLM_API_KEY`, llm.yaml
  `providers.<scheme>.api_key`, then `<SCHEME>_API_KEY`. A key not from
  the URI reaches an endpoint the URI names (`?base_url=`, host form)
  only when it is loopback or the same origin as the adapter's default,
  llm.yaml `base_url` or `LLM_BASE_URL`; no key goes over plain http to a
  remote host or to an endpoint with userinfo, `\`, `%`, whitespace or a
  control character (`llm.KeyEndpointError`). `llm.apply_api_key(uri)`
  returns the URI carrying the resolved key under the same rules.
- **Anthropic env**: `ANTHROPIC_BASE_URL` (below a configured base URL),
  `ANTHROPIC_AUTH_TOKEN` (bearer, when not blank) and
  `ANTHROPIC_WORKSPACE_ID`; the token and workspace go only to a trusted
  origin, and a hermetic adapter reads none of them. The openai adapter
  reads no environment credential.
- **Retries**: two more attempts on 408, 409, 429, 5xx and transport
  failures (`x-should-retry` overrides); a server wait
  (`retry-after-ms`, `retry-after` seconds) is honored only within
  (0, 60s], else backoff (0.5s base, 8s cap, jitter).
- **No redirects**: a 3xx is `llm.HTTPStatusError`.
- **Limits**: params `max_body_bytes` (32 MiB) and `max_event_bytes`
  (one SSE event, 8 MiB); past either, `llm.ResponseTooLargeError`.
- **Truncation**: a stream ending without its final event (Anthropic
  `message_stop`; OpenAI `[DONE]` or a `finish_reason`) raises
  `llm.StreamTruncatedError`.
- **System messages** (anthropic): every system message, in order,
  joined with a blank line, empty ones skipped; a `cache_control` on one
  ends a system text block there.
- **Errors**: 401 `AuthError`, 403 `PermissionDeniedError` (a subclass
  of `AuthError`), 404 `ModelError`, 429
  `RateLimitError`, 5xx `HTTPStatusError` (`overloaded`, 529, among
  them), other 4xx `APIError`, transport `TransportError`; an SSE error
  event maps by its type. `llm.is_fallbackable()` is true for rate
  limits, 5xx and transport failures. Every credential sent is
  `REDACTED` in error text.

### RouteLLM modes and model field

`routellm://` takes either model shape a RouteLLM server advertises on
`/v1/models`, classified as in Go and TypeScript
(`routellm_adapter.parse_model_field()`):

- `router_name:threshold` (`routellm://mf:0.7`): the text after the
  last colon parses as a finite float in Go `ParseFloat` syntax
  (decimal, hex mantissa with `p` exponent, `_` between digits). The
  threshold must lie in [0, 1]: `routellm: threshold 1.5000 out of
  range [0, 1]` (`llm.ThresholdInvalidError`).
- tier name (`routellm://private`): anything else non-empty, colons
  included (`mf:abc`, `a:b`, `mf:NaN` are tiers). The server resolves
  router and threshold from its tier config; an unknown tier fails at
  the server (`llm.ModelError`).

The adapter runs in one of two modes (`adapter.mode`):

| Mode | When | Routes | Models |
|------|------|--------|--------|
| `http` | a RouteLLM server base URL is configured | the server's OpenAI-compatible `/v1` API through `llm_openai.OpenAIAdapter` | router pair as `router-<name>-<threshold>` (Go `FormatFloat 'f' -1`: `mf:5e-1` → `router-mf-0.5`); tier verbatim |
| `in_process` | no base URL | RouteLLM's `Controller` in this process (`pip install routellm`) | router pair only; a tier raises `llm.LLMError` naming the base URL settings |

Base URL, first non-empty wins (Go's order): the provider's `base_url`
(URI `?base_url=` or host form `routellm://host:6060/private`,
`LLM_BASE_URL`, llm.yaml `base_url`), then `ROUTELLM_BASE_URL`, then
extras `routellm.base_url`. `/v1` is appended unless present. Unlike Go
and TypeScript, an unset base URL does not fall back to
`http://localhost:6060`; it selects in-process mode. A hermetic config
ignores `ROUTELLM_BASE_URL` in both modes.

HTTP mode is the openai adapter end to end: streaming, `stream_events`,
tool calling, tool names (`?tool_names=`), `max_body_bytes` /
`max_event_bytes`, retries, the credential guard (a key goes over plain
http only to loopback) and its errors, labelled `routellm`. The key
(`?api_key=`, `LLM_API_KEY` or `ROUTELLM_API_KEY`) is sent as a bearer
token when set. In-process mode keeps the extras `strong_model`,
`weak_model`, `routers` and `router_config`; extras `api_base` sets the
Controller's upstream model API base (formerly the provider
`base_url`, which now selects HTTP mode). Extras `eva` validation runs
after `complete` in both modes.

```python
llm.resolve("routellm://private?base_url=http://localhost:6060")  # tier, HTTP
llm.resolve("routellm://mf:0.7?base_url=http://localhost:6060")   # router-mf-0.7, HTTP
llm.resolve("routellm://mf:0.7")                                  # in-process Controller
```

### RouteLLM wire mapping

`hop_top_kit.routellm_adapter` sends the OpenAI chat shape, the same
mapping as the Go `openai` adapter; in in-process mode `map_messages()`
returns the list it hands the Controller:

| Message | Wire |
|---------|------|
| `assistant` + `tool_calls` | `tool_calls: [{id, type: "function", function: {name, arguments}}]`, `arguments` a JSON string; `content` omitted when empty |
| `tool` + `tool_call_id` | `{role: "tool", content, tool_call_id}`, one message per result |
| `user` (or empty role) + `parts` | `content` array: `text`; `image_url` (URL passthrough, else a base64 data URI); a PDF becomes a `file` part |
| neither | `{role, content}`, as before |

It raises `llm.LLMError` instead of degrading to plain text:
`tool_calls` or `tool_call_id` on the wrong role, a `tool` message
without `tool_call_id`, a tool result with `parts` or `tool_calls`,
assistant tool calls with `parts`, a call without an `id`, arguments
that are not valid JSON, `parts` on a role other than `user`, an image
without a MIME type or source, and `audio` or `video` parts.

### Tool names

Providers restrict tool names; `hop_top_kit.llm_toolnames` holds each
provider's rule and the sanitizer every kit SDK shares
([contracts/llm-v1/tool-names.md](../../../contracts/llm-v1/tool-names.md)):

| Rule | Accepted names |
|------|----------------|
| `ANTHROPIC_TOOL_NAMES` | `^[a-zA-Z0-9_-]{1,128}$` |
| `OPENAI_TOOL_NAMES` (OpenAI-compatible, `routellm`) | `^[a-zA-Z0-9_-]{1,64}$` |
| `GEMINI_TOOL_NAMES` | `^[a-zA-Z_][a-zA-Z0-9_.:-]{0,127}$` |

```python
from hop_top_kit.llm_toolnames import OPENAI_TOOL_NAMES, ToolNameMode, ToolNames

OPENAI_TOOL_NAMES.sanitize("pane.open")  # "pane_open"
names = ToolNames.build(OPENAI_TOOL_NAMES, ToolNameMode.SANITIZE, tools)
wire_tools = names.defs(tools)            # send these
calls = names.calls(wire_calls)           # original names back
```

A valid name goes out unchanged. Otherwise each run of other characters
becomes one `_` (Gemini also gets a leading `_` when the name does not
start with a letter or `_`), and a name still too long, or with no ASCII
letter or digit left, is cut and ends in `_` plus 8 hex digits of its
SHA-256: a 65-character name to OpenAI goes out as 55 characters +
`_635361c4`.

`ToolNames.build` raises `llm.ToolNameError` (an `llm.LLMError`, not
fallbackable; `provider`, `reason`, `name`, `other`) for the first tool
whose name is empty (`empty`), not UTF-8, such as a lone surrogate
(`invalid_utf8`), defined twice (`duplicate`), or mapped to the same
wire name as an earlier tool (`collision`, e.g. `pane.open` and
`pane:open`; `other` is the earlier one). `llm.ToolNameReason` lists
the reasons.

Opt out of rewriting with the `tool_names` provider param:
`?tool_names=strict` sends names as given and fails a name the provider
rejects with reason `invalid`; `sanitize` is the default; any other
value fails adapter construction with `llm.LLMError`.

Every adapter applies its rule on every entry point: tool definitions
(`call_with_tools`, `stream_with_tools`) and the calls replayed in
history (all four), and maps returned calls, in responses and stream
outcomes, back to the caller's names. `anthropic` uses
`ANTHROPIC_TOOL_NAMES`; the `openai` schemes and `routellm` use
`OPENAI_TOOL_NAMES`, so a `resp.tool_calls` list from another adapter
goes back verbatim: `routellm://mf:0.7` sends `pane.open` as
`pane_open`, `routellm://mf:0.7?tool_names=strict` as given.

`routellm` calls tools through the OpenAI wire mapping
(`hop_top_kit.llm_openai`): the server (HTTP mode) or the Controller
(in-process mode) gets the mapped `tools`, and the routed model's calls
come back under the caller's names.
